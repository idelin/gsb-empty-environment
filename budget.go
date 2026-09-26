package tailsampler

import "time"

// tokenBucket caps how many traces may be kept per second. When the budget is
// exhausted the sampler drops immediately instead of buffering traces while
// waiting for new tokens, so memory stays bounded.
type tokenBucket struct {
	rate     float64
	capacity float64
	tokens   float64
	last     time.Time
}

func newTokenBucket(ratePerSecond float64, now time.Time) *tokenBucket {
	return &tokenBucket{
		rate:     ratePerSecond,
		capacity: ratePerSecond,
		tokens:   ratePerSecond,
		last:     now,
	}
}

func (b *tokenBucket) allow(now time.Time) bool {
	if b.rate <= 0 {
		return false
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > b.capacity {
			b.tokens = b.capacity
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
