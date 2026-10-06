// Package gridfill classifies integer grid cell centers against the region
// filled by an axis-aligned path under the non-zero winding rule.
//
// A cell center is the point (i+0.5, j+0.5) for integers i, j. A center
// lying exactly on a path edge is reported as Boundary rather than Inside
// or Outside. Edges traced twice in opposite directions cancel, as the
// non-zero winding rule requires.
//
// Boundary detection uses exact float64 comparison: a center is on an edge
// only when the edge coordinate equals the center coordinate exactly (e.g.
// edges at half-integer coordinates pass through cell centers).
package gridfill

import (
	"errors"
	"math"
	"sort"
)

// Class is the fill classification of a grid cell center.
type Class int

const (
	Outside Class = iota
	Inside
	Boundary
)

func (c Class) String() string {
	switch c {
	case Inside:
		return "Inside"
	case Boundary:
		return "Boundary"
	default:
		return "Outside"
	}
}

// Point is a 2D point.
type Point struct {
	X, Y float64
}

// Segment is an axis-aligned edge of a path, traced from A to B. The
// direction matters: it determines the sign of the winding contribution.
type Segment struct {
	A, B Point
}

// SegmentsFromPath converts a polyline into segments. Every segment must be
// horizontal or vertical; zero-length segments are dropped.
func SegmentsFromPath(pts []Point) ([]Segment, error) {
	if len(pts) < 2 {
		return nil, errors.New("gridfill: path needs at least 2 points")
	}
	var segs []Segment
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		if a == b {
			continue
		}
		if a.X != b.X && a.Y != b.Y {
			return nil, errors.New("gridfill: segment is not axis-aligned")
		}
		segs = append(segs, Segment{A: a, B: b})
	}
	return segs, nil
}

// Grid holds the classification of the cell centers (i+0.5, j+0.5) over the
// integer bounds rectangle [X0,X1) x [Y0,Y1).
type Grid struct {
	X0, Y0, X1, Y1 int
	Cells          []Class // row-major; row 0 corresponds to j = Y0
}

// NewGrid allocates a zero-valued (all Outside) grid.
func NewGrid(x0, y0, x1, y1 int) *Grid {
	if x1 < x0 || y1 < y0 {
		panic("gridfill: empty or inverted bounds")
	}
	return &Grid{
		X0: x0, Y0: y0, X1: x1, Y1: y1,
		Cells: make([]Class, (x1-x0)*(y1-y0)),
	}
}

// At returns the classification of the cell center (i+0.5, j+0.5).
func (g *Grid) At(i, j int) Class {
	return g.Cells[(j-g.Y0)*(g.X1-g.X0)+(i-g.X0)]
}

