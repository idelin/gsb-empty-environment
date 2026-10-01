// Package sparse implements a sparse array: a logical array of length n in
// which almost every position holds a default value. Only exception
// positions are stored physically, so physical cost is O(#exceptions +
// #pending range-add layers), independent of the logical length n.
//
// Core semantics:
//   - Point update (Set) stores an explicit exception. An explicitly stored
//     0 is a real exception and is never confused with a missing position.
//   - Range add (RangeAdd) is recorded as an O(1) lazy layer; huge default
//     regions are never iterated.
//   - Range sum (RangeSum) combines stored exceptions, lazy layers and the
//     arithmetic default contribution.
//   - SetDefault changes the default value. Positions without an explicit
//     exception follow the new default. The caller chooses whether
//     exceptions equal to the OLD default follow too (followOld); exceptions
//     equal to the NEW default are always reclaimed. SetDefault is also the
//     compaction point: pending layers are folded into the exceptions
//     (O(E*L)) and cleared, never touching default-only regions.
//   - Snapshot is an O(1) copy-on-write read-only view. Locate (position by
//     cumulative value) is only available on non-negative snapshots, where
//     prefix sums are monotone.
package sparse

import (
	"fmt"
	"sort"
)

// entry is one explicitly stored exception.
type entry struct {
	pos uint64 // absolute position
	val int64  // stored value; effective value adds layers with seq > wm
	wm  uint64 // layer watermark: layers with seq <= wm never apply to this entry
}

// layer is a lazy range-add record: every position in [lo, hi] (inclusive)
// gains delta, unless a later point Set overrode that position.
type layer struct {
	lo, hi uint64
	delta  int64
	seq    uint64
}

// view is the read-only core shared by Array and Snapshot.
type view struct {
	n      uint64
	def    int64
	exc    []entry // sorted by pos
	layers []layer // sorted by seq
	nonNeg bool    // every effective value is known to be >= 0
}

// Array is a mutable sparse array.
type Array struct {
	view
	seq          uint64 // sequence number assigned to each layer
	excShared    bool   // exc is shared with a snapshot; clone before write
	layersShared bool   // layers is shared with a snapshot; clone before write
}

// Snapshot is an immutable view of an Array at the time Snapshot was called.
type Snapshot struct {
	view
}

// New returns an array of logical length n with default value 0.
func New(n uint64) *Array { return NewWithDefault(n, 0) }

// NewWithDefault returns an array of logical length n with the given default.
func NewWithDefault(n uint64, def int64) *Array {
	return &Array{view: view{n: n, def: def, nonNeg: def >= 0}}
}

// Len returns the logical length.
func (v *view) Len() uint64 { return v.n }

// Default returns the current default value.
func (v *view) Default() int64 { return v.def }

// NonNegative reports whether every effective value is known to be >= 0.
// Only then is Locate available.
func (v *view) NonNegative() bool { return v.nonNeg }

func (v *view) find(p uint64) (entry, bool) {
	i := sort.Search(len(v.exc), func(i int) bool { return v.exc[i].pos >= p })
	if i < len(v.exc) && v.exc[i].pos == p {
		return v.exc[i], true
	}
	return entry{}, false
}

// countExceptions returns the number of exceptions with pos in [lo, hi].
func (v *view) countExceptions(lo, hi uint64) int {
	a := sort.Search(len(v.exc), func(i int) bool { return v.exc[i].pos >= lo })
	b := sort.Search(len(v.exc), func(i int) bool { return v.exc[i].pos > hi })
	return b - a
}

// layerSumAfter sums deltas of layers with seq > wm that cover p.
func (v *view) layerSumAfter(p uint64, wm uint64) int64 {
	var s int64
	for _, l := range v.layers {
		if l.seq > wm && l.lo <= p && p <= l.hi {
			s += l.delta
		}
	}
	return s
}

// Get returns the effective value at position p.
func (v *view) Get(p uint64) int64 {
	if p >= v.n {
		panic(fmt.Sprintf("sparse: Get(%d) out of range [0,%d)", p, v.n))
	}
	if e, ok := v.find(p); ok {
		return e.val + v.layerSumAfter(p, e.wm)
	}
	return v.def + v.layerSumAfter(p, 0)
}

