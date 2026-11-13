package netsec

import (
	"errors"
	"fmt"
)

// Common table validation errors. Callers normally inspect a Report instead of
// matching these errors directly.
var (
	ErrMalformedTable = errors.New("malformed hash table")
	ErrBadIndex       = errors.New("symbol index out of range")
	ErrChainCycle     = errors.New("hash chain cycle")
	ErrWorkBudget     = errors.New("hash chain work budget exceeded")
)

// GNUTable is the decoded, untrusted GNU hash table.
//
// Bloom must contain a power-of-two number of 64-bit words. BloomShift must be
// in the range 0..31. Chains is indexed by (symbolIndex - SymOffset).
type GNUTable struct {
	NBuckets   uint32
	SymOffset  uint32
	Bloom      []uint64
	BloomShift uint32
	Buckets    []uint32
	Chains     []uint32
}

// GNUHashTable and GNU are descriptive aliases for GNUTable.
type GNUHashTable = GNUTable

// GNU is a short alias for GNUTable.
type GNU = GNUTable

// SysVTable is the decoded, untrusted System V hash table. Next is indexed by
// symbol number; zero terminates a chain.
type SysVTable struct {
	Buckets []uint32
	Next    []uint32
}

// SysVHashTable and SysV are descriptive aliases for SysVTable.
type SysVHashTable = SysVTable

// SysV is a short alias for SysVTable.
type SysV = SysVTable

// NewGNUTable constructs a GNU table without changing any supplied arrays.
func NewGNUTable(nbuckets, symoffset uint32, bloom []uint64, bloomShift uint32, buckets, chains []uint32) *GNUTable {
	return &GNUTable{
		NBuckets:   nbuckets,
		SymOffset:  symoffset,
		Bloom:      bloom,
		BloomShift: bloomShift,
		Buckets:    buckets,
		Chains:     chains,
	}
}

// NewSysVTable constructs a System V table without changing any supplied
// arrays.
func NewSysVTable(buckets, next []uint32) *SysVTable {
	return &SysVTable{Buckets: buckets, Next: next}
}

// Validate checks the GNU table's declared sizes against symbolCount.
func (t *GNUTable) Validate(symbolCount int) error {
	return t.validate(symbolCount)
}

// Validate checks the System V table's declared sizes against symbolCount.
func (t *SysVTable) Validate(symbolCount int) error {
	return t.validate(symbolCount)
}

func (t *GNUTable) validate(symbolCount int) error {
	if t == nil {
		return fmt.Errorf("%w: GNU table is nil", ErrMalformedTable)
	}
	if t.NBuckets == 0 {
		return fmt.Errorf("%w: GNU nbuckets is zero", ErrMalformedTable)
	}
	if uint64(len(t.Buckets)) != uint64(t.NBuckets) {
		return fmt.Errorf("%w: GNU buckets length %d does not match nbuckets %d",
			ErrMalformedTable, len(t.Buckets), t.NBuckets)
	}
	if len(t.Bloom) == 0 || len(t.Bloom)&(len(t.Bloom)-1) != 0 {
		return fmt.Errorf("%w: GNU Bloom word count %d is not a nonzero power of two",
			ErrMalformedTable, len(t.Bloom))
	}
	if t.BloomShift > 31 {
		return fmt.Errorf("%w: GNU Bloom shift %d is outside 0..31", ErrMalformedTable, t.BloomShift)
	}
	if uint64(t.SymOffset) > uint64(symbolCount) {
		return fmt.Errorf("%w: GNU symoffset %d exceeds symbol count %d",
			ErrMalformedTable, t.SymOffset, symbolCount)
	}
	neededChains := symbolCount - int(t.SymOffset)
	if neededChains < 0 || len(t.Chains) != neededChains {
		return fmt.Errorf("%w: GNU chains length %d does not match required %d",
			ErrMalformedTable, len(t.Chains), neededChains)
	}
	return nil
}

func (t *SysVTable) validate(symbolCount int) error {
	if t == nil {
		return fmt.Errorf("%w: System V table is nil", ErrMalformedTable)
	}
	if len(t.Buckets) == 0 {
		return fmt.Errorf("%w: System V bucket count is zero", ErrMalformedTable)
	}
	if len(t.Next) != symbolCount {
		return fmt.Errorf("%w: System V next length %d does not match symbol count %d",
			ErrMalformedTable, len(t.Next), symbolCount)
	}
	return nil
}
