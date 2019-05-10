package limiter

import (
	"net"
	"sync"

	"golang.org/x/time/rate"
)

// RateLimitingListener is a wrapper around net.Listener that limits the rate
// of connection accepted on it.
type RateLimitingListener struct {
	inner             net.Listener
	activeConnections map[*LimitedConnection]struct{}
	close             chan struct{}

	closed        bool
	closeResult   error
	closeResultMu *sync.Mutex

	connectionClosed chan *LimitedConnection

	globalLimiter   *rate.Limiter
	currentLimits   rateLimits
	currentLimitsMu *sync.RWMutex
	updateLimits    chan rateLimits
}

type rateLimits struct {
	GlobalLimit     rate.Limit
	ConnectionLimit rate.Limit
}

// NewRateLimitingListener wraps given listener into a RateLimitingListener with
// given initial limits.
//
// global is bandwidth that all this listener's connections are not allowed to
// exceed when summed up together
//
// perConn is bandwidth that each individual connection is not allowed to
// exceed
func NewRateLimitingListener(listener net.Listener, global, perConn int) *RateLimitingListener {
	var globalLimiter *rate.Limiter
	if global > 0 {
		globalLimiter = CreateLimiter(rate.Limit(global))
	}
	result := &RateLimitingListener{
		inner:             listener,
		activeConnections: make(map[*LimitedConnection]struct{}),
		close:             make(chan struct{}),
		closeResultMu:     new(sync.Mutex),
		connectionClosed:  make(chan *LimitedConnection),

		globalLimiter: globalLimiter,
		currentLimits: rateLimits{
			GlobalLimit:     rate.Limit(global),
			ConnectionLimit: rate.Limit(perConn),
		},
		currentLimitsMu: new(sync.RWMutex),
		updateLimits:    make(chan rateLimits),
	}

	go result.dispatcher()

	return result
}

// UpdateLimits changes rate limits that apply to all connection that were
// accepted (or will be accepted in future)
func (l *RateLimitingListener) UpdateLimits(newGlobal, newPerConn int) {
	select {
	case l.updateLimits <- rateLimits{
		GlobalLimit:     rate.Limit(newGlobal),
		ConnectionLimit: rate.Limit(newPerConn),
	}:
	case <-l.close:
	}
}

// Accept is an implementation of net.Listener.Accept
func (l *RateLimitingListener) Accept() (net.Conn, error) {
	innerConn, err := l.inner.Accept()
	if err != nil {
		return nil, err
	}

	l.currentLimitsMu.RLock()
	multiLimiter := l.createMultiLimiter()
	l.currentLimitsMu.RUnlock()

	limConn := NewLimitedConnection(innerConn, multiLimiter)

	// This is a bit of a hack, but it works fine for our needs
	limConn.whenClosed = func(c *LimitedConnection) {
		select {
		case l.connectionClosed <- c:
		case <-l.close:
		}
	}

	l.activeConnections[limConn] = struct{}{}
	return limConn, nil
}

// Close is an implementation of net.Listener.Close
func (l *RateLimitingListener) Close() error {
	l.closeResultMu.Lock()
	defer l.closeResultMu.Unlock()
	if !l.closed {
		close(l.close)
		l.closeResult = l.inner.Close()
		l.closed = true
		return l.closeResult
	}
	if l.closeResult == nil {
		return nil
	}
	l.closeResult = l.inner.Close()
	return l.closeResult
}

// Addr is an implementation of net.Listener.Addr
func (l *RateLimitingListener) Addr() net.Addr {
	return l.inner.Addr()
}

func (l *RateLimitingListener) dispatcher() {
	for {
		select {
		case newLimits := <-l.updateLimits:

			l.currentLimitsMu.Lock()
			l.globalLimiter = nil
			if newLimits.GlobalLimit > 0 {
				l.globalLimiter = CreateLimiter(rate.Limit(newLimits.GlobalLimit))
			}
			l.currentLimits = newLimits
			l.currentLimitsMu.Unlock()

			// No need to read-lock because we are the only ones capable of modifying
			// limits and we've already done it.
			for conn := range l.activeConnections {
				multiLimiter := l.createMultiLimiter()
				conn.UpdateLimiter(multiLimiter)
			}
		case closedConn := <-l.connectionClosed:
			delete(l.activeConnections, closedConn)

		case <-l.close:
			return
		}
	}
}

func (l *RateLimitingListener) createMultiLimiter() *MultiLimiter {
	limiters := make([]*rate.Limiter, 0, 2)
	if l.globalLimiter != nil {
		limiters = append(limiters, l.globalLimiter)
	}
	if l.currentLimits.ConnectionLimit > 0 {
		limiters = append(limiters, CreateLimiter(l.currentLimits.ConnectionLimit))
	}
	return NewMultiLimiter(limiters)
}