// IsException reports whether p holds an explicitly stored value. An
// explicit 0 is an exception; a missing position is not.
func (v *view) IsException(p uint64) bool {
	_, ok := v.find(p)
	return ok
}

// RangeSum returns the sum of effective values over [lo, hi] inclusive,
// including the default contribution of non-exception positions.
// Cost: O(E_in_range + L*log E), independent of hi-lo.
func (v *view) RangeSum(lo, hi uint64) int64 {
	if lo > hi || hi >= v.n {
		panic(fmt.Sprintf("sparse: RangeSum(%d,%d) out of range [0,%d)", lo, hi, v.n))
	}
	total := hi - lo + 1
	var sum int64
	i := sort.Search(len(v.exc), func(i int) bool { return v.exc[i].pos >= lo })
	cnt := 0
	for ; i < len(v.exc) && v.exc[i].pos <= hi; i++ {
		e := v.exc[i]
		sum += e.val + v.layerSumAfter(e.pos, e.wm)
		cnt++
	}
	// Default contribution of non-exception positions.
	sum += int64(total-uint64(cnt)) * v.def
	// Layer contribution of non-exception positions only; exception
	// positions already received their layers above.
	for _, l := range v.layers {
		lo2, hi2 := lo, hi
		if l.lo > lo2 {
			lo2 = l.lo
		}
		if l.hi < hi2 {
			hi2 = l.hi
		}
		if lo2 > hi2 {
			continue
		}
		overlap := hi2 - lo2 + 1
		sum += l.delta * int64(overlap-uint64(v.countExceptions(lo2, hi2)))
	}
	return sum
}

// Total returns the sum of the whole array.
func (v *view) Total() int64 {
	if v.n == 0 {
		return 0
	}
	return v.RangeSum(0, v.n-1)
}

