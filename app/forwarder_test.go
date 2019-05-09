package app

import (
	"context"
	"crypto/rand"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestForwarder(t *testing.T) {
	testForwarderWithLimiters(t, []*rate.Limiter{})
	testForwarderWithLimiters(t, []*rate.Limiter{
		CreateLimiter(Limit(100000)),
		CreateLimiter(Limit(100000)),
	})
}

func testForwarderWithLimiters(t *testing.T, limiters []*rate.Limiter) {
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fwd := CreateForwarder(client, server, limiters)
	go fwd.Run(ctx)

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
	n, err = io.ReadAtLeast(client, readBytes, 999)
	if err != nil || n != 999 {
		t.Errorf("Failed to read bytes at the client side: %v", err)
		return
	}

	if !reflect.DeepEqual(readBytes, bytes) {
		t.Error("Bytes sent and received differ")
	}
}

func TestForwarderZeroLimitAndUpdate(t *testing.T) {
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fwd := CreateForwarder(client, server, []*rate.Limiter{
		CreateLimiter(Limit(0)),
	})
	go fwd.Run(ctx)

	bytes := make([]byte, 100)
	n, err := rand.Read(bytes)
	if err != nil || n != 100 {
		t.Errorf("Failed to get random bytes for test: %v", err)
		return
	}

	writeAndReadDone := make(chan bool)
	go func() {
		n, err = server.Write(bytes)
		if err != nil || n != 100 {
			t.Errorf("Failed to write bytes to pipe: %v", err)
			return
		}
		readBytes := make([]byte, 100)
		n, err = io.ReadAtLeast(client, readBytes, 100)
		if err != nil || n != 100 {
			t.Errorf("Failed to read bytes at the client side: %v", err)
			return
		}

		if !reflect.DeepEqual(readBytes, bytes) {
			t.Error("Bytes sent and received differ")
			writeAndReadDone <- false
		} else {
			writeAndReadDone <- true
		}
	}()

	time.Sleep(50 * time.Millisecond)

	select {
	case <-writeAndReadDone:
		t.Error("Got a result while not expected to do so")
	default:
	}

	fwd.UpdateLimiters(ctx, []*rate.Limiter{
		CreateLimiter(Limit(100000)),
	})

	time.Sleep(50 * time.Millisecond)
	select {
	case result := <-writeAndReadDone:
		if result {
			return
		}
		t.Error("Got an error from writeAndReadDone")
	default:
		t.Error("Didn't got a respose from WriteRead goroutine")
	}
}
