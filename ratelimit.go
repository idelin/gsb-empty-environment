package netsec

import (
	"sync"
	"time"
)

// Clock is the time source used by rate limiters. Production code uses
// SystemClock; tests inject a controllable fake clock.
type Clock interface {
	Now() time.Time
}

// SystemClock reads the wall clock.
type SystemClock struct{}

// Now returns the current wall-clock time.
func (SystemClock) Now() time.Time { return time.Now() }

// tokenBucket is a thread-safe refilling token bucket with an optional
// burst allowance. A zero or negative rate disables limiting (tokens are
// always available).
type tokenBucket struct {
	mu         sync.Mutex
	rate       float64 // tokens per second
	burst      float64
	tokens     float64
	lastRefill time.Time
	clock      Clock
}

func newTokenBucket(rate float64, burst int, clock Clock) *tokenBucket {
	if clock == nil {
		clock = SystemClock{}
	}
	b := float64(burst)
	if b < 1 {
		b = 1
	}
	return &tokenBucket{
		rate:       rate,
		burst:      b,
		tokens:     b,
		lastRefill: clock.Now(),
		clock:      clock,
	}
}

// allow consumes one token when the bucket is not limited or has budget.
func (t *tokenBucket) allow() bool {
	if t.rate <= 0 {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.clock.Now()
	elapsed := now.Sub(t.lastRefill).Seconds()
	if elapsed > 0 {
		t.tokens += elapsed * t.rate
		if t.tokens > t.burst {
			t.tokens = t.burst
		}
		t.lastRefill = now
	}
	if t.tokens < 1 {
		return false
	}
	t.tokens--
	return true
}
