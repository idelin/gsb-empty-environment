// Package interval implements sets of real intervals with rational or
// infinite endpoints. Endpoints may be open or closed. The library
// supports union, intersection and difference, and normalizes every
// result into a sorted list of pairwise disjoint intervals.
//
// Normalization rules:
//   - empty intervals (including open single points such as (a, a)) are
//     discarded;
//   - a closed single point [a, a] is a valid, non-empty interval;
//   - two adjacent intervals are merged only when their union has no
//     gap, i.e. they overlap, or they share an endpoint value that at
//     least one of them includes.
package interval

import (
	"fmt"
	"math/big"
	"sort"
)

// Bound is one endpoint of an interval. Inf is 0 for a finite endpoint
// (Val holds the rational value), -1 for -infinity and +1 for +infinity.
// Open reports whether the endpoint itself is excluded.
type Bound struct {
	Inf  int
	Val  *big.Rat
	Open bool
}

// ClosedAt returns a closed finite endpoint at v.
func ClosedAt(v *big.Rat) Bound { return Bound{Val: new(big.Rat).Set(v)} }

// OpenAt returns an open finite endpoint at v.
func OpenAt(v *big.Rat) Bound { return Bound{Val: new(big.Rat).Set(v), Open: true} }

// NegInf returns the -infinity endpoint.
func NegInf() Bound { return Bound{Inf: -1} }

// PosInf returns the +infinity endpoint.
func PosInf() Bound { return Bound{Inf: 1} }

// cmpPosition orders bounds on the extended real line, ignoring openness:
// -inf < every finite value < +inf.
func cmpPosition(a, b Bound) int {
	if a.Inf != b.Inf {
		if a.Inf < b.Inf {
			return -1
		}
		return 1
	}
	if a.Inf != 0 {
		return 0
	}
	return a.Val.Cmp(b.Val)
}

// maxLower returns the more restrictive (later) of two lower bounds.
func maxLower(a, b Bound) Bound {
	switch c := cmpPosition(a, b); {
	case c > 0:
		return a
	case c < 0:
		return b
	}
	if a.Open {
		return a
	}
	return b
}

// minUpper returns the more restrictive (earlier) of two upper bounds.
func minUpper(a, b Bound) Bound {
	switch c := cmpPosition(a, b); {
	case c < 0:
		return a
	case c > 0:
		return b
	}
	if a.Open {
		return a
	}
	return b
}

// maxUpper returns the less restrictive (later) of two upper bounds.
func maxUpper(a, b Bound) Bound {
	switch c := cmpPosition(a, b); {
	case c > 0:
		return a
	case c < 0:
		return b
	}
	if a.Open {
		return b
	}
	return a
}

// Interval is a single connected interval [Lo, Hi] with open/closed ends.
type Interval struct {
	Lo, Hi Bound
}

// New builds the interval (lo, hi). The result may be empty; use IsEmpty.
func New(lo, hi Bound) Interval { return Interval{Lo: lo, Hi: hi} }

// IsEmpty reports whether the interval contains no point. Open or
// reversed single points such as (a, a) and [b, a] with a < b are empty;
// the closed single point [a, a] is not.
func (i Interval) IsEmpty() bool {
	c := cmpPosition(i.Lo, i.Hi)
	if c > 0 {
		return true
	}
	if c < 0 {
		return false
	}
	if i.Lo.Inf != 0 {
		return true // degenerate infinite endpoint, e.g. (+inf, +inf)
	}
	return i.Lo.Open || i.Hi.Open
}

// Contains reports whether the rational x lies in the interval.
func (i Interval) Contains(x *big.Rat) bool {
	switch i.Lo.Inf {
	case 1:
		return false
	case 0:
		c := x.Cmp(i.Lo.Val)
		if c < 0 || (c == 0 && i.Lo.Open) {
			return false
		}
	}
	switch i.Hi.Inf {
	case -1:
		return false
	case 0:
		c := x.Cmp(i.Hi.Val)
		if c > 0 || (c == 0 && i.Hi.Open) {
			return false
		}
	}
	return true
}

// intersect returns the intersection of two intervals (possibly empty).
func intersect(a, b Interval) Interval {
	return New(maxLower(a.Lo, b.Lo), minUpper(a.Hi, b.Hi))
}

