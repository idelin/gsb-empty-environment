package interval

import (
	"math/big"
	"sort"
	"strings"
)

// Set is a normalized set of intervals: sorted by lower bound, pairwise
// disjoint, and never containing two adjacent intervals whose union would
// be gap-free (those are merged). All constructors and operations return
// normalized sets.
type Set struct {
	ivs []Interval
}

// Intervals returns a copy of the normalized, ordered, disjoint intervals.
func (s Set) Intervals() []Interval {
	out := make([]Interval, len(s.ivs))
	copy(out, s.ivs)
	return out
}

// IsEmpty reports whether the set contains no points.
func (s Set) IsEmpty() bool { return len(s.ivs) == 0 }

// NewSet normalizes the given intervals into a Set, dropping empties.
func NewSet(ivs ...Interval) Set { return Normalize(ivs) }

// Normalize sorts, drops empty intervals, and merges overlapping or
// gap-free-adjacent intervals.
func Normalize(ivs []Interval) Set {
	valid := make([]Interval, 0, len(ivs))
	for _, iv := range ivs {
		if !iv.IsEmpty() {
			valid = append(valid, iv)
		}
	}
	sort.Slice(valid, func(i, j int) bool {
		a, b := valid[i].Lo, valid[j].Lo
		if c := comparePos(a, b); c != 0 {
			return c < 0
		}
		// Same position: a closed lower bound starts earlier.
		return !a.Open && b.Open
	})
	var out []Interval
	for _, iv := range valid {
		if len(out) == 0 {
			out = append(out, iv)
			continue
		}
		last := &out[len(out)-1]
		c := comparePos(last.Hi, iv.Lo)
		gapFree := c > 0 ||
			(c == 0 && last.Hi.Kind == Finite && (!last.Hi.Open || !iv.Lo.Open))
		if gapFree {
			last.Hi = maxHi(last.Hi, iv.Hi)
		} else {
			out = append(out, iv)
		}
	}
	return Set{out}
}

// maxHi returns the endpoint that extends furthest as an upper bound;
// at equal finite positions the closed one wins.
func maxHi(a, b Endpoint) Endpoint {
	if c := comparePos(a, b); c != 0 {
		if c > 0 {
			return a
		}
		return b
	}
	if a.Open && !b.Open {
		return b
	}
	return a
}

// maxLo returns the endpoint that starts latest as a lower bound;
// at equal finite positions the open one wins (intersection semantics).
func maxLo(a, b Endpoint) Endpoint {
	if c := comparePos(a, b); c != 0 {
		if c > 0 {
			return a
		}
		return b
	}
	if !a.Open && b.Open {
		return b
	}
	return a
}

// minHi returns the endpoint that ends earliest as an upper bound;
// at equal finite positions the open one wins (intersection semantics).
func minHi(a, b Endpoint) Endpoint {
	if c := comparePos(a, b); c != 0 {
		if c < 0 {
			return a
		}
		return b
	}
	if a.Open && !b.Open {
		return a
	}
	return b
}

// Union returns the normalized union of two sets.
func Union(a, b Set) Set {
	ivs := make([]Interval, 0, len(a.ivs)+len(b.ivs))
	ivs = append(ivs, a.ivs...)
	ivs = append(ivs, b.ivs...)
	return Normalize(ivs)
}

// Intersect returns the normalized intersection of two sets.
func Intersect(a, b Set) Set {
	var out []Interval
	i, j := 0, 0
	for i < len(a.ivs) && j < len(b.ivs) {
		iv := Interval{maxLo(a.ivs[i].Lo, b.ivs[j].Lo), minHi(a.ivs[i].Hi, b.ivs[j].Hi)}
		if !iv.IsEmpty() {
			out = append(out, iv)
		}
		// Advance the interval that ends first; at equal positions the
		// open (shorter) one ends first.
		c := comparePos(a.ivs[i].Hi, b.ivs[j].Hi)
		switch {
		case c < 0:
			i++
		case c > 0:
			j++
		case a.ivs[i].Hi.Open && !b.ivs[j].Hi.Open:
			i++
		case !a.ivs[i].Hi.Open && b.ivs[j].Hi.Open:
			j++
		default:
			i++
			j++
		}
	}
	return Normalize(out)
}

// Subtract returns the normalized difference a \ b.
func Subtract(a, b Set) Set {
	var out []Interval
	for _, iv := range a.ivs {
		pieces := []Interval{iv}
		for _, bv := range b.ivs {
			var next []Interval
			for _, p := range pieces {
				next = append(next, subtractOne(p, bv)...)
			}
			pieces = next
			if len(pieces) == 0 {
				break
			}
		}
		out = append(out, pieces...)
	}
	return Normalize(out)
}

// subtractOne returns p \ b as zero, one, or two intervals.
func subtractOne(p, b Interval) []Interval {
	var out []Interval
	left := Interval{p.Lo, minHi(p.Hi, flipOpen(b.Lo))}  // part of p below b
	right := Interval{maxLo(p.Lo, flipOpen(b.Hi)), p.Hi} // part of p above b
	if !left.IsEmpty() {
		out = append(out, left)
	}
	if !right.IsEmpty() {
		out = append(out, right)
	}
	return out
}

// Contains reports whether the finite rational x lies in the set.
func (s Set) Contains(x *big.Rat) bool {
	i := sort.Search(len(s.ivs), func(i int) bool {
		return comparePos(s.ivs[i].Hi, ClosedEnd(x)) >= 0
	})
	return i < len(s.ivs) && s.ivs[i].Contains(x)
}

func (s Set) String() string {
	if len(s.ivs) == 0 {
		return "{}"
	}
	parts := make([]string, len(s.ivs))
	for i, iv := range s.ivs {
		parts[i] = iv.String()
	}
	return strings.Join(parts, " ∪ ")
}
