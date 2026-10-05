// Package intervalround provides exact rational multi-interval arithmetic
// (intersection, union, division) together with conservative decimal
// enclosure: exact rational intervals are rounded outward to a fixed number
// of decimal fraction digits, lower endpoints down and upper endpoints up,
// so the rounded result always contains the exact one.
package intervalround

import (
	"math/big"
	"sort"
)

// Bound is one endpoint of an interval.
//
// Inf is -1, 0 or +1 for -inf, finite and +inf respectively. A finite bound
// carries an exact rational value Rat. Open reports whether the endpoint
// itself is excluded from the set. Infinite bounds are always open.
type Bound struct {
	Inf  int
	Rat  *big.Rat
	Open bool
}

// Fin returns a finite bound with the given exact rational value.
func Fin(r *big.Rat, open bool) Bound {
	return Bound{Rat: new(big.Rat).Set(r), Open: open}
}

// NegInf returns the -inf bound (always open).
func NegInf() Bound { return Bound{Inf: -1, Open: true} }

// PosInf returns the +inf bound (always open).
func PosInf() Bound { return Bound{Inf: 1, Open: true} }

// compare orders bounds by value: -inf < finite < +inf.
func compare(a, b Bound) int {
	if a.Inf != b.Inf {
		if a.Inf < b.Inf {
			return -1
		}
		return 1
	}
	if a.Inf != 0 {
		return 0
	}
	return a.Rat.Cmp(b.Rat)
}

// Interval is a single connected segment [Lo, Hi]. A segment with Lo == Hi
// and both endpoints closed is a single point, not an empty set.
type Interval struct {
	Lo, Hi Bound
}

// empty reports whether the segment contains no point.
func (iv Interval) empty() bool {
	c := compare(iv.Lo, iv.Hi)
	return c > 0 || (c == 0 && (iv.Lo.Open || iv.Hi.Open))
}

// contains reports whether the exact rational point x lies in the segment.
func (iv Interval) contains(x *big.Rat) bool {
	if iv.Lo.Inf == 0 {
		c := x.Cmp(iv.Lo.Rat)
		if c < 0 || (c == 0 && iv.Lo.Open) {
			return false
		}
	}
	if iv.Hi.Inf == 0 {
		c := x.Cmp(iv.Hi.Rat)
		if c > 0 || (c == 0 && iv.Hi.Open) {
			return false
		}
	}
	return true
}

// Multi is a finite union of disjoint, sorted, non-adjacent segments.
// The zero value is the empty set.
type Multi struct {
	Segs []Interval
}

// Empty returns the empty set.
func Empty() Multi { return Multi{} }

// All returns the whole real line (-inf, +inf).
func All() Multi {
	return Multi{Segs: []Interval{{Lo: NegInf(), Hi: PosInf()}}}
}

// Of builds a single-segment Multi from exact rational endpoints. An
// inconsistent or open-at-both-ends single point yields the empty set.
func Of(lo, hi *big.Rat, loOpen, hiOpen bool) Multi {
	return Multi{Segs: normalize([]Interval{{Lo: Fin(lo, loOpen), Hi: Fin(hi, hiOpen)}})}
}

// Closed builds the closed segment [lo, hi].
func Closed(lo, hi *big.Rat) Multi { return Of(lo, hi, false, false) }

// Point builds the single-point set {x}; it is never empty.
func Point(x *big.Rat) Multi { return Closed(x, x) }

// IsEmpty reports whether the set is empty.
func (m Multi) IsEmpty() bool { return len(m.Segs) == 0 }

// IsPoint reports whether the set is exactly one point.
func (m Multi) IsPoint() bool {
	if len(m.Segs) != 1 {
		return false
	}
	s := m.Segs[0]
	return s.Lo.Inf == 0 && s.Hi.Inf == 0 && !s.Lo.Open && !s.Hi.Open &&
		s.Lo.Rat.Cmp(s.Hi.Rat) == 0
}

// Contains reports whether the exact rational point x lies in the set.
func (m Multi) Contains(x *big.Rat) bool {
	for _, s := range m.Segs {
		if s.contains(x) {
			return true
		}
	}
	return false
}

// normalize drops empty segments, sorts, and merges overlapping or
// touching segments. Two segments touching at a point merge only when at
// least one of them includes that point.
func normalize(in []Interval) []Interval {
	out := make([]Interval, 0, len(in))
	for _, s := range in {
		if !s.empty() {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		c := compare(out[i].Lo, out[j].Lo)
		if c != 0 {
			return c < 0
		}
		return !out[i].Lo.Open && out[j].Lo.Open
	})
	merged := out[:0]
	for _, s := range out {
		if len(merged) == 0 {
			merged = append(merged, s)
			continue
		}
		last := &merged[len(merged)-1]
		c := compare(s.Lo, last.Hi)
		if c > 0 || (c == 0 && s.Lo.Open && last.Hi.Open) {
			merged = append(merged, s)
			continue
		}
		switch compare(s.Hi, last.Hi) {
		case 1:
			last.Hi = s.Hi
		case 0:
			last.Hi.Open = last.Hi.Open && s.Hi.Open
		}
	}
	return merged
}
