package app

import (
	"context"
	"crypto/rand"
	"io"
	"net"
	"reflect"
	"testing"

	"golang.org/x/time/rate"
)

func TestForwarder(t *testing.T) {
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go forward{
		from:     client,
		to:       server,
		limiters: []*rate.Limiter{},
	}.run(ctx)

	bytes := make([]byte, 999)
	n, err := rand.Read(bytes)
	if err != nil || n != 999 {
		t.Errorf("Failed to get random bytes for test: %v", err)
		return
	}

	n, err = server.Write(bytes)
	if err != nil || n != 999 {
		t.Errorf("Failed to write bytes to pipe: %v", err)
		return
	}

	readBytes := make([]byte, 999)
	n, err = io.ReadFull(client, readBytes)
	if err != nil || n != 999 {
		t.Errorf("Failed to read bytes at the client side: %v", err)
		return
	}

	if !reflect.DeepEqual(readBytes, bytes) {
		t.Error("Bytes sent and received differ")
	}
}
