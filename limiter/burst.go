package limiter

import (
	"golang.org/x/time/rate"
)

// MinBurstSize defines minimum size for an limiter burst
const MinBurstSize = 1

// MaxBurstSize defines maximum size for a limiter burst
const MaxBurstSize = 64 * 1024

// CreateLimiter creates rate.Limiter for a given bandwidth limit with a burst
// size equal to buffer size returned by GetBufferSize for a bandwidth limit.
func CreateLimiter(limit rate.Limit) *rate.Limiter {
	return rate.NewLimiter(limit, GetGoodBurst(limit))
}

// GetGoodBurst returns burst size that allows to precisely limit rate
//
// Returned burst size is no bigger than MaxBurstSize and no less than
// MinBurstSize
func GetGoodBurst(l rate.Limit) int {
	if l == rate.Limit(0) {
		return MaxBurstSize
	}
	// We aim for 20 bursts per second to get good precision. Decrease this
	// value to get better performance, but less precision.
	burstSize := int64(l) / 20
	if burstSize < MinBurstSize {
		burstSize = MinBurstSize
	} else if burstSize > MaxBurstSize {
		burstSize = MaxBurstSize
	}
	return int(burstSize)
}
