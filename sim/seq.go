package sim

import "math/big"

// Seq is an extended-precision sequence number (arbitrary size, no wraparound).
// It is immutable: every operation returns a new value.
type Seq struct{ v big.Int }

// NewSeq builds a Seq from a uint64.
func NewSeq(u uint64) Seq { return Seq{v: *new(big.Int).SetUint64(u)} }

// SeqFromBig builds a Seq from a big.Int (copied).
func SeqFromBig(b *big.Int) Seq { return Seq{v: *new(big.Int).Set(b)} }

// Add returns s + n.
func (s Seq) Add(n uint64) Seq {
	return Seq{v: *new(big.Int).Add(&s.v, new(big.Int).SetUint64(n))}
}

// Sub returns s - o as a signed big.Int.
func (s Seq) Sub(o Seq) *big.Int { return new(big.Int).Sub(&s.v, &o.v) }

// Diff returns (s - o) as uint64. Callers must ensure it fits.
func (s Seq) Diff(o Seq) uint64 { return s.Sub(o).Uint64() }

// Cmp returns -1, 0 or +1 comparing s to o.
func (s Seq) Cmp(o Seq) int { return s.v.Cmp(&o.v) }

// Less reports s < o.
func (s Seq) Less(o Seq) bool { return s.Cmp(o) < 0 }

// Eq reports s == o.
func (s Seq) Eq(o Seq) bool { return s.Cmp(o) == 0 }

func (s Seq) String() string { return s.v.String() }
