package retry

import (
	"hash/fnv"
	"time"
)

// BackoffConfig describes exponential backoff with bounded deterministic
// jitter. Delay(attempt) = BaseDelay * Multiplier^attempt, clamped to
// MaxDelay, then multiplied by a jitter factor in [1-Jitter, 1+Jitter].
type BackoffConfig struct {
	BaseDelay  time.Duration
	Multiplier float64
	MaxDelay   time.Duration
	// Jitter is the relative jitter magnitude, e.g. 0.2 means +/-20%.
	Jitter float64
	// Seed makes the jitter deterministic; identical (seed, call, attempt)
	// triples always produce the same delay, so tests and the discrete-event
	// simulation are fully reproducible.
	Seed uint64
}

// Delay computes the wait before attempt attempt (0-based: the first retry
// is attempt 1). callID identifies the logical call so that two concurrent
// calls get different jitter sequences.
func (b BackoffConfig) Delay(callID uint64, attempt int) time.Duration {
	if b.BaseDelay <= 0 {
		return 0
	}
	mult := b.Multiplier
	if mult <= 1 {
		mult = 2
	}
	d := float64(b.BaseDelay)
	for i := 0; i < attempt; i++ {
		d *= mult
		if b.MaxDelay > 0 && d >= float64(b.MaxDelay) {
			d = float64(b.MaxDelay)
			break
		}
	}
	if b.Jitter > 0 {
		h := fnv.New64a()
		var buf [16]byte
		putUint64(buf[:8], b.Seed)
		putUint64(buf[8:], callID)
		_, _ = h.Write(buf[:])
		putUint64(buf[:8], uint64(attempt))
		_, _ = h.Write(buf[:8])
		// Map hash to [0, 1) without importing math for the conversion.
		unit := float64(h.Sum64()%1_000_000) / 1_000_000.0
		factor := 1 + (unit*2-1)*b.Jitter
		if factor < 0 {
			factor = 0
		}
		d *= factor
	}
	return time.Duration(d)
}

func putUint64(b []byte, v uint64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (uint(i) * 8))
	}
}
