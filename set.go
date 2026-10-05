package interval

import (
	"math/big"
	"sort"
	"strings"
)

// Set is a finite union of disjoint, sorted, non-adjacent intervals.
// MaxSegments > 0 caps the segment count; when the cap would be
// exceeded, adjacent segments are merged into their hull, which is
// conservative (the result is a superset, never a subset).
type Set struct {
	Segments    []Interval
	MaxSegments int
}

// NewSet returns an empty Set. maxSegments <= 0 means unlimited.
func NewSet(maxSegments int) *Set { return &Set{MaxSegments: maxSegments} }

// IsEmpty reports whether s has no segments.
func (s *Set) IsEmpty() bool { return len(s.Segments) == 0 }

// Contains reports whether the rational point v lies in any segment.
func (s *Set) Contains(v *big.Rat) bool {
	for _, iv := range s.Segments {
		if iv.Contains(v) {
			return true
		}
	}
	return false
}

func lessIv(a, b Interval) bool {
	if c := cmpExt(a.lo, b.lo); c != 0 {
		return c < 0
	}
	if a.loOpen != b.loOpen {
		return !a.loOpen // closed endpoint sorts first
	}
	return cmpExt(a.hi, b.hi) < 0
}

// mergeable reports whether a and b (a before b) overlap or touch at a
// point included in at least one of them.
func mergeable(a, b Interval) bool {
	c := cmpExt(a.hi, b.lo)
	return c > 0 || (c == 0 && (!a.hiOpen || !b.loOpen))
}

// hull returns the smallest interval containing a and b (a before b).
func hull(a, b Interval) Interval {
	hi, hiOpen := a.hi, a.hiOpen
	if c := cmpExt(b.hi, a.hi); c > 0 {
		hi, hiOpen = b.hi, b.hiOpen
	} else if c == 0 {
		hiOpen = hiOpen && b.hiOpen
	}
	return Make(a.lo, hi, a.loOpen, hiOpen)
}

// Add inserts iv into s, merging overlapping or touching segments and
// enforcing MaxSegments conservatively.
func (s *Set) Add(iv Interval) {
	if iv.empty {
		return
	}
	s.Segments = append(s.Segments, iv)
	sort.Slice(s.Segments, func(a, b int) bool { return lessIv(s.Segments[a], s.Segments[b]) })
	merged := s.Segments[:0]
	for _, cur := range s.Segments {
		if n := len(merged); n > 0 && mergeable(merged[n-1], cur) {
			merged[n-1] = hull(merged[n-1], cur)
		} else {
			merged = append(merged, cur)
		}
	}
	s.Segments = merged
	s.enforceLimit()
}

// enforceLimit merges the closest adjacent segments into their hull
// until the segment cap is satisfied. Merging only ever grows the set.
func (s *Set) enforceLimit() {
	if s.MaxSegments <= 0 {
		return
	}
	for len(s.Segments) > s.MaxSegments {
		best, bestGap := 0, (*big.Rat)(nil)
		for k := 0; k+1 < len(s.Segments); k++ {
			gap := new(big.Rat).Sub(s.Segments[k+1].lo.val, s.Segments[k].hi.val)
			if bestGap == nil || gap.Cmp(bestGap) < 0 {
				best, bestGap = k, gap
			}
		}
		s.Segments[best] = hull(s.Segments[best], s.Segments[best+1])
		s.Segments = append(s.Segments[:best+1], s.Segments[best+2:]...)
	}
}

// Union returns s ∪ t.
func (s *Set) Union(t *Set) *Set {
	max := s.MaxSegments
	if max == 0 {
		max = t.MaxSegments
	}
	out := NewSet(max)
	for _, iv := range s.Segments {
		out.Add(iv)
	}
	for _, iv := range t.Segments {
		out.Add(iv)
	}
	return out
}

// Intersect returns s ∩ t.
func (s *Set) Intersect(t *Set) *Set {
	max := s.MaxSegments
	if max == 0 {
		max = t.MaxSegments
	}
	out := NewSet(max)
	for _, a := range s.Segments {
		for _, b := range t.Segments {
			out.Add(a.Intersect(b))
		}
	}
	return out
}

func (s *Set) String() string {
	if len(s.Segments) == 0 {
		return "empty"
	}
	parts := make([]string, len(s.Segments))
	for k, iv := range s.Segments {
		parts[k] = iv.String()
	}
	return strings.Join(parts, " ∪ ")
}
