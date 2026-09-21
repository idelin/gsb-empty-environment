// Package seqwin implements a fixed-width sequence-number window receiver
// with compact acknowledgment ranges, plus an incremental frame parser.
package seqwin

import "errors"

// ErrAmbiguous is returned when a sequence-number distance equals exactly
// half the sequence space (e.g. 128 for 8-bit numbers). At that distance
// "newer" and "older" cannot be distinguished, so callers must not guess.
var ErrAmbiguous = errors.New("seqwin: distance is exactly half the sequence space (ambiguous)")

// Space describes a fixed-width sequence-number space of Bits bits.
// Arithmetic wraps modulo 2^Bits; comparisons use the half-space rule.
type Space struct {
	Bits uint
	mod  uint32
	half uint32
}

// NewSpace builds a sequence space of the given bit width (2..31).
func NewSpace(bits uint) Space {
	if bits < 2 || bits > 31 {
		panic("seqwin: bits must be in [2, 31]")
	}
	mod := uint32(1) << bits
	return Space{Bits: bits, mod: mod, half: mod >> 1}
}

// Mod returns 2^Bits, the size of the sequence space.
func (s Space) Mod() uint32 { return s.mod }

// Half returns half the sequence space; a distance of exactly Half is ambiguous.
func (s Space) Half() uint32 { return s.half }

// Add returns (a + n) mod 2^Bits.
func (s Space) Add(a, n uint32) uint32 { return (a + n) & (s.mod - 1) }

// Diff returns the signed distance (a - b) mapped into (-half, +half):
// positive means a is ahead of b, negative means a is behind b.
// ok is false when the distance is exactly half the space, which is
// ambiguous and must not be resolved by guessing.
func (s Space) Diff(a, b uint32) (d int32, ok bool) {
	raw := (a - b) & (s.mod - 1)
	if raw == s.half {
		return 0, false
	}
	if raw > s.half {
		return int32(raw) - int32(s.mod), true
	}
	return int32(raw), true
}

// Less reports whether a is strictly behind b in sequence space.
// It returns ErrAmbiguous when the distance is exactly half the space.
func (s Space) Less(a, b uint32) (bool, error) {
	d, ok := s.Diff(a, b)
	if !ok {
		return false, ErrAmbiguous
	}
	return d < 0, nil
}
