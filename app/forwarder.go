package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/time/rate"
)

// Forwarder is the machinery to forward traffic between a pair of two net.Conn
// while limiting the bandwidth with a set of rate.Limiter
type Forwarder struct {
	from           net.Conn
	to             net.Conn
	limiters       *MultipleLimiters
	updateLimiters chan []*rate.Limiter
}

// CreateForwarder creates Forwarder structure based on required arguments
func CreateForwarder(from net.Conn, to net.Conn, limiters []*rate.Limiter) Forwarder {
	return Forwarder{
		from:           from,
		to:             to,
		limiters:       CreateMultipleLimiters(limiters),
		updateLimiters: make(chan []*rate.Limiter),
	}
}

// UpdateLimiters sets new rate limiters for a given forwarder. If given context
// is canceled before Forwarder accepts new limits, function returns.
func (f Forwarder) UpdateLimiters(ctx context.Context, newLimiters []*rate.Limiter) {
	select {
	case f.updateLimiters <- newLimiters:
	case <-ctx.Done():
	}
}

// Run forwards traffic between connections respecting bandwidth limits.
// If context gets cancelled, forwarding is cancelled as well and function
// returns nil.
//
// This function also returns nil in case any of connections gets closed more
// or less normally (including remote peer forcibly closing the connection)
//
// Never call Run for a given Forwarder on more than from one goroutine
// simultaneously.
func (f *Forwarder) Run(ctx context.Context) error {
	buf := make([]byte, f.limiters.GetBufferSize())
	abort := make(chan struct{})
	localUpdateLimiters := make(chan []*rate.Limiter)
	defer close(abort)
	go func() {
		for {
			select {
			case newLimiters := <-f.updateLimiters:
				// This will result in waking up forwarder goroutine that waits on
				// limiters. Forwarder goroutine is ready for that and will poll
				// localUpdateLimiters afterwards. Even if we don't manage to
				// deliver a value to localUpdateLimiters before primary goroutine
				// reaches polling point, worst thing that happens is that we spin
				// a round or two without actually waiting for rate limiters.
				f.limiters.Abort()
				localUpdateLimiters <- newLimiters
			case <-abort:
				return
			}
		}
	}()
	netOpDone := make(chan struct{})
	var nr int
	var nw int
	var err error
	for {
		select {
		case newLimiters := <-localUpdateLimiters:
			f.limiters = CreateMultipleLimiters(newLimiters)
			buf = make([]byte, f.limiters.GetBufferSize())
		default:
		}

		go func() {
			// We take care about occasionally waking up and forwarding traffic even
			// if buffer is not full yet to avoid introducing too much of a latency
			// in case of slow producers.
			f.from.SetReadDeadline(time.Now().Add(NetPollInterval))
			nr, err = f.from.Read(buf)
			netOpDone <- struct{}{}
		}()

		select {
		case <-netOpDone:
			if err != nil && !isTimeout(err) {
				if isConnectionClosed(err) {
					return nil
				}
				log.Printf("Failed to read from conn: %v", err)
				return err
			}
		case <-ctx.Done():
			return nil
		} // select

		if nr > 0 {
			err = f.limiters.Wait(ctx, nr)
			// In case of ErrorAborted we could maybe spin a round or two until
			// code in the beginning of for loop creates new limiters for us. That's
			// not an issue - in the worst case we will slightly exceed bandwidth for
			// a moment, but we will recover soon.
			if err != nil && err != ErrorAborted {
				return err
			}

			go func() {
				nw, err = f.to.Write(buf[0:nr])
				netOpDone <- struct{}{}
			}()

			select {
			case <-netOpDone:
				if err != nil {
					if isConnectionClosed(err) {
						return nil
					}
					log.Printf("Failed to write to conn: %v", err)
					return err
				}
				if nw != nr {
					return io.ErrShortWrite
				}
			case <-ctx.Done():
				return nil
			} // select
		} // if nr > 0
	} // for
}

// isTimeout returns true if given error is network timeout
func isTimeout(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		return opErr.Timeout()
	}
	return false
}

