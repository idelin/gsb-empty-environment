// Package vindex implements a versioned index of non-overlapping half-open
// intervals [lo, hi) with associated values.
//
// Every mutating operation (Set/Delete) over a non-empty range appends a new
// immutable version; old versions remain queryable forever. Updates split
// existing segments at the range boundaries, and adjacent segments carrying
// equal values are merged back together. Operations over an empty range
// (lo >= hi) are no-ops and do not create a version.
//
// Endpoints are arbitrary rationals (*big.Rat); they are never mutated by
// the store.
package vindex

import (
	"math/big"
	"sort"
)

// Segment is one non-overlapping half-open interval [Lo, Hi) with a value.
type Segment[V comparable] struct {
	Lo  *big.Rat
	Hi  *big.Rat
	Val V
}

// Covers reports whether p lies in [s.Lo, s.Hi).
func (s Segment[V]) Covers(p *big.Rat) bool {
	return s.Lo.Cmp(p) <= 0 && p.Cmp(s.Hi) < 0
}

type segment[V comparable] struct {
	lo, hi *big.Rat
	val    V
}

// Store keeps every version of the interval set. Version 0 is the empty set;
// each successful update appends exactly one version.
type Store[V comparable] struct {
	versions [][]segment[V]
}

// New returns an empty store whose only version is 0 (the empty set).
func New[V comparable]() *Store[V] {
	return &Store[V]{versions: [][]segment[V]{{}}}
}

// Version returns the current (latest) version number.
func (s *Store[V]) Version() int { return len(s.versions) - 1 }

// NumVersions returns the total number of versions, including version 0.
func (s *Store[V]) NumVersions() int { return len(s.versions) }

// Set assigns val to the half-open range [lo, hi) and returns the new
// version. Existing segments are split at lo and hi; overlapping parts are
// replaced; adjacent segments with equal values are merged. If lo >= hi the
// call is a no-op and the current version is returned unchanged.
func (s *Store[V]) Set(lo, hi *big.Rat, val V) int {
	if lo.Cmp(hi) >= 0 {
		return s.Version()
	}
	cur := s.versions[len(s.versions)-1]
	next := make([]segment[V], 0, len(cur)+2)

	// Segments entirely before lo.
	i := 0
	for i < len(cur) && cur[i].hi.Cmp(lo) <= 0 {
		next = append(next, cur[i])
		i++
	}
	// Left split: keep the part of cur[i] below lo.
	if i < len(cur) && cur[i].lo.Cmp(lo) < 0 {
		next = append(next, segment[V]{lo: cur[i].lo, hi: lo, val: cur[i].val})
	}
	// Skip everything covered by [lo, hi).
	for i < len(cur) && cur[i].hi.Cmp(hi) <= 0 {
		i++
	}
	// The new segment, merged with an equal-valued left neighbor.
	if n := len(next); n > 0 && next[n-1].val == val && next[n-1].hi.Cmp(lo) == 0 {
		next[n-1].hi = hi
	} else {
		next = append(next, segment[V]{lo: lo, hi: hi, val: val})
	}
	// Right split: keep the part of cur[i] at or above hi, merging with an
	// equal-valued new segment when they touch.
	if i < len(cur) && cur[i].lo.Cmp(hi) < 0 {
		if cur[i].val == val {
			next[len(next)-1].hi = cur[i].hi
		} else {
			next = append(next, segment[V]{lo: hi, hi: cur[i].hi, val: cur[i].val})
		}
		i++
	}
	// Segments entirely after hi.
	for ; i < len(cur); i++ {
		next = append(next, cur[i])
	}

	s.versions = append(s.versions, merge(next))
	return s.Version()
}

// Delete removes the half-open range [lo, hi) and returns the new version.
// Existing segments are split at lo and hi; adjacent segments that become
// contiguous and share a value are merged. If lo >= hi the call is a no-op
// and the current version is returned unchanged.
func (s *Store[V]) Delete(lo, hi *big.Rat) int {
	if lo.Cmp(hi) >= 0 {
		return s.Version()
	}
	cur := s.versions[len(s.versions)-1]
	next := make([]segment[V], 0, len(cur))

	i := 0
	for i < len(cur) && cur[i].hi.Cmp(lo) <= 0 {
		next = append(next, cur[i])
		i++
	}
	if i < len(cur) && cur[i].lo.Cmp(lo) < 0 {
		next = append(next, segment[V]{lo: cur[i].lo, hi: lo, val: cur[i].val})
	}
	for i < len(cur) && cur[i].hi.Cmp(hi) <= 0 {
		i++
	}
	if i < len(cur) && cur[i].lo.Cmp(hi) < 0 {
		next = append(next, segment[V]{lo: hi, hi: cur[i].hi, val: cur[i].val})
		i++
	}
	for ; i < len(cur); i++ {
		next = append(next, cur[i])
	}

	s.versions = append(s.versions, merge(next))
	return s.Version()
}

// merge coalesces adjacent segments that touch and carry equal values.
func merge[V comparable](segs []segment[V]) []segment[V] {
	out := segs[:0]
	for _, sg := range segs {
		if n := len(out); n > 0 && out[n-1].val == sg.val && out[n-1].hi.Cmp(sg.lo) == 0 {
			out[n-1].hi = sg.hi
			continue
		}
		out = append(out, sg)
	}
	return out
}

// Get returns the value covering point p at the given version.
func (s *Store[V]) Get(version int, p *big.Rat) (V, bool) {
	segs := s.versions[version]
	i := sort.Search(len(segs), func(i int) bool { return segs[i].hi.Cmp(p) > 0 })
	if i < len(segs) && segs[i].lo.Cmp(p) <= 0 {
		return segs[i].val, true
	}
	var zero V
	return zero, false
}

// Segments returns a copy of the segment list at the given version, in
// ascending order. The returned Segments share endpoint pointers with the
// store; treat them as read-only.
func (s *Store[V]) Segments(version int) []Segment[V] {
	segs := s.versions[version]
	out := make([]Segment[V], len(segs))
	for i, sg := range segs {
		out[i] = Segment[V]{Lo: sg.lo, Hi: sg.hi, Val: sg.val}
	}
	return out
}
