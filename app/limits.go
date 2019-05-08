package app

import (
	"math"

	"golang.org/x/time/rate"
)

// MinBufferSize defines minimum size for an Rx buffer of a forwarder
const MinBufferSize = 1

// MaxBufferSize defines maximum size for an Rx buffer of a forwarder (and
// consequently max burst size for rate limiters)
const MaxBufferSize = 64 * 1024

func createLimiter(limit Limit) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(limit), getBufferSize(limit))
}

// Calculates minimal buffer size to use with given slice of rate limiters
func getMinBufferSize(limiters []*rate.Limiter) int {
	minLimit := rate.Limit(math.MaxFloat64)
	for _, limiter := range limiters {
		limit := limiter.Limit()
		if limit < minLimit {
			minLimit = limit
		}
	}

	return getBufferSize(Limit(minLimit))
}

func getBufferSize(l Limit) int {
	bufSize := int64(l) / 20 // We aim for 20 transmissions per second to get good precision
	if bufSize < MinBufferSize {
		bufSize = MinBufferSize
	} else if bufSize > MaxBufferSize {
		bufSize = MaxBufferSize
	}
	return int(bufSize)
}
