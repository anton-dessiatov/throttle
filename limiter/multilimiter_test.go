package limiter

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestEmptyMultiLimiter(t *testing.T) {
	ml := NewMultiLimiter([]*rate.Limiter{})
	blocks := []int{2, 256, 65535, 1024 * 1024}
	now := time.Now()
	for _, b := range blocks {
		r := ml.ReserveN(now, b)
		if r.DelayFrom(now) > 0 {
			t.Errorf("Empty multilimter demanded wait for %d tokens", b)
			return
		}
	}
}

func TestInfiniteReservation(t *testing.T) {
	ml := NewMultiLimiter([]*rate.Limiter{
		CreateLimiter(rate.Limit(100)),
		CreateLimiter(rate.Limit(0)),
	})
	now := time.Now()
	r := ml.ReserveN(now, 1)
	delay := r.DelayFrom(now)
	if delay < rate.InfDuration {
		t.Errorf("Zero rate limiter reported wait duration less than infinite: %v", delay)
	}
}

func TestBurst(t *testing.T) {
	lims := []*rate.Limiter{
		rate.NewLimiter(rate.Limit(1), 10),
		rate.NewLimiter(rate.Limit(1), 5),
		rate.NewLimiter(rate.Limit(1), 100),
	}
	ml := NewMultiLimiter(lims)
	burst := ml.Burst()
	if burst != 5 {
		t.Errorf("Expected burst to be minimal (5), got %v", burst)
	}
}

func TestMultiReservation(t *testing.T) {
	lims := []*rate.Limiter{
		rate.NewLimiter(rate.Limit(2), 1),
		rate.NewLimiter(rate.Limit(1), 1),
	}
	ml := NewMultiLimiter(lims)
	now := time.Now()
	r := ml.ReserveN(now, 1) // This should succeed
	delay := r.DelayFrom(now)
	if delay > 0 {
		t.Errorf("Attempt to reserve single token per second resulted in delay: %v", delay)
		return
	}
	r = ml.ReserveN(now, 1) // This should demand waiting for a second
	delay = r.DelayFrom(now)
	if delay != time.Second {
		t.Errorf("Expected a delay of one second, got %v", delay)
		return
	}
	now = now.Add(time.Second * 2)
	r = ml.ReserveN(now, 1) // This should succeed again
	delay = r.DelayFrom(now)
	if delay > 0 {
		t.Errorf("Expected no delays after advancing two seconds. Got %v", delay)
		return
	}
}
