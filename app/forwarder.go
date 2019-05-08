package app

import (
	"context"
	"io"
	"log"
	"net"

	"golang.org/x/time/rate"
)

type forward struct {
	from     net.Conn
	to       net.Conn
	limiters []*rate.Limiter
}

func (f forward) run(ctx context.Context) error {
	buf := make([]byte, ForwarderBufSize)
	netOpDone := make(chan struct{})
	for {
		var n int
		var err error
		go func() {
			n, err = f.from.Read(buf)
			netOpDone <- struct{}{}
		}()

		select {
		case <-netOpDone:
			if err != nil {
				if err == io.EOF {
					return nil // Assume graceful completion because of EOF
				}
				log.Printf("Failed to read from ingress conn: %v", err)
				return err
			}
		case <-ctx.Done():
			return nil
		}

		if n > 0 {
			for _, lim := range f.limiters {
				// TODO: This is a bit naive. Instead of consequent waits, we should
				// calculate a time slice big enough for all limiters not to overflow
				// and only then make a blocking wait on that time slice
				err = lim.WaitN(ctx, n)
				if err != nil {
					return err
				}
			}

			go func() {
				n, err = f.to.Write(buf)
				netOpDone <- struct{}{}
			}()

			select {
			case <-netOpDone:
				if err != nil {
					if err == io.EOF {
						return nil // Assume graceful completion because of EOF
					}
					log.Printf("Failed to write to ingress conn: %v", err)
					return err
				}
			case <-ctx.Done():
				return nil
			}
		}
	}
}