// isConnectionClosed returns true if error indicates that connection was
// legitimately closed by peer (either EOF or ECONNRESET or EPIPE) or connection
// was closed due to socket shutdown (EPIPE)
func isConnectionClosed(err error) bool {
	if err == io.EOF {
		return true
	}

	if opErr, ok := err.(*net.OpError); ok {
		if syscallErr, ok := opErr.Err.(*os.SyscallError); ok {
			if syscallErr.Err == syscall.ECONNRESET || syscallErr.Err == syscall.EPIPE {
				return true
			}
		}
	}

	// This looks bad, but that's what Golang's http2 library does :) Check yourself
	// golang/net/http2/server.go:641
	str := err.Error()
	if strings.Contains(str, "use of closed network connection") {
		return true
	}

	return false
}

// MultipleLimiters encapsulates a set of rate limiters with additional
// structures needed to wait for all of them simultaneously
type MultipleLimiters struct {
	limiters     []*rate.Limiter
	reservations []*rate.Reservation
	abort        chan struct{}
}

// ErrorAborted is returned from MultipleLimiters.Wait if MultipleLimiters.Abort
// was called on a parallel goroutine.
var ErrorAborted = errors.New("aborted")

// CreateMultipleLimiters creates MultipleLimieters structure from a slice of
// rate limiters
func CreateMultipleLimiters(limiters []*rate.Limiter) *MultipleLimiters {
	return &MultipleLimiters{
		limiters:     limiters,
		reservations: make([]*rate.Reservation, 0, len(limiters)),
		abort:        make(chan struct{}),
	}
}

// Abort terminates all waits on MultipleLimiters making them return with
// ErrorAborted. Once Abort is called, all future calls to Wait will return with
// ErrorAborted as well.
func (ml MultipleLimiters) Abort() {
	close(ml.abort)
}

// GetBufferSize returns size of rx buffer to use with given set of rate limiters
func (ml MultipleLimiters) GetBufferSize() int {
	if len(ml.limiters) == 0 {
		return MaxBufferSize
	}
	minLimit := rate.Limit(math.MaxFloat64)
	for _, limiter := range ml.limiters {
		limit := limiter.Limit()
		if limit < minLimit {
			minLimit = limit
		}
	}

	result := GetBufferSize(Limit(minLimit))
	return result
}

// Wait blocks the goroutine until each rate limiter approves allocating n bytes
func (ml *MultipleLimiters) Wait(ctx context.Context, n int) error {
	select {
	case <-ml.abort:
		return ErrorAborted
	default:
	}
	ml.reservations = ml.reservations[:0]

	defer func() {
		for _, r := range ml.reservations {
			r.Cancel()
		}
	}()

	var delay time.Duration = 0
	now := time.Now()
	var infiniteWait = false

	for _, lim := range ml.limiters {
		// A special case is required because rate.Limiter with zero limit, but
		// non-zero burst, still allows for events. We, however, in case of zero
		// limit would like to block until either aborted or canceled by context.
		if lim.Limit() == rate.Limit(0) {
			infiniteWait = true
			break
		}
		r := lim.ReserveN(now, n)
		if !r.OK() {
			return fmt.Errorf("Failed to reserve %d bytes at a rate limiter", n)
		}

		ml.reservations = append(ml.reservations, r)
	} // for

	if infiniteWait {
		select {
		case <-ml.abort:
			return ErrorAborted
		case <-ctx.Done():
			return nil
		} // select
	}

	for _, r := range ml.reservations {
		if r.DelayFrom(now) > delay {
			delay = r.DelayFrom(now)
		}
	} // for

	// The idea is that if delay is too small, it makes sense to disregard it (but
	// still keep the reservation) so that we hit a bigger delay a bit later when
	// it grows. Otherwise it might as well take us more than delay time to get
	// rescheduled (which is going to hurt performance)
	if delay <= MinRateLimiterDelay {
		ml.reservations = ml.reservations[:0]
		return nil
	}

	t := time.NewTimer(delay)
	defer t.Stop()

	select {
	case <-t.C:
		ml.reservations = ml.reservations[:0]
		return nil
	case <-ml.abort:
		return ErrorAborted
	case <-ctx.Done():
		return nil
	} // select
}
