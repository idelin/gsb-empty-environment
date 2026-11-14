package netsec

import "time"

// Clock abstracts time so rate limiting is deterministic in tests.
type Clock interface {
	Now() time.Time
}

// systemClock is the production Clock backed by wall time.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// tokenBucket is a self-contained, allocation-free-after-construction rate
// limiter. Tokens accrue at RatePerSecond up to Burst; each admitted request
// consumes one token.
type tokenBucket struct {
	tokens        float64
	last          time.Time
	ratePerSecond float64
	burst         float64
}

func newTokenBucket(ratePerSecond float64, burst int, now time.Time) *tokenBucket {
	return &tokenBucket{
		tokens:        float64(burst),
		last:          now,
		ratePerSecond: ratePerSecond,
		burst:         float64(burst),
	}
}

// allow refills based on elapsed time and attempts to consume one token.
func (b *tokenBucket) allow(now time.Time) bool {
	if b.ratePerSecond <= 0 {
		return false
	}
	if now.After(b.last) {
		b.tokens += now.Sub(b.last).Seconds() * b.ratePerSecond
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
