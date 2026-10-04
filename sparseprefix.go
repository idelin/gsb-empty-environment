// Package sparseprefix implements a sparse prefix-sum structure over a very
// long array in which only a few intervals hold non-zero values.
//
// The array is stored as a sorted list of disjoint half-open segments with
// constant values. Invariants:
//   - segments are sorted by start and do not overlap;
//   - zero-valued segments are never stored;
//   - adjacent segments never hold equal values (they are merged eagerly).
//
// All accumulation uses saturating uint64 arithmetic, so totals and prefix
// sums never wrap around: once a sum would exceed math.MaxUint64 it stays
// pinned at math.MaxUint64, which keeps Find queries exact for every
// representable amount.
package sparseprefix

import (
	"fmt"
	"math"
	"sort"
)

// segment is a half-open interval [lo, hi) holding a constant non-zero value.
type segment struct {
	lo, hi int64
	val    uint64
}

// Sum is a sparse array of non-negative values of length n.
type Sum struct {
	n    int64
	segs []segment
}

// New returns a zeroed sparse array of length n. It panics if n < 0.
func New(n int64) *Sum {
	if n < 0 {
		panic(fmt.Sprintf("sparseprefix: negative length %d", n))
	}
	return &Sum{n: n}
}

// Len returns the length of the underlying array.
func (s *Sum) Len() int64 { return s.n }

// Assign sets every position in the half-open range [l, r) to v.
// Assigning v == 0 clears the range. It panics on an out-of-bounds range.
func (s *Sum) Assign(l, r int64, v uint64) {
	if l < 0 || r > s.n || l > r {
		panic(fmt.Sprintf("sparseprefix: invalid range [%d,%d) for length %d", l, r, s.n))
	}
	if l == r {
		return
	}
	// First segment that may intersect [l, r): the first with hi > l.
	i := sort.Search(len(s.segs), func(k int) bool { return s.segs[k].hi > l })

	out := make([]segment, 0, len(s.segs)+2)
	out = append(out, s.segs[:i]...)
	// Left remainder of the segment straddling l.
	if i < len(s.segs) && s.segs[i].lo < l {
		out = append(out, segment{s.segs[i].lo, l, s.segs[i].val})
	}
	// The newly assigned interval (dropped when v == 0).
	out = appendMerged(out, segment{l, r, v})
	// Drop segments fully inside [l, r); keep the right remainder of the
	// segment straddling r.
	j := i
	for j < len(s.segs) && s.segs[j].lo < r {
		if s.segs[j].hi > r {
			out = appendMerged(out, segment{r, s.segs[j].hi, s.segs[j].val})
		}
		j++
	}
	for ; j < len(s.segs); j++ {
		out = appendMerged(out, s.segs[j])
	}
	s.segs = out
}

// Total returns the sum of all values, saturating at math.MaxUint64.
func (s *Sum) Total() uint64 {
	var t uint64
	for _, seg := range s.segs {
		t = satAdd(t, satMul(seg.val, uint64(seg.hi-seg.lo)))
	}
	return t
}

// Find returns the smallest index i such that the inclusive prefix sum
// a[0]+...+a[i] is >= amount. ok is false when amount exceeds the total
// (or the array is empty). amount == 0 matches index 0 of a non-empty array.
func (s *Sum) Find(amount uint64) (idx int64, ok bool) {
	if amount == 0 {
		return 0, s.n > 0
	}
	var cum uint64
	for _, seg := range s.segs {
		next := satAdd(cum, satMul(seg.val, uint64(seg.hi-seg.lo)))
		if next >= amount {
			// cum is exact here: had it saturated, an earlier segment
			// would already have matched. So 1 <= rem <= seg total.
			rem := amount - cum
			return seg.lo + int64((rem-1)/seg.val), true
		}
		cum = next
	}
	return 0, false
}

// appendMerged appends seg, merging it into the previous segment when they
// are adjacent and equal-valued. Zero-value or empty segments are dropped.
func appendMerged(segs []segment, seg segment) []segment {
	if seg.val == 0 || seg.lo >= seg.hi {
		return segs
	}
	if m := len(segs); m > 0 && segs[m-1].val == seg.val && segs[m-1].hi == seg.lo {
		segs[m-1].hi = seg.hi
		return segs
	}
	return append(segs, seg)
}

func satAdd(a, b uint64) uint64 {
	if s := a + b; s >= a {
		return s
	}
	return math.MaxUint64
}

func satMul(a, b uint64) uint64 {
	if a != 0 && b > math.MaxUint64/a {
		return math.MaxUint64
	}
	return a * b
}
