package netsec

import "sort"

// Rect is an abstract symbolic packet: the Cartesian product of a closed
// source interval and a closed destination interval. Every concrete packet
// (s, d) with s in S and d in D is represented by exactly this region.
type Rect struct {
	S Interval
	D Interval
}

func rectFromConstraints(cs []Constraint) Rect {
	r := Rect{
		S: Interval{Lo: 0, Hi: ^uint32(0)},
		D: Interval{Lo: 0, Hi: ^uint32(0)},
	}
	for _, c := range cs {
		if c.Field == Src {
			r.S = c.Range
		} else {
			r.D = c.Range
		}
	}
	return r
}

func (r Rect) field(f Field) Interval {
	if f == Src {
		return r.S
	}
	return r.D
}

func (r Rect) withField(f Field, iv Interval) Rect {
	if f == Src {
		r.S = iv
	} else {
		r.D = iv
	}
	return r
}

func (r Rect) containsPacket(p Packet) bool {
	return r.S.Contains(p.Src) && r.D.Contains(p.Dst)
}

// intersect returns the overlap of two rectangles and whether it is non-empty.
func (r Rect) intersect(o Rect) (Rect, bool) {
	s, okS := intersectInterval(r.S, o.S)
	d, okD := intersectInterval(r.D, o.D)
	if !okS || !okD {
		return Rect{}, false
	}
	return Rect{S: s, D: d}, true
}

func intersectInterval(a, b Interval) (Interval, bool) {
	lo := a.Lo
	if b.Lo > lo {
		lo = b.Lo
	}
	hi := a.Hi
	if b.Hi < hi {
		hi = b.Hi
	}
	if lo > hi {
		return Interval{}, false
	}
	return Interval{Lo: lo, Hi: hi}, true
}

// subtract returns the portions of r that lie outside o. The returned
// rectangles are pairwise disjoint and never empty.
func (r Rect) subtract(o Rect) []Rect {
	if _, ok := r.intersect(o); !ok {
		return []Rect{r}
	}
	sParts := subtractInterval(r.S, o.S)
	dParts := subtractInterval(r.D, o.D)
	out := make([]Rect, 0, len(sParts)+len(dParts))
	for _, s := range sParts {
		out = append(out, Rect{S: s, D: r.D})
	}
	for _, d := range dParts {
		out = append(out, Rect{S: r.S, D: d})
	}
	return out
}

// subtractInterval splits closed interval a into the pieces outside b.
func subtractInterval(a, b Interval) []Interval {
	if a.Hi < b.Lo || a.Lo > b.Hi {
		return []Interval{a}
	}
	var out []Interval
	if a.Lo < b.Lo {
		out = append(out, Interval{Lo: a.Lo, Hi: b.Lo - 1})
	}
	if b.Hi < a.Hi {
		out = append(out, Interval{Lo: b.Hi + 1, Hi: a.Hi})
	}
	return out
}

// covers reports whether every concrete packet in child is also in r.
func (r Rect) covers(child Rect) bool {
	return r.S.Lo <= child.S.Lo && child.S.Hi <= r.S.Hi &&
		r.D.Lo <= child.D.Lo && child.D.Hi <= r.D.Hi
}

// tryMerge returns the smallest rectangle covering both inputs and true when
// that bounding rectangle contains no concrete packet outside the union. Two
// rectangles merge exactly when they agree on one field and are adjacent or
// overlapping on the other.
func tryMerge(a, b Rect) (Rect, bool) {
	if a.S == b.S && touch(a.D, b.D) {
		return Rect{S: a.S, D: hull(a.D, b.D)}, true
	}
	if a.D == b.D && touch(a.S, b.S) {
		return Rect{S: hull(a.S, b.S), D: a.D}, true
	}
	return Rect{}, false
}

func touch(a, b Interval) bool {
	if a.Lo > b.Lo {
		a, b = b, a
	}
	// Guard against uint32 wraparound at the top of the address space:
	// MaxUint32 cannot be adjacent to a higher value.
	return a.Hi < ^uint32(0) && b.Lo <= a.Hi+1
}

func hull(a, b Interval) Interval {
	lo := a.Lo
	if b.Lo < lo {
		lo = b.Lo
	}
	hi := a.Hi
	if b.Hi > hi {
		hi = b.Hi
	}
	return Interval{Lo: lo, Hi: hi}
}

// canonicalize sorts and merges a disjoint set of rectangles, dropping
// duplicates and any region already covered by another one.
func canonicalize(in []Rect) []Rect {
	if len(in) == 0 {
		return nil
	}
	work := make([]Rect, 0, len(in))
	for _, r := range in {
		covered := false
		for _, q := range work {
			if q.covers(r) {
				covered = true
				break
			}
		}
		if !covered {
			work = append(work, r)
		}
	}
	sort.Slice(work, func(i, j int) bool {
		if work[i].S.Lo != work[j].S.Lo {
			return work[i].S.Lo < work[j].S.Lo
		}
		if work[i].S.Hi != work[j].S.Hi {
			return work[i].S.Hi < work[j].S.Hi
		}
		if work[i].D.Lo != work[j].D.Lo {
			return work[i].D.Lo < work[j].D.Lo
		}
		return work[i].D.Hi < work[j].D.Hi
	})
	merged := true
	for merged {
		merged = false
		for i := 0; i < len(work); i++ {
			for j := i + 1; j < len(work); j++ {
				if m, ok := tryMerge(work[i], work[j]); ok {
					work[i] = m
					work = append(work[:j], work[j+1:]...)
					merged = true
					break
				}
			}
			if merged {
				break
			}
		}
	}
	return work
}

// unionSet merges two canonical rectangle sets into one canonical set.
func unionSet(a, b []Rect) []Rect {
	return canonicalize(append(append([]Rect{}, a...), b...))
}

// coveredBySet reports whether r is fully contained in the rectangle set.
func coveredBySet(r Rect, set []Rect) bool {
	remaining := []Rect{r}
	for _, q := range set {
		var next []Rect
		for _, piece := range remaining {
			if _, ok := piece.intersect(q); ok {
				next = append(next, piece.subtract(q)...)
			} else {
				next = append(next, piece)
			}
		}
		remaining = next
		if len(remaining) == 0 {
			return true
		}
	}
	return false
}
