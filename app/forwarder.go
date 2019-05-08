package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/time/rate"
)

type forward struct {
	from     net.Conn
	to       net.Conn
	limiters []*rate.Limiter
}

func (f forward) run(ctx context.Context) error {
	buf := make([]byte, ForwarderBufSize)
	reservations := make([]*rate.Reservation, 0, len(f.limiters))
	netOpDone := make(chan struct{})
	var nr int
	var nw int
	var err error
	for {
		go func() {
			nr, err = f.from.Read(buf)
			netOpDone <- struct{}{}
		}()

		select {
		case <-netOpDone:
			if err != nil {
				if isConnectionClosed(err) {
					return nil
				}
				log.Printf("Failed to read from ingress conn: %v", err)
				return err
			}
		case <-ctx.Done():
			return nil
		}

		if nr > 0 {
			err = waitMultipleLimiters(ctx, f.limiters, reservations, nr)
			if err != nil {
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
					log.Printf("Failed to write to ingress conn: %v", err)
					return err
				}
				if nw != nr {
					return io.ErrShortWrite
				}
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// isConnectionClosed returns true if error indicates that connection was
// legitimately closed by peer (either EOF or ECONNRESET)
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

// reservations argument is pure optimization. It's just a 'scratch space'.
// Don't store anything in reservations and don't expect to have anything there
// after function returns.
func waitMultipleLimiters(ctx context.Context, limiters []*rate.Limiter, reservations []*rate.Reservation, n int) error {
	reservations = reservations[:0]

	defer func() {
		for _, r := range reservations {
			r.Cancel()
		}
	}()

	var delay time.Duration = 0

	for _, lim := range limiters {
		r := lim.ReserveN(time.Now(), n)
		if !r.OK() {
			return fmt.Errorf("Failed to reserve %d bytes at a rate limiter", n)
		}

		reservations = append(reservations, r)
	}

	for _, r := range reservations {
		if r.Delay() > delay {
			delay = r.Delay()
		}
	}

	if delay == 0 {
		reservations = reservations[:0]
		return nil
	}

	t := time.NewTimer(delay)
	defer t.Stop()

	select {
	case <-t.C:
		reservations = reservations[:0]
		return nil
	case <-ctx.Done():
		return nil
	}
}
