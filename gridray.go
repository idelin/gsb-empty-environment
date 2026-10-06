// Package gridray provides exact traversal of 2D grid cells by segments
// with rational endpoints, plus versioned voxel picking over
// asynchronously arriving grid chunks.
package gridray

import (
	"math/big"
	"sort"
)

// Vec is a 2D point with exact rational coordinates.
type Vec struct {
	X, Y *big.Rat
}

// V returns the integer point (x, y).
func V(x, y int64) Vec {
	return Vec{big.NewRat(x, 1), big.NewRat(y, 1)}
}

// R returns the rational point (xn/xd, yn/yd).
func R(xn, xd, yn, yd int64) Vec {
	return Vec{big.NewRat(xn, xd), big.NewRat(yn, yd)}
}

func cloneVec(p Vec) Vec {
	return Vec{new(big.Rat).Set(p.X), new(big.Rat).Set(p.Y)}
}

// Cell identifies the closed unit grid cell [X, X+1] x [Y, Y+1].
// Coordinates may be negative.
type Cell struct {
	X, Y int
}

// CellHit is a cell touched by a segment, together with the first
// parameter t in [0, 1] at which the segment makes contact with it.
type CellHit struct {
	Cell Cell
	T    *big.Rat
}

// SegmentCells lists every closed grid cell touched by the closed segment
// from p0 to p1. A segment passing exactly through a grid vertex touches
// all four adjacent cells; a segment running exactly along a grid line
// touches the cells on both sides. Results are sorted by first-contact
// parameter, then by cell coordinates, and contain no duplicates.
func SegmentCells(p0, p1 Vec) []CellHit {
	p0, p1 = cloneVec(p0), cloneVec(p1)
	dx := new(big.Rat).Sub(p1.X, p0.X)
	dy := new(big.Rat).Sub(p1.Y, p0.Y)

	// Event parameters in [0,1] where the segment crosses a grid line.
	events := map[string]*big.Rat{}
	addEvent := func(t *big.Rat) { events[t.RatString()] = t }
	addEvent(big.NewRat(0, 1))
	addEvent(big.NewRat(1, 1))
	collect := func(a0, d *big.Rat) {
		if d.Sign() == 0 {
			return
		}
		lo := new(big.Rat).Set(a0)
		hi := new(big.Rat).Add(a0, d)
		if lo.Cmp(hi) > 0 {
			lo, hi = hi, lo
		}
		for k := floorRat(lo) + 1; ; k++ {
			kr := big.NewRat(int64(k), 1)
			if kr.Cmp(hi) >= 0 {
				break
			}
			t := new(big.Rat).Sub(kr, a0)
			t.Quo(t, d)
			addEvent(t)
		}
	}
	collect(p0.X, dx)
	collect(p0.Y, dy)

	ts := make([]*big.Rat, 0, len(events))
	for _, t := range events {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Cmp(ts[j]) < 0 })

	at := func(t *big.Rat) (x, y *big.Rat) {
		x = new(big.Rat).Mul(t, dx)
		x.Add(x, p0.X)
		y = new(big.Rat).Mul(t, dy)
		y.Add(y, p0.Y)
		return x, y
	}

	first := map[Cell]*big.Rat{}
	touch := func(t, x, y *big.Rat) {
		for _, c := range cellsAt(x, y) {
			if old, ok := first[c]; !ok || t.Cmp(old) < 0 {
				first[c] = t
			}
		}
	}

	// Cells containing each event point (vertices touch 4 cells,
	// edge points touch 2, plain points touch 1).
	for _, t := range ts {
		x, y := at(t)
		touch(t, x, y)
	}
	// Cells containing the interior of each event interval. A midpoint
	// lies on a grid line only when the segment runs along it, in which
	// case cells on both sides are touched.
	for i := 0; i+1 < len(ts); i++ {
		tm := new(big.Rat).Add(ts[i], ts[i+1])
		tm.Quo(tm, big.NewRat(2, 1))
		x, y := at(tm)
		touch(ts[i], x, y)
	}

	hits := make([]CellHit, 0, len(first))
	for c, t := range first {
		hits = append(hits, CellHit{Cell: c, T: t})
	}
	sort.Slice(hits, func(i, j int) bool {
		if c := hits[i].T.Cmp(hits[j].T); c != 0 {
			return c < 0
		}
		if hits[i].Cell.X != hits[j].Cell.X {
			return hits[i].Cell.X < hits[j].Cell.X
		}
		return hits[i].Cell.Y < hits[j].Cell.Y
	})
	return hits
}

// cellsAt returns the closed cells containing the point (x, y): 4 when the
// point is a grid vertex, 2 when it lies on a grid line, otherwise 1.
func cellsAt(x, y *big.Rat) []Cell {
	xs := axisCells(x)
	ys := axisCells(y)
	out := make([]Cell, 0, len(xs)*len(ys))
	for _, cx := range xs {
		for _, cy := range ys {
			out = append(out, Cell{cx, cy})
		}
	}
	return out
}

func axisCells(v *big.Rat) []int {
	f := floorRat(v)
	if isInt(v) {
		return []int{f - 1, f}
	}
	return []int{f}
}

func isInt(r *big.Rat) bool {
	return r.Denom().Cmp(big.NewInt(1)) == 0
}

// floorRat returns floor(r) as an int, correct for negative values.
func floorRat(r *big.Rat) int {
	q := new(big.Int).Quo(r.Num(), r.Denom()) // truncated toward zero
	if r.Sign() < 0 && new(big.Int).Rem(r.Num(), r.Denom()).Sign() != 0 {
		q.Sub(q, big.NewInt(1))
	}
	return int(q.Int64())
}
