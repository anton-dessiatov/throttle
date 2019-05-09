package app

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

// Forwarder is the machinery to forward traffic between a pair of two net.Conn
// while limiting the bandwidth with a set of rate.Limiter
type Forwarder struct {
	from net.Conn
	to   net.Conn
}

// CreateForwarder creates Forwarder structure based on required arguments
func CreateForwarder(from net.Conn, to net.Conn) Forwarder {
	return Forwarder{
		from: from,
		to:   to,
	}
}

// BufSize is a buffer size to use for connection forwarding
const BufSize = 64 * 1024

// NetPollInterval is the maximum amount of time spent waiting for data in
// the forwarder receiver
const NetPollInterval = time.Second / 2

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
	buf := make([]byte, BufSize)
	netOpDone := make(chan struct{})
	var nr int
	var nw int
	var err error
	var exit = false
	for !exit {
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
					err = nil
				} else {
					log.Printf("Failed to read from conn: %v", err)
				}
				exit = true
			}
		case <-ctx.Done():
			err = nil
			exit = true
		} // select

		if nr > 0 {
			go func() {
				nw, err = f.to.Write(buf[0:nr])
				netOpDone <- struct{}{}
			}()

			select {
			case <-netOpDone:
				if err != nil {
					if isConnectionClosed(err) {
						err = nil
					} else {
						log.Printf("Failed to write to conn: %v", err)
					}
					exit = true
				}
				if nw != nr {
					err = io.ErrShortWrite
					exit = true
				}
			case <-ctx.Done():
				err = nil
				exit = true
			} // select
		} // if nr > 0
	} // for
	return err
}

// isTimeout returns true if given error is network timeout
func isTimeout(err error) bool {
	if opErr, ok := err.(net.Error); ok {
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
