// Package counter interprets samples from fixed-width unsigned counters.
//
// A counter of n bits wraps every 2^n increments. Given two adjacent
// samples, the true number of increments between them is ambiguous: any
// count congruent to the observed residue modulo 2^n is possible. This
// package enumerates every increment count that is also consistent with a
// configured maximum growth rate, instead of assuming the counter wrapped
// at most once.
package counter

import "fmt"

// Config describes a fixed-width unsigned counter.
type Config struct {
	// Bits is the counter width, 1..64.
	Bits uint
	// MaxRate is the maximum sustainable growth rate, in increments
	// per second. A candidate increment count is plausible only if it
	// does not exceed MaxRate times the elapsed time.
	MaxRate float64
}

// Sample is a single observation of the counter.
type Sample struct {
	// Value is the raw counter reading. It must fit in Config.Bits.
	Value uint64
	// Epoch is the restart generation. It changes exactly when the
	// counter resets (process restart, device reboot, ...).
	Epoch uint64
	// Time is the observation time in seconds from any monotonic
	// origin. It must be non-decreasing across a sample series.
	Time float64
}

// modulus returns 2^bits. Callers must ensure bits < 64.
func modulus(bits uint) uint64 {
	return uint64(1) << bits
}

// validate checks the configuration and a sample value range.
func (c Config) validate(s Sample) error {
	if c.Bits == 0 || c.Bits > 64 {
		return fmt.Errorf("counter: Bits must be in 1..64, got %d", c.Bits)
	}
	if c.MaxRate < 0 {
		return fmt.Errorf("counter: MaxRate must be >= 0, got %v", c.MaxRate)
	}
	if c.Bits < 64 && s.Value >= modulus(c.Bits) {
		return fmt.Errorf("counter: value %d does not fit in %d bits", s.Value, c.Bits)
	}
	return nil
}

// Deltas returns every increment count between two adjacent samples that
// is consistent with the counter width and the configured maximum rate,
// in ascending order.
//
// Same epoch: the candidates are r, r+m, r+2m, ... where r is the
// wrapped residue (curr-prev) mod 2^bits and m = 2^bits. This covers
// any number of wraps, not just zero or one.
//
// Epoch change: the counter reset to zero at some unknown instant
// between the samples, so increments before the reset are lost. The
// candidates are curr.Value, curr.Value+m, ... — the increments since
// the reset, bounded by the full interval because the reset happened
// no earlier than prev.Time.
//
// A candidate d is plausible iff d <= MaxRate * (curr.Time - prev.Time).
// The result is empty when no candidate fits the rate bound: the jump
// is unexplainable under this configuration.
func (c Config) Deltas(prev, curr Sample) ([]uint64, error) {
	if err := c.validate(prev); err != nil {
		return nil, err
	}
	if err := c.validate(curr); err != nil {
		return nil, err
	}
	if curr.Time < prev.Time {
		return nil, fmt.Errorf("counter: sample time went backwards: %v -> %v", prev.Time, curr.Time)
	}

	var residue uint64
	if curr.Epoch == prev.Epoch {
		// Wrapping subtraction yields (curr - prev) mod 2^64, which
		// is the residue for 64-bit counters; mask for narrower ones.
		residue = curr.Value - prev.Value
		if c.Bits < 64 {
			residue &= modulus(c.Bits) - 1
		}
	} else {
		residue = curr.Value
	}

	limit := c.MaxRate * (curr.Time - prev.Time)
	var out []uint64
	d := residue
	for float64(d) <= limit {
		out = append(out, d)
		if c.Bits == 64 {
			// Adding 2^64 is not representable; a limit that large
			// cannot be expressed either.
			break
		}
		next := d + modulus(c.Bits)
		if next < d { // overflow guard
			break
		}
		d = next
	}
	return out, nil
}

// Explain is a convenience wrapper that classifies a pair of adjacent
// samples. It reports the plausible deltas and whether the pair is a
// restart (epoch change) or an unexplainable jump (no plausible delta).
func (c Config) Explain(prev, curr Sample) (deltas []uint64, restart bool, err error) {
	deltas, err = c.Deltas(prev, curr)
	if err != nil {
		return nil, false, err
	}
	return deltas, curr.Epoch != prev.Epoch, nil
}
