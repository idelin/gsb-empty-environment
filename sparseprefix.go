// Package sparseprefix implements a sparse prefix-sum structure over a very
// long conceptual array (indices are non-negative int64) in which only a few
// intervals hold non-zero values.
//
// The array is stored as a sorted list of disjoint segments [lo, hi] -> val.
// Invariants maintained after every mutation:
//   - segments are sorted by lo and do not overlap;
//   - every stored segment has val != 0 (empty/zero segments are dropped);
//   - adjacent segments never share the same value (equal neighbours merge).
//
// All cumulative arithmetic is done with math/big so totals and prefix sums
// cannot overflow regardless of segment values or lengths.
package sparseprefix

import (
	"errors"
	"math/big"
	"sort"
)

// ErrInvalidRange is returned by Assign when lo > hi or lo is negative.
var ErrInvalidRange = errors.New("sparseprefix: invalid range")

// Segment is one maximal constant run [Lo, Hi] (inclusive) with value Val.
type Segment struct {
	Lo, Hi int64
	Val    uint64
}

type segment struct {
	lo, hi int64
	val    uint64 // always non-zero
}

// SparsePrefixSum is a sparse array supporting range assignment and
// cumulative-amount lookups. The zero value is ready to use.
type SparsePrefixSum struct {
	segs []segment
}

// segLen returns hi-lo+1 without overflowing (0 <= lo <= hi <= MaxInt64).
func segLen(lo, hi int64) uint64 {
	return uint64(hi) - uint64(lo) + 1
}

// Assign sets every index in [lo, hi] (inclusive) to val. Assigning 0 clears
// the range. Overlapping segments are split or removed; adjacent segments
// with equal values are merged afterwards.
func (s *SparsePrefixSum) Assign(lo, hi int64, val uint64) error {
	if lo < 0 || hi < lo {
		return ErrInvalidRange
	}
	// First segment that may intersect [lo, hi].
	start := sort.Search(len(s.segs), func(i int) bool { return s.segs[i].hi >= lo })

	out := make([]segment, 0, len(s.segs)+2)
	out = append(out, s.segs[:start]...)

	var tail []segment // right remainders of split segments, belong after [lo, hi]
	i := start
	for i < len(s.segs) && s.segs[i].lo <= hi {
		seg := s.segs[i]
		if seg.lo < lo { // lo >= 1 here, so lo-1 cannot underflow
			out = append(out, segment{seg.lo, lo - 1, seg.val})
		}
		if seg.hi > hi { // hi < MaxInt64 here, so hi+1 cannot overflow
			tail = append(tail, segment{hi + 1, seg.hi, seg.val})
		}
		i++
	}
	if val != 0 {
		out = append(out, segment{lo, hi, val})
	}
	out = append(out, tail...)
	out = append(out, s.segs[i:]...)

	// Merge adjacent equal-valued segments. out is sorted and disjoint.
	merged := out[:0]
	for _, seg := range out {
		if n := len(merged); n > 0 && merged[n-1].val == seg.val && merged[n-1].hi+1 == seg.lo {
			merged[n-1].hi = seg.hi
		} else {
			merged = append(merged, seg)
		}
	}
	s.segs = merged
	return nil
}

// ValueAt returns the value stored at index i (0 when i is negative or lies
// in a zero gap).
func (s *SparsePrefixSum) ValueAt(i int64) uint64 {
	idx := sort.Search(len(s.segs), func(k int) bool { return s.segs[k].hi >= i })
	if idx < len(s.segs) && s.segs[idx].lo <= i {
		return s.segs[idx].val
	}
	return 0
}

// PrefixSum returns the sum of values over indices [0, i]. It is 0 for i < 0.
// The result is a fresh *big.Int and never overflows.
func (s *SparsePrefixSum) PrefixSum(i int64) *big.Int {
	total := new(big.Int)
	if i < 0 {
		return total
	}
	tmp := new(big.Int)
	for _, seg := range s.segs {
		if seg.lo > i {
			break
		}
		hi := seg.hi
		if hi > i {
			hi = i
		}
		tmp.SetUint64(seg.val)
		tmp.Mul(tmp, new(big.Int).SetUint64(segLen(seg.lo, hi)))
		total.Add(total, tmp)
	}
	return total
}

// Total returns the sum of all stored values as a fresh *big.Int.
func (s *SparsePrefixSum) Total() *big.Int {
	total := new(big.Int)
	tmp := new(big.Int)
	for _, seg := range s.segs {
		tmp.SetUint64(seg.val)
		tmp.Mul(tmp, new(big.Int).SetUint64(segLen(seg.lo, seg.hi)))
		total.Add(total, tmp)
	}
	return total
}

// FindIndex returns the smallest index i >= 0 whose prefix sum over [0, i]
// is >= cum, and true. If cum exceeds the total of the whole array it
// returns (0, false). cum == 0 trivially matches index 0.
func (s *SparsePrefixSum) FindIndex(cum uint64) (int64, bool) {
	return s.FindIndexBig(new(big.Int).SetUint64(cum))
}

// FindIndexBig is FindIndex for cumulative amounts that do not fit in a
// uint64. A negative cum never matches.
func (s *SparsePrefixSum) FindIndexBig(cum *big.Int) (int64, bool) {
	if cum.Sign() < 0 {
		return 0, false
	}
	if cum.Sign() == 0 {
		return 0, true
	}
	sum := new(big.Int)  // invariant: sum < cum
	tmp := new(big.Int)  // scratch for val * segLen
	rem := new(big.Int)  // cum - sum
	need := new(big.Int) // ceil(rem / val)
	one := big.NewInt(1)
	for _, seg := range s.segs {
		n := segLen(seg.lo, seg.hi)
		tmp.SetUint64(seg.val)
		tmp.Mul(tmp, new(big.Int).SetUint64(n)) // segment sum
		rem.Sub(cum, sum)                       // >= 1
		if tmp.Cmp(rem) >= 0 {
			// Answer lies inside this segment: smallest k >= 1 with
			// k*val >= rem is ceil(rem/val) = (rem-1)/val + 1.
			need.Sub(rem, one)
			need.Div(need, tmp.SetUint64(seg.val))
			need.Add(need, one) // 1 <= need <= n <= MaxInt64
			return seg.lo + need.Int64() - 1, true
		}
		sum.Add(sum, tmp) // stays < cum, no overflow possible
	}
	return 0, false
}

// NumSegments returns the number of stored (non-zero) segments.
func (s *SparsePrefixSum) NumSegments() int {
	return len(s.segs)
}

// Segments returns a copy of the stored segments in order. It is intended
// for inspection and testing.
func (s *SparsePrefixSum) Segments() []Segment {
	out := make([]Segment, len(s.segs))
	for i, seg := range s.segs {
		out[i] = Segment{Lo: seg.lo, Hi: seg.hi, Val: seg.val}
	}
	return out
}
