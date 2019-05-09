package limiter

import (
	"golang.org/x/time/rate"
	"time"
)

// MultiLimiter is a set of rate limiters with an option to reserve time slots
// from all of them simultaneously.
type MultiLimiter struct {
	limiters []*rate.Limiter
	burst    int
}

// NewMultiLimiter creates MultiLimiters structure from a slice of rate limiters
func NewMultiLimiter(limiters []*rate.Limiter) *MultiLimiter {
	if len(limiters) == 0 {
		return &MultiLimiter{
			burst: int(^uint(0) >> 1),
		}
	}

	var burst = limiters[0].Burst()
	for _, lim := range limiters {
		if lim.Burst() < burst {
			burst = lim.Burst()
		}
	}

	return &MultiLimiter{
		limiters: limiters,
		burst:    burst,
	}
}

// Burst returns minimal burst size of rate limiters that belong to this
// MultiLimiter
func (ml *MultiLimiter) Burst() int {
	return ml.burst
}

// ReserveN allocates 'n' tokens at 'now' moment of time from all rate limiters
// belonging to this MultiLimiter simultaneously
func (ml *MultiLimiter) ReserveN(now time.Time, n int) *MultiReservation {
	res := make([]*rate.Reservation, 0, len(ml.limiters))
	defer func() {
		for _, r := range res {
			r.Cancel()
		}
	}()
	for _, lim := range ml.limiters {
		// A special case is required because rate.Limiter with zero limit, but
		// non-zero burst, still allows for events. We, however, in case of zero
		// limit would like to block until either aborted or canceled by context.
		if lim.Limit() == rate.Limit(0) {
			return &MultiReservation{infinite: true}
		}
		r := lim.ReserveN(now, n)
		if !r.OK() {
			return &MultiReservation{failed: true}
		}
		res = append(res, r)
	}
	result := &MultiReservation{
		res: res,
	}
	res = nil
	return result
}

// MultiReservation is token bucket reservation obtained from multiple rate
// limiters (with the help of MultiLimiter)
type MultiReservation struct {
	infinite bool
	failed   bool
	res      []*rate.Reservation
}

// DelayFrom calculates a wait duration starting from 'now' to not exceed
// the rate limit.
func (mr *MultiReservation) DelayFrom(now time.Time) time.Duration {
	if mr.infinite || mr.failed {
		return rate.InfDuration
	}

	var delay time.Duration
	for _, r := range mr.res {
		if r.DelayFrom(now) > delay {
			delay = r.DelayFrom(now)
		}
	} // for

	return delay
}
