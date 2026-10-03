package cdcprobe

import (
	"testing"
	"time"
)

func TestRateLimiterTokenBucket(t *testing.T) {
	now := time.Now()
	r := NewRateLimiter(10, 1)
	r.now = func() time.Time { return now }

	// Burst: initial bucket holds 10 tokens.
	for i := 0; i < 10; i++ {
		if !r.Allow() {
			t.Fatalf("token %d of initial burst denied", i)
		}
	}
	if r.Allow() {
		t.Fatalf("expected denial once bucket is empty")
	}
	// Advance 100ms -> 1 token refilled.
	now = now.Add(100 * time.Millisecond)
	if !r.Allow() {
		t.Fatalf("expected allow after refill")
	}
	if r.Allow() {
		t.Fatalf("expected denial: only one token refilled")
	}
}

func TestRateLimiterThrottleAndRestore(t *testing.T) {
	r := NewRateLimiter(100, 5)
	r.Throttle(0.5)
	if got := r.Rate(); got != 50 {
		t.Fatalf("expected rate 50 after throttle, got %v", got)
	}
	if !r.Throttled() {
		t.Fatalf("expected throttled state")
	}
	// Throttle below the floor: clamped to minRate.
	r.Throttle(0.01)
	if got := r.Rate(); got != 5 {
		t.Fatalf("expected clamp to minRate 5, got %v", got)
	}
	r.Restore()
	if got := r.Rate(); got != 100 {
		t.Fatalf("expected restore to 100, got %v", got)
	}
	if r.Throttled() {
		t.Fatalf("expected throttled state cleared")
	}
}
