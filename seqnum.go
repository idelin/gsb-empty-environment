// Package seqwin implements a fixed-width sequence-number window receiver
// with compact acknowledgement ranges, plus an incremental frame parser
// that tolerates arbitrarily chunked byte input.
package seqwin

import "fmt"

// Relation describes how sequence number a relates to b.
type Relation int

const (
	// Ambiguous means the modular distance is exactly half the sequence
	// space, so neither "a is newer" nor "a is older" may be assumed.
	Ambiguous Relation = iota
	// Older means a is behind b.
	Older
	// Equal means a == b.
	Equal
	// Newer means a is ahead of b.
	Newer
)

func (r Relation) String() string {
	switch r {
	case Older:
		return "older"
	case Equal:
		return "equal"
	case Newer:
		return "newer"
	default:
		return "ambiguous"
	}
}

// Space is a fixed-width sequence number space of 2^Bits values.
type Space struct {
	Bits uint
	Size uint64 // 2^Bits
	Half uint64 // 2^(Bits-1)
	Mask uint64 // Size-1
}

// NewSpace builds a sequence space of the given bit width (2..32).
func NewSpace(bits uint) (*Space, error) {
	if bits < 2 || bits > 32 {
		return nil, fmt.Errorf("seqwin: bit width %d out of range [2,32]", bits)
	}
	size := uint64(1) << bits
	return &Space{Bits: bits, Size: size, Half: size >> 1, Mask: size - 1}, nil
}

// Valid reports whether s is a member of the space.
func (sp *Space) Valid(s uint64) bool { return s < sp.Size }

// Compare relates a to b using serial-number arithmetic. A distance of
// exactly Half is Ambiguous and must not be resolved arbitrarily.
func (sp *Space) Compare(a, b uint64) Relation {
	diff := (a - b) & sp.Mask
	switch {
	case diff == 0:
		return Equal
	case diff < sp.Half:
		return Newer
	case diff > sp.Half:
		return Older
	default:
		return Ambiguous
	}
}

// Dist returns the forward modular distance from a to b (b-a mod 2^Bits).
func (sp *Space) Dist(a, b uint64) uint64 { return (b - a) & sp.Mask }

// Add returns s+n reduced into the space.
func (sp *Space) Add(s, n uint64) uint64 { return (s + n) & sp.Mask }
