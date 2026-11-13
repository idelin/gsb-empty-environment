package grant

import "time"

// FakeClock is a controllable monotonic clock for tests. Its readings are
// generated from a fixed base plus an offset, so they always compare
// consistently, mirroring monotonic host time without wall-clock ambiguity.
type FakeClock struct {
	base   time.Time
	offset time.Duration
}

// NewFakeClock builds a FakeClock anchored at a deterministic base.
func NewFakeClock() *FakeClock {
	return &FakeClock{base: time.Unix(1_700_000_000, 0)}
}

// Now returns the current fake time.
func (f *FakeClock) Now() time.Time { return f.base.Add(f.offset) }

// Advance moves the clock forward.
func (f *FakeClock) Advance(d time.Duration) { f.offset += d }