// Locate returns the smallest position p such that the prefix sum over
// [0, p] is >= k. It is only defined for non-negative snapshots (monotone
// prefix sums) and 1 <= k <= Total(); ok is false otherwise.
// Cost: O(log n) range-sum queries.
func (v *view) Locate(k int64) (pos uint64, ok bool) {
	if !v.nonNeg || k <= 0 || v.n == 0 {
		return 0, false
	}
	if k > v.Total() {
		return 0, false
	}
	lo, hi := uint64(0), v.n-1
	for lo < hi {
		mid := lo + (hi-lo)/2
		if v.RangeSum(0, mid) >= k {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, true
}

// Snapshot returns an O(1) immutable view of the current state. Later
// mutations of the Array never affect the Snapshot (copy-on-write).
func (a *Array) Snapshot() *Snapshot {
	a.excShared = true
	a.layersShared = true
	return &Snapshot{view: view{
		n: a.n, def: a.def, exc: a.exc, layers: a.layers, nonNeg: a.nonNeg,
	}}
}

func (a *Array) ensureExcWritable() {
	if a.excShared {
		a.exc = append([]entry(nil), a.exc...)
		a.excShared = false
	}
}

func (a *Array) ensureLayersWritable() {
	if a.layersShared {
		a.layers = append([]layer(nil), a.layers...)
		a.layersShared = false
	}
}

// Set stores val as an explicit exception at p, overriding any lazy layers
// that currently cover p (future layers still apply). Cost: O(E) due to the
// sorted-insert memmove; O(log E) on overwrite.
func (a *Array) Set(p uint64, val int64) {
	if p >= a.n {
		panic(fmt.Sprintf("sparse: Set(%d) out of range [0,%d)", p, a.n))
	}
	a.ensureExcWritable()
	i := sort.Search(len(a.exc), func(i int) bool { return a.exc[i].pos >= p })
	if i < len(a.exc) && a.exc[i].pos == p {
		a.exc[i].val = val
		a.exc[i].wm = a.seq
	} else {
		a.exc = append(a.exc, entry{})
		copy(a.exc[i+1:], a.exc[i:])
		a.exc[i] = entry{pos: p, val: val, wm: a.seq}
	}
	if val < 0 {
		a.nonNeg = false
	}
}

// RangeAdd lazily adds delta to every position in [lo, hi] inclusive.
// Cost: O(1); the huge default region is never iterated.
func (a *Array) RangeAdd(lo, hi uint64, delta int64) {
	if lo > hi || hi >= a.n {
		panic(fmt.Sprintf("sparse: RangeAdd(%d,%d) out of range [0,%d)", lo, hi, a.n))
	}
	if delta == 0 {
		return
	}
	a.ensureLayersWritable()
	a.seq++
	a.layers = append(a.layers, layer{lo: lo, hi: hi, delta: delta, seq: a.seq})
	if delta < 0 {
		a.nonNeg = false
	}
}

// foldLayers absorbs every pending layer into the stored exceptions and
// clears the layer list. Effective values of exceptions are unchanged.
// Cost: O(E*L); default-only regions are never touched.
func (a *Array) foldLayers() {
	if len(a.layers) == 0 {
		return
	}
	a.ensureExcWritable()
	for i := range a.exc {
		a.exc[i].val += a.layerSumAfter(a.exc[i].pos, a.exc[i].wm)
		a.exc[i].wm = a.seq
	}
	if a.layersShared {
		// The backing array is shared with a snapshot; dropping to
		// a.layers[:0] would let a later append overwrite the
		// snapshot's layers. Detach instead.
		a.layers = nil
	} else {
		a.layers = a.layers[:0]
	}
	a.layersShared = false
}

// SetDefault changes the default value to newDef.
//
// Positions without an explicit exception follow the new default (e.g.
// changing 0 -> 2 makes every unassigned position read 2). Explicit
// exceptions keep their value and position identity, with two rules:
//
//   - followOld: exceptions whose current value equals the OLD default are
//     treated as default positions and follow to newDef (reclaimed).
//   - reclaim: exceptions whose value equals the NEW default are redundant
//     and always reclaimed, shrinking physical storage.
//
// SetDefault first folds all pending lazy layers into the exceptions
// (O(E*L)), so an implicit position becomes exactly newDef and an explicit
// position keeps its full effective value.
func (a *Array) SetDefault(newDef int64, followOld bool) {
	a.foldLayers()
	oldDef := a.def
	a.def = newDef
	if newDef < 0 {
		a.nonNeg = false
	}
	a.ensureExcWritable()
	out := a.exc[:0]
	for _, e := range a.exc {
		if followOld && e.val == oldDef {
			continue // treated as a default position: follows newDef
		}
		if e.val == newDef {
			continue // redundant with the new default: reclaim
		}
		out = append(out, e)
	}
	a.exc = out
}

// RevalidateNonNegative recomputes the non-negativity flag after negative
// values may have been removed. The flag is conservative: it only flips
// back to true when the default, every layer delta and every stored
// exception value are >= 0.
func (a *Array) RevalidateNonNegative() {
	if a.nonNeg {
		return
	}
	nn := a.def >= 0
	for _, l := range a.layers {
		if l.delta < 0 {
			nn = false
		}
	}
	for _, e := range a.exc {
		if e.val < 0 {
			nn = false
		}
	}
	a.nonNeg = nn
}

// Stats reports logical vs physical occupancy.
type Stats struct {
	LogicalLen    uint64 // logical positions
	Default       int64  // current default value
	Exceptions    int    // stored exceptions
	Layers        int    // pending lazy layers
	PhysicalBytes int    // len(Encode()): the real on-disk encoding
	InMemoryBytes int    // estimated live Go structure size
	DenseBytes    uint64 // what a dense []int64 of the same length would cost
}

// Stats returns the current occupancy numbers.
func (v *view) Stats() Stats {
	return Stats{
		LogicalLen:    v.n,
		Default:       v.def,
		Exceptions:    len(v.exc),
		Layers:        len(v.layers),
		PhysicalBytes: len(v.Encode()),
		InMemoryBytes: 24*len(v.exc) + 32*len(v.layers) + 96,
		DenseBytes:    v.n * 8,
	}
}
