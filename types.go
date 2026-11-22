package layout

import (
	"errors"
	"fmt"
	"math/big"
)

const (
	// MaxPages is the largest page domain supported by the layout planner.
	MaxPages = 48
	// MaxRegions is the largest number of named regions supported by the
	// layout planner.
	MaxRegions = 8
)

var (
	// ErrNoLayouts is returned when the configuration has no legal layout.
	ErrNoLayouts = errors.New("layout: no legal layouts")
	// ErrRankOutOfRange is returned when a rank is outside [0, Count()).
	ErrRankOutOfRange = errors.New("layout: rank out of range")
	// ErrInvalidLayout is returned when a layout does not satisfy the
	// configuration.
	ErrInvalidLayout = errors.New("layout: invalid layout")
	// ErrStaleCheckpoint is returned when a checkpoint was produced for a
	// different configuration (including a different lease generation).
	ErrStaleCheckpoint = errors.New("layout: stale checkpoint")
	// ErrInvalidCheckpoint is returned for malformed sampler checkpoints.
	ErrInvalidCheckpoint = errors.New("layout: invalid checkpoint")
	// ErrNeedMoreBits is returned by a BitSource, and by a Sampler, when the
	// caller must provide more random bits before sampling can continue.
	ErrNeedMoreBits = errors.New("layout: need more random bits")
	// ErrBitPosition is returned when a resumed bit source is not positioned
	// at the bit offset recorded in the checkpoint.
	ErrBitPosition = errors.New("layout: bit source position does not match checkpoint")
)

// Region describes one named interval in the layout. A region occupies
// Length consecutive pages and its start must be a multiple of Alignment.
//
// A region is movable by default. Pinned fixes it at Start. Stable asks the
// planner to reuse the prior StableMapping with the same Name and Generation.
// Pinned and Stable are mutually exclusive.
type Region struct {
	Name      string
	Length    int
	Alignment int

	Pinned bool
	Start  int

	Stable     bool
	Generation uint64
}

// StableMapping is a mapping from a prior generation. A stable region may
// reuse it only when both the name and the generation match.
type StableMapping struct {
	Name       string
	Generation uint64
	Start      int
	Length     int
}

// Lease describes pages that are still held by readers of an old mapping.
// If Pages is non-empty it is used as an explicit page set and Start/Length
// are ignored; otherwise the lease is the contiguous interval [Start,Start+
// Length). A pinned or stable region may overlap a lease only when the name
// and generation match and the region's whole span is contained in the lease.
// All other regions are excluded until the lease is removed from the
// configuration.
type Lease struct {
	Name       string
	Generation uint64
	Start      int
	Length     int
	Pages      []int
}

// Constraint restricts two starts by
//
//	(start[A] - start[B]) mod Modulus in Allowed
//
// The difference is always reduced to a non-negative residue. Modulus must be
// positive and every Allowed value must be smaller than Modulus. A and B may
// be the same region; in that case the only possible residue is zero.
type Constraint struct {
	A       int
	B       int
	Modulus uint64
	Allowed []uint64
}

// Config is the input to a layout planner.
type Config struct {
	// Pages is the size of the page domain. Pages are numbered 0..Pages-1.
	Pages int
	// Generation is the generation being planned. It is part of the
	// configuration fingerprint and therefore part of sampler checkpoints.
	Generation uint64

	Regions []Region

	// Reserved pages are never assignable, including to stable mappings.
	Reserved []int
	// Stable is the prior-generation mapping catalog used by stable regions.
	Stable []StableMapping
	// Leases are pages still held by old readers.
	Leases []Lease
	// OldReaderPages are pages held by readers that do not expose a reusable
	// named mapping. They are never reusable.
	OldReaderPages []int
	// Constraints are all required simultaneously.
	Constraints []Constraint
}

// Layout is a complete assignment. Starts is indexed by region number, so two
// regions with identical shapes but different names are still distinct
// coordinates in the layout.
type Layout struct {
	Starts []int
}

// Clone returns an independent copy of the layout.
func (l Layout) Clone() Layout {
	if l.Starts == nil {
		return Layout{}
	}
	return Layout{Starts: append([]int(nil), l.Starts...)}
}

// Equal reports whether two layouts have the same start tuple.
func (l Layout) Equal(other Layout) bool {
	if len(l.Starts) != len(other.Starts) {
		return false
	}
	for i := range l.Starts {
		if l.Starts[i] != other.Starts[i] {
			return false
		}
	}
	return true
}

// String renders the start tuple.
func (l Layout) String() string {
	return fmt.Sprint(l.Starts)
}

// Start returns the start of region i.
func (l Layout) Start(i int) int {
	return l.Starts[i]
}

// cloneBig returns an independent big integer.
func cloneBig(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(v)
}
