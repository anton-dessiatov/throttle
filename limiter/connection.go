package limiter

import (
	"io"
	"net"
	"sync"
	"time"
)

// LimitedConnection is a wrapper around net.Conn that limits the rate of its
// Read and Write operations based on given MultiLimiter.
type LimitedConnection struct {
	inner net.Conn

	limiterMu      *sync.RWMutex
	limiter        *MultiLimiter
	readNotBefore  time.Time
	writeNotBefore time.Time
	abortWait      chan struct{}

	readDeadline  time.Time
	writeDeadline time.Time
	close         chan struct{}
	whenClosed    func(*LimitedConnection)
	updateLimiter chan *MultiLimiter
}

// NewLimitedConnection creates a LimitedConnection from net.Conn and a
// MultiLimiter
func NewLimitedConnection(inner net.Conn, limiter *MultiLimiter) *LimitedConnection {
	bufSize := limiter.Burst()
	if bufSize > MaxBurstSize {
		bufSize = MaxBurstSize
	}
	return &LimitedConnection{
		inner: inner,

		limiterMu: new(sync.RWMutex),
		limiter:   limiter,
		abortWait: make(chan struct{}),

		close:         make(chan struct{}),
		updateLimiter: make(chan *MultiLimiter),
	}
}

// UpdateLimiter changes the limiter in effect for a given connection. May be
// called concurrently with Read or Write.
func (c *LimitedConnection) UpdateLimiter(newLimiter *MultiLimiter) {
	c.limiterMu.Lock()
	c.limiter = newLimiter
	oldAbortWait := c.abortWait
	c.abortWait = make(chan struct{})
	c.readNotBefore = time.Time{}
	c.writeNotBefore = time.Time{}
	close(oldAbortWait)
	c.limiterMu.Unlock()
}

// LocalAddr is an implementation of net.Conn.LocalAddr
func (c *LimitedConnection) LocalAddr() net.Addr {
	return c.inner.LocalAddr()
}

// RemoteAddr is an implementation of net.Conn.RemoteAddr
func (c *LimitedConnection) RemoteAddr() net.Addr {
	return c.inner.RemoteAddr()
}

// Read is an implementation of net.Conn.Read
func (c *LimitedConnection) Read(b []byte) (read int, err error) {
	return c.rateLimitLoop(&c.readNotBefore, &c.readDeadline, c.inner.Read, b)
}

// Write is an implementation of net.Conn.Write
func (c *LimitedConnection) Write(b []byte) (written int, err error) {
	return c.rateLimitLoop(&c.writeNotBefore, &c.writeDeadline, c.inner.Write, b)
}

// The idea is that we read in chunks equal to max burst allowed by multilimiter
// After reading we attempt to reserve time slot for a read chunk. If we succeed
// we go on. If not, we check what happens before - operation deadline or wait
// time. If that's wait time then simply wait and repeat. If it's a deadline
// then set 'not before' timestamp and wait for it upon next invocation.
func (c *LimitedConnection) rateLimitLoop(notBefore *time.Time,
	deadline *time.Time, innerAct func([]byte) (int, error),
	b []byte) (cntr int, err error) {
	if len(b) == 0 {
		return
	}

	now := time.Now()
	var until time.Time

	// Grab the limiter and abortwait until end of operation.
	c.limiterMu.RLock()
	limiter := c.limiter
	abortWait := c.abortWait
	if now.Before(*notBefore) {
		until = *notBefore
		if !deadline.IsZero() && deadline.Before(until) {
			until = *deadline
		}
	}
	c.limiterMu.RUnlock()

	if !until.IsZero() {
		if c.waitUntil(abortWait, until) {
			err = io.ErrClosedPipe
			return
		}
	}

	burst := limiter.Burst()
	for cntr < len(b) && err == nil {
		var n int
		if burst > len(b)-cntr {
			burst = len(b) - cntr
		}
		n, err = innerAct(b[cntr:][:burst])
		if n == 0 {
			return
		}

		cntr += n
		until = time.Time{}

		now = time.Now()
		r := limiter.ReserveN(now, n)
		act := now.Add(r.DelayFrom(now))
		if now.Before(act) {
			if !deadline.IsZero() && deadline.Before(act) {
				c.limiterMu.RLock()
				// What I want to avoid here is the case when limiter got updated and
				// "Not before"s got reset during limiter update, but we don't know
				// about it and are going to write outdated value to notBefore.
				// A good test for this is checking if our cached abortWait is closed
				select {
				case <-abortWait:
					// Do nothing, our limits are no longer valid
				default:
					*notBefore = act
				}
				c.limiterMu.RUnlock()
				err = timeoutError{}
				return
			}
			until = act
		}
		if !until.IsZero() {
			if c.waitUntil(abortWait, act) {
				err = io.ErrClosedPipe
				return
			}
		}
	}
	return
}

type timeoutError struct{}

func (timeoutError) Error() string { return "deadline exceeded" }

func (timeoutError) Timeout() bool { return true }

func (timeoutError) Temporary() bool { return true }

// SetDeadline is an implementation of net.Conn.SetDeadline
func (c *LimitedConnection) SetDeadline(t time.Time) error {
	err := c.SetReadDeadline(t)
	if err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

// SetReadDeadline is an implementation of net.Conn.SetReadDeadline
func (c *LimitedConnection) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	return c.inner.SetReadDeadline(t)
}

// SetWriteDeadline is an implementation of net.Conn.SetWriteDeadline
func (c *LimitedConnection) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return c.inner.SetWriteDeadline(t)
}

// Close is an implementation of net.Conn.Close
func (c *LimitedConnection) Close() error {
	close(c.close)
	res := c.inner.Close()
	if c.whenClosed != nil {
		c.whenClosed(c)
	}
	return res
}

// Waits until given time or until connection is closed. Returns
// true if connection was closed and false if time has elapsed
// or if wait was aborted by closing or sending on 'abortWait'
func (c *LimitedConnection) waitUntil(abortWait chan struct{}, t time.Time) bool {
	timer := time.NewTimer(t.Sub(time.Now()))
	defer timer.Stop()
	select {
	case <-timer.C:
		return false
	case <-abortWait:
		return false
	case <-c.close:
		return true
	}
}