// subtract returns a \ b as zero, one or two intervals, in order.
func subtract(a, b Interval) []Interval {
	if intersect(a, b).IsEmpty() {
		return []Interval{a}
	}
	var out []Interval
	left := New(a.Lo, Bound{Inf: b.Lo.Inf, Val: b.Lo.Val, Open: !b.Lo.Open})
	if !left.IsEmpty() {
		out = append(out, left)
	}
	right := New(Bound{Inf: b.Hi.Inf, Val: b.Hi.Val, Open: !b.Hi.Open}, a.Hi)
	if !right.IsEmpty() {
		out = append(out, right)
	}
	return out
}

// String renders the interval in mathematical notation.
func (i Interval) String() string {
	lo, hi := "[", "]"
	if i.Lo.Open || i.Lo.Inf != 0 {
		lo = "("
	}
	if i.Hi.Open || i.Hi.Inf != 0 {
		hi = ")"
	}
	return fmt.Sprintf("%s%s,%s%s", lo, boundString(i.Lo), boundString(i.Hi), hi)
}

func boundString(b Bound) string {
	switch b.Inf {
	case -1:
		return "-inf"
	case 1:
		return "+inf"
	}
	return b.Val.RatString()
}

// Set is a normalized set of real intervals: sorted by lower bound,
// pairwise disjoint, and never containing two intervals whose union
// would be a single interval (no gap between them).
type Set struct {
	ivs []Interval
}

// NewSet builds a normalized set from arbitrary intervals.
func NewSet(ivs ...Interval) Set {
	return Set{ivs: normalize(ivs)}
}

// Intervals returns the normalized, sorted, disjoint intervals.
func (s Set) Intervals() []Interval {
	out := make([]Interval, len(s.ivs))
	copy(out, s.ivs)
	return out
}

// Len returns the number of disjoint intervals in the set.
func (s Set) Len() int { return len(s.ivs) }

// Contains reports whether the rational x belongs to the set.
func (s Set) Contains(x *big.Rat) bool {
	i := sort.Search(len(s.ivs), func(i int) bool {
		return cmpPosition(Bound{Val: x}, s.ivs[i].Hi) <= 0
	})
	return i < len(s.ivs) && s.ivs[i].Contains(x)
}

// Union returns the normalized union of two sets.
func (s Set) Union(t Set) Set {
	all := make([]Interval, 0, len(s.ivs)+len(t.ivs))
	all = append(all, s.ivs...)
	all = append(all, t.ivs...)
	return Set{ivs: normalize(all)}
}

// Intersect returns the normalized intersection of two sets.
func (s Set) Intersect(t Set) Set {
	var out []Interval
	for _, a := range s.ivs {
		for _, b := range t.ivs {
			if iv := intersect(a, b); !iv.IsEmpty() {
				out = append(out, iv)
			}
		}
	}
	return Set{ivs: out}
}

// Subtract returns the normalized difference s \ t.
func (s Set) Subtract(t Set) Set {
	res := s.ivs
	for _, b := range t.ivs {
		var next []Interval
		for _, a := range res {
			next = append(next, subtract(a, b)...)
		}
		res = next
	}
	return Set{ivs: res}
}

// String renders the set as a union of intervals.
func (s Set) String() string {
	if len(s.ivs) == 0 {
		return "{}"
	}
	out := ""
	for i, iv := range s.ivs {
		if i > 0 {
			out += " U "
		}
		out += iv.String()
	}
	return out
}

// normalize drops empty intervals, sorts the rest by lower bound and
// merges any pair whose union has no gap.
func normalize(ivs []Interval) []Interval {
	var live []Interval
	for _, iv := range ivs {
		if !iv.IsEmpty() {
			live = append(live, iv)
		}
	}
	sort.Slice(live, func(i, j int) bool {
		a, b := live[i].Lo, live[j].Lo
		if c := cmpPosition(a, b); c != 0 {
			return c < 0
		}
		return !a.Open && b.Open // closed lower bound starts earlier
	})
	var out []Interval
	for _, iv := range live {
		if len(out) == 0 {
			out = append(out, iv)
			continue
		}
		last := &out[len(out)-1]
		if mergeable(last.Hi, iv.Lo) {
			last.Hi = maxUpper(last.Hi, iv.Hi)
		} else {
			out = append(out, iv)
		}
	}
	return out
}

// mergeable reports whether an interval ending at hi and an interval
// starting at lo (with lo not before hi) have no gap between them.
func mergeable(hi, lo Bound) bool {
	c := cmpPosition(hi, lo)
	if c > 0 {
		return true // overlap
	}
	if c < 0 {
		return false // strict gap
	}
	if hi.Inf != 0 {
		return true // both at the same infinity
	}
	// Same finite value: the shared point is covered unless both
	// sides exclude it.
	return !hi.Open || !lo.Open
}
