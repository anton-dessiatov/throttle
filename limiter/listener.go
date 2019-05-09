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
	connectionClosed  chan *LimitedConnection

	limiter         *rate.Limiter
	currentLimits   RateLimits
	currentLimitsMu *sync.RWMutex
	updateLimits    chan RateLimits
}

// RateLimits defines limits for RateLimitingListener to apply. Limits are
// expressed in bytes per second
type RateLimits struct {
	ListenerLimit   rate.Limit
	ConnectionLimit rate.Limit
}

// NewRateLimitingListener wraps given listener into a RateLimitingListener with
// given initial limits
func NewRateLimitingListener(listener net.Listener, limits RateLimits) *RateLimitingListener {
	result := &RateLimitingListener{
		inner:             listener,
		activeConnections: make(map[*LimitedConnection]struct{}),
		close:             make(chan struct{}),
		connectionClosed:  make(chan *LimitedConnection),

		limiter:         CreateLimiter(limits.ListenerLimit),
		currentLimits:   limits,
		currentLimitsMu: new(sync.RWMutex),
		updateLimits:    make(chan RateLimits),
	}

	go result.dispatcher()

	return result
}

// UpdateLimits changes rate limits that apply to all connection that were
// accepted (or will be accepted in future)
func (l *RateLimitingListener) UpdateLimits(newLimits RateLimits) {
	select {
	case l.updateLimits <- newLimits:
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
	multiLimiter := NewMultiLimiter([]*rate.Limiter{
		l.limiter,
		CreateLimiter(l.currentLimits.ConnectionLimit),
	})
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
	close(l.close)
	return l.inner.Close()
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
			l.limiter = CreateLimiter(newLimits.ListenerLimit)
			l.currentLimits = newLimits
			l.currentLimitsMu.Unlock()

			// No need to read-lock because we are the only ones capable of modifying
			// limits and we've already done it.
			for conn := range l.activeConnections {
				limiter := NewMultiLimiter([]*rate.Limiter{
					l.limiter,
					CreateLimiter(l.currentLimits.ConnectionLimit),
				})
				conn.UpdateLimiter(limiter)
			}
		case closedConn := <-l.connectionClosed:
			delete(l.activeConnections, closedConn)

		case <-l.close:
			return
		}
	}
}
