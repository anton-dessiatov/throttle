package limiter

import (
	"io"
	"math/rand"
	"net"
	"reflect"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

const TestBufLen = 100 * 1000

func TestNoDataModifications(t *testing.T) {
	c1, unwrapped := net.Pipe()
	defer c1.Close()
	defer unwrapped.Close()
	wrapped := NewLimitedConnection(c1, NewMultiLimiter([]*rate.Limiter{
		CreateLimiter(rate.Limit(1024 * 1024)),
	}))
	defer wrapped.Close()

	buf := make([]byte, TestBufLen)
	if n, err := rand.Read(buf); err != nil || n != TestBufLen {
		t.Error("Failed to get random bytes for a buffer")
		return
	}

	readBuf := make([]byte, TestBufLen)

	writeDone := make(chan bool)
	readDone := make(chan bool)
	go func() {
		if n, err := unwrapped.Write(buf); err != nil || n != TestBufLen {
			t.Error("Failed to write random bytes to one end of pipe")
			writeDone <- false
		} else {
			writeDone <- true
		}
	}()

	go func() {
		if n, err := io.ReadFull(wrapped, readBuf); err != nil || n != TestBufLen {
			t.Error("Failed to read random bytes from other end of pipe")
			readDone <- false
		} else {
			readDone <- true
		}
	}()

	if writeRes := <-writeDone; !writeRes {
		return
	}
	if readRes := <-readDone; !readRes {
		return
	}

	if !reflect.DeepEqual(buf, readBuf) {
		t.Error("Data was modified after going through limited connection (Read)")
		return
	}

	go func() {
		if n, err := wrapped.Write(buf); err != nil || n != TestBufLen {
			t.Error("Failed to write random bytes to one end of pipe")
			writeDone <- false
		} else {
			writeDone <- true
		}
	}()

	go func() {
		if n, err := io.ReadFull(unwrapped, readBuf); err != nil || n != TestBufLen {
			t.Error("Failed to read random bytes from other end of pipe")
			readDone <- false
		} else {
			readDone <- true
		}
	}()

	if writeRes := <-writeDone; !writeRes {
		return
	}
	if readRes := <-readDone; !readRes {
		return
	}

	if !reflect.DeepEqual(buf, readBuf) {
		t.Error("Data was modified after going through limited connection (Write)")
		return
	}
}

func TestReadDeadline(t *testing.T) {
	c1, unwrapped := net.Pipe()
	defer c1.Close()
	defer unwrapped.Close()
	wrapped := NewLimitedConnection(c1, NewMultiLimiter([]*rate.Limiter{
		rate.NewLimiter(100, 10),
	}))
	defer wrapped.Close()

	buf := make([]byte, TestBufLen)
	if n, err := rand.Read(buf); err != nil || n != TestBufLen {
		t.Error("Failed to get random bytes for a buffer")
		return
	}

	readBuf := make([]byte, TestBufLen)

	readDone := make(chan int)
	go func() {
		const chunkSize = 10
		var pos int
		var n int
		var err error

		for pos < len(buf) && err == nil {
			if n, err = unwrapped.Write(buf[pos:][:chunkSize]); err != nil || n != chunkSize {
				return
			}
			pos += n
		}
	}()

	go func() {
		wrapped.SetReadDeadline(time.Now().Add(time.Millisecond * 200))
		n, err := wrapped.Read(readBuf)
		t.Logf("N: %d, err: %v", n, err)
		if err == nil {
			t.Error("Expected to fail with a timeout, but executed successfully")
			readDone <- 0
			return
		}
		if err, ok := err.(net.Error); ok && err.Timeout() {
			if n < 20 {
				t.Errorf("Expected to read at least 20 bytes, but got %d", n)
				readDone <- 0
			} else {
				readDone <- n
			}
			return
		}
		t.Errorf("Read failed with an error: %v", err)
		readDone <- 0
	}()

	n := <-readDone
	if n == 0 {
		return
	}

	if !reflect.DeepEqual(readBuf[:n], buf[:n]) {
		t.Error("Read buffer doesn't match beginning of write buffer")
	}
}
