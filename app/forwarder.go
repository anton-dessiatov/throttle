package app

import (
	"context"
	"log"
	"net"

	"golang.org/x/time/rate"
)

const bufSize = 64 * 1024

func forward(from net.Conn, to net.Conn, limiters []*rate.Limiter, ctx context.Context) error {
	buf := make([]byte, bufSize)
	for {
		n, err := from.Read(buf)
		if err != nil {
			log.Printf("Failed to read from ingress conn: %v", err)
			return err
		}
		if n > 0 {
			for _, lim := range limiters {
				err = lim.WaitN(ctx, n)
				if err != nil {
					return err
				}
			}
			to.Write(buf)
		}
	}
}
