package cdcprobe

import (
	"sync"
	"time"
)

// RateLimiter is a dynamically reconfigurable token-bucket limiter.
// The monitor lowers the rate when lag breaches the alert threshold
// and restores it once the pipeline recovers.
type RateLimiter struct {
	mu        sync.Mutex
	rate      float64 // tokens (events) per second
	maxRate   float64
	minRate   float64
	tokens    float64
	last      time.Time
	now       func() time.Time // injectable clock for tests
	throttled bool
}

// NewRateLimiter creates a limiter allowing rate events/second.
// minRate is the floor used during dynamic throttling.
func NewRateLimiter(rate, minRate float64) *RateLimiter {
	return &RateLimiter{
		rate:    rate,
		maxRate: rate,
		minRate: minRate,
		tokens:  rate,
		last:    time.Now(),
		now:     time.Now,
	}
}

// Allow reports whether one event may proceed, consuming a token.
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	elapsed := now.Sub(r.last).Seconds()
	if elapsed < 0 {
		elapsed = 0 // guard against clock moving backwards
	}
	r.last = now
	r.tokens += elapsed * r.rate
	if r.tokens > r.rate {
		r.tokens = r.rate
	}
	if r.tokens < 1 {
		return false
	}
	r.tokens--
	return true
}

// Rate returns the current limit in events/second.
func (r *RateLimiter) Rate() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rate
}

// Throttle multiplies the current rate by factor (clamped to minRate),
// e.g. factor 0.5 halves the throughput.
func (r *RateLimiter) Throttle(factor float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rate *= factor
	if r.rate < r.minRate {
		r.rate = r.minRate
	}
	r.throttled = true
}

// Restore returns the limiter to its original maximum rate.
func (r *RateLimiter) Restore() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rate = r.maxRate
	r.throttled = false
}

// Throttled reports whether the limiter is currently throttled.
func (r *RateLimiter) Throttled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.throttled
}