func (g *Grid) set(i, j int, c Class) {
	g.Cells[(j-g.Y0)*(g.X1-g.X0)+(i-g.X0)] = c
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// onSegment reports whether p lies exactly on s (endpoints included).
func onSegment(s Segment, p Point) bool {
	if s.A.X == s.B.X { // vertical
		return p.X == s.A.X && p.Y >= minf(s.A.Y, s.B.Y) && p.Y <= maxf(s.A.Y, s.B.Y)
	}
	return p.Y == s.A.Y && p.X >= minf(s.A.X, s.B.X) && p.X <= maxf(s.A.X, s.B.X)
}

// windingNumber computes the winding number of the segments around p using a
// horizontal ray cast towards +x. Vertical edges crossing the ray contribute
// +1 (upward) or -1 (downward); the half-open interval [lo,hi) counts every
// shared vertex exactly once. Horizontal edges never cross the ray.
func windingNumber(segs []Segment, p Point) int {
	w := 0
	for _, s := range segs {
		if s.A.Y == s.B.Y {
			continue
		}
		if s.A.X <= p.X {
			continue
		}
		lo, hi := s.A.Y, s.B.Y
		delta := 1
		if lo > hi {
			lo, hi = hi, lo
			delta = -1
		}
		if p.Y >= lo && p.Y < hi {
			w += delta
		}
	}
	return w
}

// ClassifyPoint classifies a single point with the direct ray-casting
// method. Points exactly on an edge are Boundary.
func ClassifyPoint(segs []Segment, p Point) Class {
	for _, s := range segs {
		if onSegment(s, p) {
			return Boundary
		}
	}
	if windingNumber(segs, p) != 0 {
		return Inside
	}
	return Outside
}

// FillGridPointwise classifies every cell center in the bounds rectangle by
// direct ray casting. It is the reference implementation used to verify
// ScanlineFill.
func FillGridPointwise(segs []Segment, x0, y0, x1, y1 int) *Grid {
	g := NewGrid(x0, y0, x1, y1)
	for j := y0; j < y1; j++ {
		for i := x0; i < x1; i++ {
			g.set(i, j, ClassifyPoint(segs, Point{X: float64(i) + 0.5, Y: float64(j) + 0.5}))
		}
	}
	return g
}

// ScanlineFill classifies every cell center in the bounds rectangle in bulk:
// each scanline y = j+0.5 collects its vertical-edge crossings, sorts them
// by x, and fills the runs where the accumulated winding number is non-zero.
// Centers exactly on an edge are then marked Boundary.
func ScanlineFill(segs []Segment, x0, y0, x1, y1 int) *Grid {
	g := NewGrid(x0, y0, x1, y1)
	type crossing struct {
		x     float64
		delta int
	}
	for j := y0; j < y1; j++ {
		y := float64(j) + 0.5
		var xs []crossing
		for _, s := range segs {
			if s.A.Y == s.B.Y {
				continue
			}
			lo, hi := s.A.Y, s.B.Y
			delta := 1
			if lo > hi {
				lo, hi = hi, lo
				delta = -1
			}
			if y >= lo && y < hi {
				xs = append(xs, crossing{x: s.A.X, delta: delta})
			}
		}
		sort.Slice(xs, func(a, b int) bool { return xs[a].x < xs[b].x })

		// A center's winding number sums the crossings to its right (the ray
		// points towards +x), so walk the crossings from right to left.
		winding := 0
		prev := math.Inf(1)
		fill := func(next float64) {
			if winding == 0 {
				return
			}
			lo, hi := x0, x1-1
			if !math.IsInf(next, -1) {
				if v := int(math.Floor(next-0.5)) + 1; v > lo {
					lo = v
				}
			}
			if !math.IsInf(prev, 1) {
				if v := int(math.Ceil(prev-0.5)) - 1; v < hi {
					hi = v
				}
			}
			for i := lo; i <= hi; i++ {
				cx := float64(i) + 0.5
				if cx > next && cx < prev {
					g.set(i, j, Inside)
				}
			}
		}
		for k := len(xs) - 1; k >= 0; k-- {
			fill(xs[k].x)
			winding += xs[k].delta
			prev = xs[k].x
		}
		fill(math.Inf(-1))
	}
	markBoundary(g, segs)
	return g
}

// markBoundary overrides every cell center lying exactly on a segment.
func markBoundary(g *Grid, segs []Segment) {
	for _, s := range segs {
		if s.A.X == s.B.X {
			markLine(g, true, s.A.X, s.A.Y, s.B.Y)
		} else {
			markLine(g, false, s.A.Y, s.A.X, s.B.X)
		}
	}
}

// markLine marks centers on an axis-aligned segment. vertical selects the
// orientation, fix is the constant coordinate, and [lo,hi] the varying one.
func markLine(g *Grid, vertical bool, fix, a, b float64) {
	// The fixed coordinate must equal a center line i+0.5 exactly.
	k := int(math.Floor(fix - 0.5))
	if float64(k)+0.5 != fix {
		return
	}
	lo, hi := minf(a, b), maxf(a, b)
	nLo := int(math.Ceil(lo - 0.5))
	nHi := int(math.Floor(hi - 0.5))
	for n := nLo; n <= nHi; n++ {
		c := float64(n) + 0.5
		if c < lo || c > hi {
			continue
		}
		i, j := k, n
		if !vertical {
			i, j = n, k
		}
		if i >= g.X0 && i < g.X1 && j >= g.Y0 && j < g.Y1 {
			g.set(i, j, Boundary)
		}
	}
}
