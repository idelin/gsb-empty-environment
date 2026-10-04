// Package rectunion computes the area and perimeter of the union of
// closed, axis-aligned rectangles with integer coordinates.
//
// Rectangles are closed sets: [X1,X2] x [Y1,Y2]. Zero-area rectangles
// (degenerate lines or points) do not contribute to the union.
// Overlapping or shared edges are counted only where they are exposed;
// edges shared between two rectangles in the interior of the union are
// not part of the perimeter. Results are exact big integers and the
// algorithm is a sweep line, not rasterization.
package rectunion

import (
	"math/big"
	"sort"
)

// Rect is a closed axis-aligned rectangle [X1,X2] x [Y1,Y2].
// Corners may be given in any order; they are normalized internally.
type Rect struct {
	X1, Y1, X2, Y2 int64
}

func (r Rect) normalized() Rect {
	if r.X1 > r.X2 {
		r.X1, r.X2 = r.X2, r.X1
	}
	if r.Y1 > r.Y2 {
		r.Y1, r.Y2 = r.Y2, r.Y1
	}
	return r
}

func (r Rect) degenerate() bool { return r.X1 == r.X2 || r.Y1 == r.Y2 }

// interval is a closed y-interval [lo, hi] with lo < hi.
type interval struct {
	lo, hi int64
}

// unionIntervals merges the y-projections of rects into disjoint,
// sorted intervals. Overlapping and merely touching intervals merge,
// matching the closed-set semantics of the union.
func unionIntervals(rects []Rect) []interval {
	if len(rects) == 0 {
		return nil
	}
	iv := make([]interval, 0, len(rects))
	for _, r := range rects {
		iv = append(iv, interval{r.Y1, r.Y2})
	}
	sort.Slice(iv, func(i, j int) bool { return iv[i].lo < iv[j].lo })
	merged := make([]interval, 0, len(iv))
	cur := iv[0]
	for _, nxt := range iv[1:] {
		if nxt.lo <= cur.hi {
			if nxt.hi > cur.hi {
				cur.hi = nxt.hi
			}
		} else {
			merged = append(merged, cur)
			cur = nxt
		}
	}
	merged = append(merged, cur)
	return merged
}

// totalLen returns the sum of interval lengths as a big integer.
func totalLen(iv []interval) *big.Int {
	sum := new(big.Int)
	for _, in := range iv {
		sum.Add(sum, big.NewInt(in.hi-in.lo))
	}
	return sum
}

// intersectionLen returns the total overlap length of two sorted,
// disjoint interval lists.
func intersectionLen(a, b []interval) *big.Int {
	sum := new(big.Int)
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		lo := a[i].lo
		if b[j].lo > lo {
			lo = b[j].lo
		}
		hi := a[i].hi
		if b[j].hi < hi {
			hi = b[j].hi
		}
		if hi > lo {
			sum.Add(sum, big.NewInt(hi-lo))
		}
		if a[i].hi < b[j].hi {
			i++
		} else {
			j++
		}
	}
	return sum
}

// symDiffLen returns |A \ B| + |B \ A| for two interval lists, i.e. the
// length of the boundary that appears or disappears across a sweep event.
func symDiffLen(a, b []interval) *big.Int {
	sum := totalLen(a)
	sum.Add(sum, totalLen(b))
	sum.Sub(sum, new(big.Int).Mul(big.NewInt(2), intersectionLen(a, b)))
	return sum
}

type event struct {
	x     int64
	start bool
	idx   int
}

// Union returns the area and perimeter of the union of the given
// rectangles. Zero-area rectangles are ignored. The sweep runs along x;
// at every event the exposed vertical edge length is the symmetric
// difference of the y-coverage on both sides, and inside each slab the
// horizontal edges contribute 2 * (number of y-components) * width.
func Union(rects []Rect) (area, perimeter *big.Int) {
	area = new(big.Int)
	perimeter = new(big.Int)

	rs := make([]Rect, 0, len(rects))
	for _, r := range rects {
		r = r.normalized()
		if r.degenerate() {
			continue
		}
		rs = append(rs, r)
	}
	if len(rs) == 0 {
		return area, perimeter
	}

	events := make([]event, 0, 2*len(rs))
	for i, r := range rs {
		events = append(events, event{x: r.X1, start: true, idx: i})
		events = append(events, event{x: r.X2, start: false, idx: i})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].x < events[j].x })

	active := make([]bool, len(rs))
	activeRects := func() []Rect {
		out := make([]Rect, 0, len(rs))
		for i, on := range active {
			if on {
				out = append(out, rs[i])
			}
		}
		return out
	}

	var prevUnion []interval
	prevX := events[0].x
	for i := 0; i < len(events); {
		x := events[i].x
		if dx := x - prevX; dx > 0 {
			// Slab (prevX, x): accumulate area and horizontal edges.
			measure := totalLen(prevUnion)
			area.Add(area, new(big.Int).Mul(measure, big.NewInt(dx)))
			horiz := new(big.Int).Mul(big.NewInt(2*int64(len(prevUnion))), big.NewInt(dx))
			perimeter.Add(perimeter, horiz)
		}
		for i < len(events) && events[i].x == x {
			active[events[i].idx] = events[i].start
			i++
		}
		// Exposed vertical edges at x: coverage change across the line.
		newUnion := unionIntervals(activeRects())
		perimeter.Add(perimeter, symDiffLen(prevUnion, newUnion))
		prevUnion = newUnion
		prevX = x
	}
	return area, perimeter
}
