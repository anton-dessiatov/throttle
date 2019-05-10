package limiter

import (
	"net"
	"testing"
)

func TestDoubleClose(t *testing.T) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Errorf("Failed with %v", err)
	}
	l = NewRateLimitingListener(l, 123, 456)

	l.Close()
	l.Close()
}
