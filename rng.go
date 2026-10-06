// Package wsample provides streaming weighted random sampling without
// replacement over a finite, mutable set of items with positive integer
// weights.
package wsample

import "math/bits"

// Source is an injectable random source. *rand.Rand from the standard
// library already satisfies this interface.
type Source interface {
	// Int63n returns a uniform random integer in [0, n). It panics if n <= 0.
	Int63n(n int64) int64
}

// SplitMix64 is a small, fast, self-contained Source whose entire state is a
// single uint64, which makes it trivial to export and restore.
type SplitMix64 struct {
	state uint64
}

// NewSplitMix64 returns a SplitMix64 seeded with seed.
func NewSplitMix64(seed uint64) *SplitMix64 {
	return &SplitMix64{state: seed}
}

// State exports the current state of the generator.
func (s *SplitMix64) State() uint64 {
	return s.state
}

// SetState restores a state previously obtained from State. Successive
// draws after SetState are identical to those of the original generator.
func (s *SplitMix64) SetState(state uint64) {
	s.state = state
}

// Uint64 returns the next 64-bit random value.
func (s *SplitMix64) Uint64() uint64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Int63n returns a uniform random integer in [0, n) using rejection
// sampling, so results are unbiased for every n.
func (s *SplitMix64) Int63n(n int64) int64 {
	if n <= 0 {
		panic("wsample: Int63n with non-positive n")
	}
	mask := uint64(1)<<uint(bits.Len64(uint64(n-1))) - 1
	for {
		v := s.Uint64() & mask
		if v < uint64(n) {
			return int64(v)
		}
	}
}
