package app

import (
	"time"

	"golang.org/x/time/rate"
)

// MinBufferSize defines minimum size for an Rx buffer of a forwarder
const MinBufferSize = 1

// MaxBufferSize defines maximum size for an Rx buffer of a forwarder (and
// consequently max burst size for rate limiters)
const MaxBufferSize = 64 * 1024

// MinRateLimiterDelay is the minimal amount of time that we consider
// significant to block forwarder. If we find out that we need
// to block transmission for a smaller period of time, we don't block and expect
// the time that we didn't wait to accumulate in the rate limiter to eventually
// exceed this threshold.
const MinRateLimiterDelay = 5 * time.Millisecond

// NetPollInterval is how much time we are allowed to spend in conn.Read.
// Once this time elapses, we will forward whatever we have managed to read
// even if Rx buffer is not full yet.
const NetPollInterval = time.Second / 2

// CreateLimiter creates rate.Limiter for a given bandwidth limit with a burst
// size equal to buffer size returned by GetBufferSize for a bandwidth limit.
func CreateLimiter(limit Limit) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(limit), GetBufferSize(limit))
}

// GetBufferSize returns size of the buffer to be used for Rx for connections
// with given bandwidth limit.
//
// Returned buffer size is no bigger than MaxBufferSize and no less than
// MinBufferSize
func GetBufferSize(l Limit) int {
	if l == Limit(0) {
		return MaxBufferSize
	}
	// We aim for 20 transmissions per second to get good precision. Decrease this
	// value to get better performance, but less precision.
	bufSize := int64(l) / 20
	if bufSize < MinBufferSize {
		bufSize = MinBufferSize
	} else if bufSize > MaxBufferSize {
		bufSize = MaxBufferSize
	}
	return int(bufSize)
}
