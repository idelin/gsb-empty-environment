package netsec

import (
	"context"
	"sync"
	"time"
)

// Limiter throttles replay delivery. Wait blocks until one token is available
// or ctx is done. A nil Limiter means unlimited delivery.
type Limiter interface {
	Wait(ctx context.Context) error
}

// Clock abstracts time for the token bucket. Production code uses RealClock;
// tests inject a fake clock to assert rate behavior without sleeping.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) (<-chan time.Time, func() bool)
}

// RealClock is backed by the standard time package.
type RealClock struct{}

// Now returns the current wall-clock time.
func (RealClock) Now() time.Time { return time.Now() }

// NewTimer creates a real timer and returns its channel plus a stop function
// that reports whether it stopped the timer before it fired.
func (RealClock) NewTimer(d time.Duration) (<-chan time.Time, func() bool) {
	t := time.NewTimer(d)
	return t.C, t.Stop
}

// LimiterFactory builds one Limiter per replay, so each subscriber gets an
// independent token bucket instead of sharing a global one.
type LimiterFactory func() Limiter

// TokenBucket is a simple refill-based token limiter. Tokens accrue
// continuously at RatePerSecond up to Burst; every Wait consumes one token.
type TokenBucket struct {
	mu           sync.Mutex
	clock        Clock
	rate         float64
	burst        float64
	tokens       float64
	last         time.Time
	intervalNano int64
}

// NewTokenBucket creates a limiter allowing rate events per second with the
// given burst. Non-positive rate or burst yields an unlimited limiter
// (Wait never blocks). A nil clock selects RealClock.
func NewTokenBucket(ratePerSecond float64, burst int, clock Clock) *TokenBucket {
	if clock == nil {
		clock = RealClock{}
	}
	tb := &TokenBucket{
		clock: clock,
		rate:  ratePerSecond,
		burst: float64(burst),
		last:  clock.Now(),
	}
	if ratePerSecond > 0 {
		tb.intervalNano = int64(float64(time.Second) / ratePerSecond)
		if tb.intervalNano <= 0 {
			tb.intervalNano = 1
		}
	}
	if burst > 0 {
		tb.tokens = float64(burst)
	}
	return tb
}

// refillLocked adds tokens accrued since the last check, capped at burst.
func (tb *TokenBucket) refillLocked(now time.Time) {
	if tb.rate <= 0 || tb.burst <= 0 {
		return
	}
	elapsed := now.Sub(tb.last).Seconds()
	if elapsed <= 0 {
		return
	}
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.burst {
		tb.tokens = tb.burst
	}
	tb.last = now
}

// Allow reports whether one token can be consumed without waiting.
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refillLocked(tb.clock.Now())
	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// Wait consumes one token, sleeping until enough time has accrued when the
// bucket is empty. It honors ctx cancellation while sleeping.
func (tb *TokenBucket) Wait(ctx context.Context) error {
	if tb.rate <= 0 || tb.burst <= 0 {
		return nil
	}
	for {
		tb.mu.Lock()
		now := tb.clock.Now()
		tb.refillLocked(now)
		if tb.tokens >= 1 {
			tb.tokens--
			tb.mu.Unlock()
			return nil
		}
		need := (1 - tb.tokens) / tb.rate
		d := time.Duration(need * float64(time.Second))
		if d <= 0 {
			d = time.Duration(tb.intervalNano)
		}
		ch, stop := tb.clock.NewTimer(d)
		tb.mu.Unlock()

		select {
		case <-ctx.Done():
			stop()
			return ctx.Err()
		case <-ch:
		}
	}
}
