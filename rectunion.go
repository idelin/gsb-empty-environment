// Package rectunion computes the area and perimeter of the union of
// axis-aligned rectangles with integer coordinates, using a sweep-line
// algorithm and arbitrary-precision arithmetic.
//
// Rectangles are closed sets: [X1, X2] x [Y1, Y2]. Zero-area rectangles
// (X1 == X2 or Y1 == Y2) contribute nothing. Overlapping or abutting
// rectangles merge: interior shared edges are not part of the perimeter,
// and only the exposed parts of overlapping edges are counted.
package rectunion

import (
	"math/big"
	"sort"
)

// Rect is a closed axis-aligned rectangle [X1, X2] x [Y1, Y2].
// Coordinates may be given in any order; they are normalized internally.
type Rect struct {
	X1, Y1, X2, Y2 *big.Int
}

// NewRect builds a Rect from int64 coordinates.
func NewRect(x1, y1, x2, y2 int64) Rect {
	return Rect{
		X1: big.NewInt(x1),
		Y1: big.NewInt(y1),
		X2: big.NewInt(x2),
		Y2: big.NewInt(y2),
	}
}

// Union returns the area and perimeter of the union of the given
// rectangles. Both results are exact big integers. The returned values
// are freshly allocated; inputs are never mutated.
func Union(rects []Rect) (area, perimeter *big.Int) {
	norm := make([]Rect, 0, len(rects))
	for _, r := range rects {
		x1, x2 := minMax(r.X1, r.X2)
		y1, y2 := minMax(r.Y1, r.Y2)
		// Zero-area rectangles (degenerate edges/points) contribute nothing.
		if x1.Cmp(x2) == 0 || y1.Cmp(y2) == 0 {
			continue
		}
		norm = append(norm, Rect{X1: x1, Y1: y1, X2: x2, Y2: y2})
	}
	area = new(big.Int)
	perimeter = new(big.Int)
	if len(norm) == 0 {
		return area, perimeter
	}

	// Sweep line along x: the active y-interval set only changes at
	// rectangle x-boundaries, so each slab between consecutive distinct
	// x-coordinates has a constant cross-section.
	xs := make([]*big.Int, 0, 2*len(norm))
	for _, r := range norm {
		xs = append(xs, r.X1, r.X2)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i].Cmp(xs[j]) < 0 })
	uniq := xs[:0]
	for _, x := range xs {
		if len(uniq) == 0 || uniq[len(uniq)-1].Cmp(x) != 0 {
			uniq = append(uniq, x)
		}
	}

	// sections[i] is the merged y-interval list of the slab
	// [uniq[i], uniq[i+1]].
	sections := make([][]interval, len(uniq)-1)
	for i := 0; i+1 < len(uniq); i++ {
		lo, hi := uniq[i], uniq[i+1]
		var ys []interval
		for _, r := range norm {
			if r.X1.Cmp(lo) <= 0 && r.X2.Cmp(hi) >= 0 {
				ys = append(ys, interval{r.Y1, r.Y2})
			}
		}
		merged := mergeIntervals(ys)
		sections[i] = merged

		width := new(big.Int).Sub(hi, lo)
		// Area of the slab.
		area.Add(area, new(big.Int).Mul(width, totalLength(merged)))
		// Horizontal perimeter: every merged interval contributes a top
		// and a bottom edge spanning the slab width.
		edges := new(big.Int).Mul(width, big.NewInt(2*int64(len(merged))))
		perimeter.Add(perimeter, edges)
	}

	// Vertical perimeter: at each event x, the boundary consists of the
	// symmetric difference of the cross-sections on either side (empty
	// outside the swept range).
	var prev []interval
	for i, x := range uniq {
		_ = x
		var cur []interval
		if i < len(sections) {
			cur = sections[i]
		}
		perimeter.Add(perimeter, symDiffLength(prev, cur))
		prev = cur
	}
	return area, perimeter
}

// interval is a closed y-interval [lo, hi] with lo < hi.
type interval struct {
	lo, hi *big.Int
}

func minMax(a, b *big.Int) (lo, hi *big.Int) {
	if a.Cmp(b) <= 0 {
		return a, b
	}
	return b, a
}

// mergeIntervals merges overlapping or touching intervals. Touching
// intervals ([a,b] and [b,c]) merge because closed rectangles sharing
// an edge form a connected union with no boundary between them.
func mergeIntervals(ivs []interval) []interval {
	if len(ivs) == 0 {
		return nil
	}
	sorted := make([]interval, len(ivs))
	copy(sorted, ivs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].lo.Cmp(sorted[j].lo) < 0
	})
	merged := []interval{sorted[0]}
	for _, iv := range sorted[1:] {
		last := &merged[len(merged)-1]
		if iv.lo.Cmp(last.hi) <= 0 {
			if iv.hi.Cmp(last.hi) > 0 {
				last.hi = iv.hi
			}
		} else {
			merged = append(merged, iv)
		}
	}
	return merged
}

func totalLength(merged []interval) *big.Int {
	total := new(big.Int)
	for _, iv := range merged {
		total.Add(total, new(big.Int).Sub(iv.hi, iv.lo))
	}
	return total
}

// symDiffLength returns the total length covered by exactly one of the
// two merged interval lists: len(a) + len(b) - 2*len(a ∩ b).
func symDiffLength(a, b []interval) *big.Int {
	result := totalLength(a)
	result.Add(result, totalLength(b))
	overlap := new(big.Int)
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		lo := a[i].lo
		if b[j].lo.Cmp(lo) > 0 {
			lo = b[j].lo
		}
		hi := a[i].hi
		if b[j].hi.Cmp(hi) < 0 {
			hi = b[j].hi
		}
		if lo.Cmp(hi) < 0 {
			overlap.Add(overlap, new(big.Int).Sub(hi, lo))
		}
		if a[i].hi.Cmp(b[j].hi) < 0 {
			i++
		} else {
			j++
		}
	}
	overlap.Mul(overlap, big.NewInt(2))
	return result.Sub(result, overlap)
}
