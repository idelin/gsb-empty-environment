package viewopt

import (
	"fmt"
	"math"
	"sort"
)

// Interval is an inclusive integer interval.  HasLo/HasHi describe whether
// the corresponding endpoint is present; a missing endpoint is unbounded.
//
// The zero value represents the whole int64 line (-infinity, +infinity).
// Use Between, AtLeast, AtMost, EmptyInterval and AllInterval to construct
// intervals explicitly.
//
// Bounds are inclusive.  An open integer bound can be represented by
// shifting it by one (for example (lo,hi] is [lo+1,hi]).
type Interval struct {
	Lo    int64
	Hi    int64
	HasLo bool
	HasHi bool
}

// Between returns [lo, hi].  If lo > hi the result is empty.
func Between(lo, hi int64) Interval {
	return Interval{Lo: lo, Hi: hi, HasLo: true, HasHi: true}
}

// AtLeast returns [lo, +infinity).
func AtLeast(lo int64) Interval {
	return Interval{Lo: lo, HasLo: true}
}

// AtMost returns (-infinity, hi].
func AtMost(hi int64) Interval {
	return Interval{Hi: hi, HasHi: true}
}

// AllInterval returns the whole int64 line.
func AllInterval() Interval { return Interval{} }

// EmptyInterval returns an empty interval.
func EmptyInterval() Interval { return Interval{Lo: 1, Hi: 0, HasLo: true, HasHi: true} }

// IsEmpty reports whether the interval contains no integer.
func (in Interval) IsEmpty() bool {
	return in.HasLo && in.HasHi && in.Lo > in.Hi
}

// Contains reports whether v belongs to the interval.
func (in Interval) Contains(v int64) bool {
	if in.IsEmpty() {
		return false
	}
	if in.HasLo && v < in.Lo {
		return false
	}
	if in.HasHi && v > in.Hi {
		return false
	}
	return true
}

// Intersect returns the set intersection.
func (in Interval) Intersect(o Interval) Interval {
	if in.IsEmpty() || o.IsEmpty() {
		return EmptyInterval()
	}
	out := in
	if o.HasLo {
		if !out.HasLo || o.Lo > out.Lo {
			out.Lo, out.HasLo = o.Lo, true
		}
	}
	if o.HasHi {
		if !out.HasHi || o.Hi < out.Hi {
			out.Hi, out.HasHi = o.Hi, true
		}
	}
	if out.HasLo && out.HasHi && out.Lo > out.Hi {
		return EmptyInterval()
	}
	return out
}

// IsSubsetOf reports whether every integer in in is also in o.
func (in Interval) IsSubsetOf(o Interval) bool {
	if in.IsEmpty() {
		return true
	}
	if o.IsEmpty() {
		return false
	}
	if in.HasLo {
		if !o.HasLo || in.Lo < o.Lo {
			return false
		}
	} else if o.HasLo {
		return false
	}
	if in.HasHi {
		if !o.HasHi || in.Hi > o.Hi {
			return false
		}
	} else if o.HasHi {
		return false
	}
	return true
}

// Equal reports interval equality.  Empty intervals are equal.
func (in Interval) Equal(o Interval) bool {
	if in.IsEmpty() || o.IsEmpty() {
		return in.IsEmpty() && o.IsEmpty()
	}
	if in.HasLo != o.HasLo || in.HasHi != o.HasHi {
		return false
	}
	if in.HasLo && in.Lo != o.Lo {
		return false
	}
	if in.HasHi && in.Hi != o.Hi {
		return false
	}
	return true
}

func (in Interval) String() string {
	if in.IsEmpty() {
		return "empty"
	}
	lo, hi := "-inf", "+inf"
	if in.HasLo {
		lo = fmt.Sprintf("%d", in.Lo)
	}
	if in.HasHi {
		hi = fmt.Sprintf("%d", in.Hi)
	}
	return "[" + lo + "," + hi + "]"
}

// cell is a maximal contiguous range on which every supplied interval's
// membership is constant.  It may be unbounded on either side.
type cell struct {
	lo, hi int64
	hasLo  bool
	hasHi  bool
}

func (c cell) sample() int64 {
	if c.hasLo {
		return c.lo
	}
	if c.hasHi {
		return c.hi
	}
	return 0
}

// minValue picks a deterministic, small representative.  For a bounded cell
// this is its lower endpoint; for a cell unbounded below it is the upper
// endpoint, and for the whole line it is zero.
func (c cell) minValue() int64 { return c.sample() }

func (c cell) intersect(in Interval) (cell, bool) {
	if in.IsEmpty() {
		return cell{}, false
	}
	out := c
	if in.HasLo && (!out.hasLo || in.Lo > out.lo) {
		out.lo, out.hasLo = in.Lo, true
	}
	if in.HasHi && (!out.hasHi || in.Hi < out.hi) {
		out.hi, out.hasHi = in.Hi, true
	}
	if out.hasLo && out.hasHi && out.lo > out.hi {
		return cell{}, false
	}
	return out, true
}

// partitionCells partitions domain by all interval endpoints.  The returned
// cells are maximal ranges of constant membership for every interval.  It
// never enumerates individual integers, so ranges such as 2^40 are cheap.
func partitionCells(domain Interval, intervals []Interval) []cell {
	if domain.IsEmpty() {
		return nil
	}
	cuts := make([]int64, 0, 2*(len(intervals)+1))
	addCut := func(v int64) { cuts = append(cuts, v) }

	if domain.HasLo {
		addCut(domain.Lo)
	}
	if domain.HasHi && domain.Hi < math.MaxInt64 {
		addCut(domain.Hi + 1)
	}
	for _, in := range intervals {
		if in.IsEmpty() {
			continue
		}
		if in.HasLo {
			addCut(in.Lo)
		}
		if in.HasHi && in.Hi < math.MaxInt64 {
			addCut(in.Hi + 1)
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	uniq := cuts[:0]
	for _, v := range cuts {
		if len(uniq) == 0 || uniq[len(uniq)-1] != v {
			uniq = append(uniq, v)
		}
	}

	var all []cell
	if len(uniq) == 0 {
		all = []cell{{}}
	} else {
		if uniq[0] > math.MinInt64 {
			all = append(all, cell{hi: uniq[0] - 1, hasHi: true})
		}
		for i, cut := range uniq {
			c := cell{lo: cut, hasLo: true}
			if i+1 < len(uniq) {
				c.hi, c.hasHi = uniq[i+1]-1, true
			}
			all = append(all, c)
		}
	}

	out := make([]cell, 0, len(all))
	for _, c := range all {
		clamped, ok := c.intersect(domain)
		if !ok {
			continue
		}
		// A clamped cell remains constant because every interval endpoint
		// inside the domain was already a cut point.
		out = append(out, clamped)
	}
	return out
}
