package fetchquota

import "time"

// bucket is a refillable token bucket. Tokens accrue continuously at
// rate tokens/second up to burst. All math is driven by the injected clock,
// and every method must be called with the scheduler lock held.
type bucket struct {
	burst  float64
	rate   float64 // tokens per second
	tokens float64
	last   time.Time
}

func newBucket(rl RateLimit, now time.Time) *bucket {
	return &bucket{
		burst:  rl.Burst,
		rate:   rl.Rate,
		tokens: rl.Burst,
		last:   now,
	}
}

// advance folds accrued tokens into the balance.
func (b *bucket) advance(now time.Time) {
	if !now.After(b.last) {
		return
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
}

// available reports the balance after refilling to now.
func (b *bucket) available(now time.Time) float64 {
	b.advance(now)
	return b.tokens
}

// take removes one token; the caller must have confirmed availability.
func (b *bucket) take() {
	b.tokens--
}

// refund puts one token back, capped at burst. It is used when an approved
// request is cancelled before being sent.
func (b *bucket) refund() {
	b.tokens++
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
}

// nextFull reports how long from now until at least one token is available.
// It returns 0 when a token is already available and the zero duration when
// the bucket has a zero rate.
func (b *bucket) nextFull(now time.Time) time.Duration {
	if b.tokens >= 1 {
		return 0
	}
	if b.rate <= 0 {
		return 0
	}
	needed := 1 - b.tokens
	secs := needed / b.rate
	d := time.Duration(secs * float64(time.Second))
	// Never round below the true instant: rounding down would cause an
	// extra wake-up at the boundary with no token to give out.
	if float64(d)/float64(time.Second) < secs {
		d += time.Nanosecond
	}
	return d
}
