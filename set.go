package netsec

import (
	"sort"
	"strings"
)

// Set is a set of versions represented as a normalized list of disjoint,
// non-empty, sorted intervals. The zero value is the empty set.
//
// Normalization merges any two intervals whose union is contiguous,
// including pairs like [1.0.0, 1.0.5] and [1.0.6-0, 2.0.0] where no
// version exists between the endpoints.
type Set struct {
	ivs []Interval
}

// NewSet builds a Set from arbitrary intervals, normalizing them into
// sorted disjoint form. Empty intervals are dropped.
func NewSet(ivs ...Interval) Set {
	return Set{ivs: normalize(ivs)}
}

// Empty returns the empty set.
func Empty() Set { return Set{} }

// All returns the set of all versions.
func All() Set { return NewSet(Interval{Lo: InfiniteBound(), Hi: InfiniteBound()}) }

// Exact returns the set containing exactly v.
func Exact(v Version) Set {
	return NewSet(Interval{Lo: ClosedBound(v), Hi: ClosedBound(v)})
}

// ExactVersions returns the set containing exactly the given versions,
// used for precise backport exemptions.
func ExactVersions(vs ...Version) Set {
	ivs := make([]Interval, 0, len(vs))
	for _, v := range vs {
		ivs = append(ivs, Interval{Lo: ClosedBound(v), Hi: ClosedBound(v)})
	}
	return NewSet(ivs...)
}

// Closed returns the interval set [lo, hi].
func Closed(lo, hi Version) Set {
	return NewSet(Interval{Lo: ClosedBound(lo), Hi: ClosedBound(hi)})
}

// Open returns the interval set (lo, hi).
func Open(lo, hi Version) Set {
	return NewSet(Interval{Lo: OpenBound(lo), Hi: OpenBound(hi)})
}

// ClosedOpen returns the interval set [lo, hi).
func ClosedOpen(lo, hi Version) Set {
	return NewSet(Interval{Lo: ClosedBound(lo), Hi: OpenBound(hi)})
}

// OpenClosed returns the interval set (lo, hi].
func OpenClosed(lo, hi Version) Set {
	return NewSet(Interval{Lo: OpenBound(lo), Hi: ClosedBound(hi)})
}

// AtLeast returns [v, ∞).
func AtLeast(v Version) Set {
	return NewSet(Interval{Lo: ClosedBound(v), Hi: InfiniteBound()})
}

// GreaterThan returns (v, ∞).
func GreaterThan(v Version) Set {
	return NewSet(Interval{Lo: OpenBound(v), Hi: InfiniteBound()})
}

// AtMost returns (-∞, v].
func AtMost(v Version) Set {
	return NewSet(Interval{Lo: InfiniteBound(), Hi: ClosedBound(v)})
}

// LessThan returns (-∞, v).
func LessThan(v Version) Set {
	return NewSet(Interval{Lo: InfiniteBound(), Hi: OpenBound(v)})
}

// IsEmpty reports whether the set contains no version.
func (s Set) IsEmpty() bool { return len(s.ivs) == 0 }

// Intervals returns a copy of the normalized disjoint intervals backing s,
// in ascending order.
func (s Set) Intervals() []Interval {
	out := make([]Interval, len(s.ivs))
	copy(out, s.ivs)
	return out
}

// String renders the normalized disjoint intervals, e.g.
// "[1.0.0, 1.4.0) ∪ {1.4.2}".
func (s Set) String() string {
	if s.IsEmpty() {
		return "∅"
	}
	parts := make([]string, len(s.ivs))
	for i, iv := range s.ivs {
		parts[i] = iv.String()
	}
	return strings.Join(parts, " ∪ ")
}

// Contains reports whether v is in the set.
func (s Set) Contains(v Version) bool {
	_, hit := s.find(v)
	return hit
}

// Match is the hit evidence for a membership query: whether v is in the
// set, and if so which normalized interval contains it.
type Match struct {
	Hit      bool
	Version  Version
	Interval Interval // valid only when Hit is true
}

// Match explains why v is or is not in the set.
func (s Set) Match(v Version) Match {
	i, hit := s.find(v)
	if !hit {
		return Match{Version: v}
	}
	return Match{Hit: true, Version: v, Interval: s.ivs[i]}
}

func (s Set) find(v Version) (int, bool) {
	// First interval whose upper bound does not end strictly below v.
	i := sort.Search(len(s.ivs), func(i int) bool {
		hi := s.ivs[i].Hi
		return hi.Infinite || Compare(hi.V, v) >= 0
	})
	if i < len(s.ivs) && s.ivs[i].Contains(v) {
		return i, true
	}
	return 0, false
}

// Union returns the set of versions in either s or o.
func (s Set) Union(o Set) Set {
	ivs := make([]Interval, 0, len(s.ivs)+len(o.ivs))
	ivs = append(ivs, s.ivs...)
	ivs = append(ivs, o.ivs...)
	return Set{ivs: normalize(ivs)}
}

// Intersect returns the set of versions in both s and o.
func (s Set) Intersect(o Set) Set {
	var out []Interval
	i, j := 0, 0
	for i < len(s.ivs) && j < len(o.ivs) {
		a, b := s.ivs[i], o.ivs[j]
		iv := Interval{Lo: maxLo(a.Lo, b.Lo), Hi: minHi(a.Hi, b.Hi)}
		if !iv.isEmpty() {
			out = append(out, iv)
		}
		if hiLess(a.Hi, b.Hi) {
			i++
		} else {
			j++
		}
	}
	return Set{ivs: out}
}

// Difference returns the set of versions in s but not in o.
func (s Set) Difference(o Set) Set {
	if s.IsEmpty() || o.IsEmpty() {
		return s
	}
	var out []Interval
	for _, iv := range s.ivs {
		pieces := []Interval{iv}
		for _, cut := range o.ivs {
			var next []Interval
			for _, p := range pieces {
				next = append(next, subtract(p, cut)...)
			}
			pieces = next
			if len(pieces) == 0 {
				break
			}
		}
		out = append(out, pieces...)
	}
	return Set{ivs: normalize(out)}
}

// Exempt returns the set with the given exact versions removed, modeling
// precise backport exemptions from a vulnerable range.
func (s Set) Exempt(vs ...Version) Set {
	return s.Difference(ExactVersions(vs...))
}

// normalize sorts intervals, drops empty ones, and merges any pair whose
// union is contiguous, yielding the canonical disjoint form.
func normalize(ivs []Interval) []Interval {
	kept := ivs[:0]
	for _, iv := range ivs {
		if !iv.isEmpty() {
			kept = append(kept, iv)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return loLess(kept[i].Lo, kept[j].Lo) })
	var out []Interval
	for _, iv := range kept {
		if n := len(out); n > 0 && touches(out[n-1].Hi, iv.Lo) {
			out[n-1].Hi = maxHi(out[n-1].Hi, iv.Hi)
		} else {
			out = append(out, iv)
		}
	}
	return out
}
