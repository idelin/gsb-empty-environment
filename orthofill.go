// Package orthofill determines, for axis-aligned (horizontal/vertical) closed
// paths, which integer grid cell centers lie inside the filled region under
// the non-zero winding rule.
//
// Cell centers are the integer lattice points (x, y): cell (x, y) is the unit
// square centered at that point. Paths may self-intersect and may retrace
// edges; an edge retraced in the opposite direction cancels its winding
// contribution, as required by the non-zero rule. Centers lying exactly on a
// drawn edge are not classified as inside or outside; they are reported
// separately as boundary.
//
// Each subpath is closed implicitly by connecting its last vertex back to its
// first. Every segment (including the implicit closing segment) must be
// horizontal or vertical. Zero-length segments are ignored.
//
// FillScanline computes the classification for a whole rectangle of cells in
// one sweep. FillRay is the direct reference implementation that casts an
// independent ray per cell center; the two are cross-checked in tests.
package orthofill

import (
	"errors"
	"fmt"
	"sort"
)

// ErrNotOrthogonal is returned when a path segment is neither horizontal nor
// vertical.
var ErrNotOrthogonal = errors.New("orthofill: segment is neither horizontal nor vertical")

// Point is an integer grid coordinate, used both for path vertices and for
// cell centers.
type Point struct {
	X, Y int
}

// Rect is an inclusive rectangle of cell centers to classify.
type Rect struct {
	MinX, MinY, MaxX, MaxY int
}

// Fill is the classification of a rectangle of cell centers. Both slices are
// sorted row-major (by Y, then X) and are disjoint.
type Fill struct {
	Inside   []Point // centers with non-zero winding number
	Boundary []Point // centers lying exactly on a drawn edge
}

type edge struct {
	x1, y1, x2, y2 int
}

func (e edge) vertical() bool { return e.x1 == e.x2 }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// collectEdges flattens all subpaths into directed segments, closing each
// subpath and dropping zero-length segments.
func collectEdges(paths [][]Point) ([]edge, error) {
	var es []edge
	for _, path := range paths {
		n := len(path)
		if n < 2 {
			continue
		}
		for i := 0; i < n; i++ {
			a := path[i]
			b := path[(i+1)%n]
			if a == b {
				continue
			}
			if a.X != b.X && a.Y != b.Y {
				return nil, fmt.Errorf("%v: (%d,%d)->(%d,%d)", ErrNotOrthogonal, a.X, a.Y, b.X, b.Y)
			}
			es = append(es, edge{a.X, a.Y, b.X, b.Y})
		}
	}
	return es, nil
}

// boundarySet returns the set of cell centers inside area that lie exactly on
// any drawn edge.
func boundarySet(es []edge, area Rect) map[Point]bool {
	set := make(map[Point]bool)
	for _, e := range es {
		if e.vertical() {
			if e.x1 < area.MinX || e.x1 > area.MaxX {
				continue
			}
			lo := max(min(e.y1, e.y2), area.MinY)
			hi := min(max(e.y1, e.y2), area.MaxY)
			for y := lo; y <= hi; y++ {
				set[Point{e.x1, y}] = true
			}
		} else {
			if e.y1 < area.MinY || e.y1 > area.MaxY {
				continue
			}
			lo := max(min(e.x1, e.x2), area.MinX)
			hi := min(max(e.x1, e.x2), area.MaxX)
			for x := lo; x <= hi; x++ {
				set[Point{x, e.y1}] = true
			}
		}
	}
	return set
}

// classify computes the winding number of p with respect to the edges by
// casting a ray from p towards +X, and reports whether p lies exactly on an
// edge. Horizontal edges never cross a horizontal ray; vertical edges are
// counted with the half-open rule minY <= p.Y < maxY so that a ray passing
// through a shared endpoint counts exactly one crossing. An upward edge
// contributes +1, a downward edge -1, so reversed duplicate edges cancel.
func classify(es []edge, p Point) (winding int, onEdge bool) {
	for _, e := range es {
		if e.vertical() {
			lo, hi := min(e.y1, e.y2), max(e.y1, e.y2)
			if p.X == e.x1 && lo <= p.Y && p.Y <= hi {
				onEdge = true
			}
			if e.x1 > p.X && lo <= p.Y && p.Y < hi {
				if e.y2 > e.y1 {
					winding++
				} else {
					winding--
				}
			}
		} else {
			if p.Y == e.y1 && min(e.x1, e.x2) <= p.X && p.X <= max(e.x1, e.x2) {
				onEdge = true
			}
		}
	}
	return winding, onEdge
}

// Winding returns the winding number of p with respect to the paths and
// whether p lies exactly on a drawn edge.
func Winding(paths [][]Point, p Point) (winding int, onEdge bool, err error) {
	es, err := collectEdges(paths)
	if err != nil {
		return 0, false, err
	}
	winding, onEdge = classify(es, p)
	return winding, onEdge, nil
}

// FillScanline classifies every cell center in area in a single sweep: for
// each row it gathers the crossings of vertical edges with the row's
// horizontal line once, then evaluates all centers of the row via suffix sums
// of crossing deltas.
func FillScanline(paths [][]Point, area Rect) (Fill, error) {
	es, err := collectEdges(paths)
	if err != nil {
		return Fill{}, err
	}
	boundary := boundarySet(es, area)

	type crossing struct {
		x     int
		delta int
	}
	rowCrossings := make(map[int][]crossing)
	for _, e := range es {
		if !e.vertical() {
			continue
		}
		delta := 1
		if e.y2 < e.y1 {
			delta = -1
		}
		lo := max(min(e.y1, e.y2), area.MinY)
		hi := min(max(e.y1, e.y2)-1, area.MaxY) // half-open: y < maxY of edge
		for y := lo; y <= hi; y++ {
			rowCrossings[y] = append(rowCrossings[y], crossing{e.x1, delta})
		}
	}

	var fill Fill
	for y := area.MinY; y <= area.MaxY; y++ {
		cs := rowCrossings[y]
		sort.Slice(cs, func(i, j int) bool { return cs[i].x < cs[j].x })
		// suffix[i] = sum of deltas of crossings with x >= cs[i].x, so the
		// winding of center (cx, y) is suffix[upperBound(cx)].
		suffix := make([]int, len(cs)+1)
		for i := len(cs) - 1; i >= 0; i-- {
			suffix[i] = suffix[i+1] + cs[i].delta
		}
		for x := area.MinX; x <= area.MaxX; x++ {
			p := Point{x, y}
			if boundary[p] {
				fill.Boundary = append(fill.Boundary, p)
				continue
			}
			i := sort.Search(len(cs), func(i int) bool { return cs[i].x > x })
			if suffix[i] != 0 {
				fill.Inside = append(fill.Inside, p)
			}
		}
	}
	return fill, nil
}

// FillRay is the direct reference implementation: it casts an independent ray
// towards +X for every cell center in area. It must agree with FillScanline.
func FillRay(paths [][]Point, area Rect) (Fill, error) {
	es, err := collectEdges(paths)
	if err != nil {
		return Fill{}, err
	}
	var fill Fill
	for y := area.MinY; y <= area.MaxY; y++ {
		for x := area.MinX; x <= area.MaxX; x++ {
			p := Point{x, y}
			w, onEdge := classify(es, p)
			switch {
			case onEdge:
				fill.Boundary = append(fill.Boundary, p)
			case w != 0:
				fill.Inside = append(fill.Inside, p)
			}
		}
	}
	return fill, nil
}
