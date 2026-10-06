package gridray

import (
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

// clipCells is an independent cross-check implementation: it clips the
// segment against every candidate closed cell (Liang-Barsky in exact
// rational arithmetic) and keeps the cells with a non-empty intersection,
// recording the entry parameter.
func clipCells(p0, p1 Vec) []CellHit {
	dx := new(big.Rat).Sub(p1.X, p0.X)
	dy := new(big.Rat).Sub(p1.Y, p0.Y)

	minCX := min(floorRat(p0.X), floorRat(p1.X)) - 1
	maxCX := max(floorRat(p0.X), floorRat(p1.X)) + 1
	minCY := min(floorRat(p0.Y), floorRat(p1.Y)) - 1
	maxCY := max(floorRat(p0.Y), floorRat(p1.Y)) + 1

	var hits []CellHit
	for cx := minCX; cx <= maxCX; cx++ {
		for cy := minCY; cy <= maxCY; cy++ {
			t0, ok := clipToCell(p0.X, p0.Y, dx, dy, cx, cy)
			if ok {
				hits = append(hits, CellHit{Cell: Cell{cx, cy}, T: t0})
			}
		}
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

// clipToCell intersects the segment p(t) = (x0,y0) + t*(dx,dy), t in
// [0,1], with the closed cell [cx,cx+1] x [cy,cy+1]. On success it
// returns the smallest parameter at which the segment is inside the cell.
func clipToCell(x0, y0, dx, dy *big.Rat, cx, cy int) (*big.Rat, bool) {
	t0 := big.NewRat(0, 1)
	t1 := big.NewRat(1, 1)
	var ok bool
	if t0, t1, ok = clipAxis(x0, dx, int64(cx), t0, t1); !ok {
		return nil, false
	}
	if t0, t1, ok = clipAxis(y0, dy, int64(cy), t0, t1); !ok {
		return nil, false
	}
	return t0, true
}

func clipAxis(s, d *big.Rat, lo int64, t0, t1 *big.Rat) (*big.Rat, *big.Rat, bool) {
	loR := big.NewRat(lo, 1)
	hiR := big.NewRat(lo+1, 1)
	if d.Sign() == 0 {
		if s.Cmp(loR) < 0 || s.Cmp(hiR) > 0 {
			return nil, nil, false
		}
		return t0, t1, true
	}
	r1 := new(big.Rat).Sub(loR, s)
	r1.Quo(r1, d)
	r2 := new(big.Rat).Sub(hiR, s)
	r2.Quo(r2, d)
	if r1.Cmp(r2) > 0 {
		r1, r2 = r2, r1
	}
	if r1.Cmp(t0) > 0 {
		t0 = new(big.Rat).Set(r1)
	}
	if r2.Cmp(t1) < 0 {
		t1 = new(big.Rat).Set(r2)
	}
	if t0.Cmp(t1) > 0 {
		return nil, nil, false
	}
	return t0, t1, true
}

func assertSameHits(t *testing.T, p0, p1 Vec) {
	t.Helper()
	got := SegmentCells(p0, p1)
	want := clipCells(p0, p1)
	if len(got) != len(want) {
		t.Fatalf("segment %v -> %v: got %d cells %v, clipping wants %d cells %v",
			p0, p1, len(got), got, len(want), want)
	}
	for i := range got {
		if got[i].Cell != want[i].Cell || got[i].T.Cmp(want[i].T) != 0 {
			t.Fatalf("segment %v -> %v: mismatch at %d: got %v, clipping wants %v\ngot:  %v\nwant: %v",
				p0, p1, i, got[i], want[i], got, want)
		}
	}
	// No duplicates, sorted by (T, X, Y).
	for i := 1; i < len(got); i++ {
		if got[i].Cell == got[i-1].Cell {
			t.Fatalf("duplicate cell %v", got[i].Cell)
		}
		c := got[i-1].T.Cmp(got[i].T)
		if c > 0 || (c == 0 && (got[i-1].Cell.X > got[i].Cell.X ||
			(got[i-1].Cell.X == got[i].Cell.X && got[i-1].Cell.Y > got[i].Cell.Y))) {
			t.Fatalf("result not sorted at %d: %v then %v", i, got[i-1], got[i])
		}
	}
}

func TestSegmentCellsAgainstClipping(t *testing.T) {
	cases := [][2]Vec{
		// Vertex-to-vertex diagonal through negative coordinates.
		{V(-2, -2), V(2, 2)},
		// Horizontal walk exactly along the grid line y = -1.
		{V(-3, -1), V(2, -1)},
		// Vertical walk exactly along the grid line x = 0.
		{V(0, -1), V(0, 3)},
		// Zero-length rays: interior, on a grid line, on a vertex.
		{R(1, 2, 1, 2), R(1, 2, 1, 2)},
		{V(2, -3), V(2, -3)},
		{V(-1, -1), V(-1, -1)},
		// Rational endpoints with negative coordinates.
		{R(-7, 3, 5, 4), R(11, 6, -9, 2)},
		{R(1, 8, -1, 8), R(-33, 8, 15, 8)},
		// Axis-aligned but off the grid lines.
		{R(-5, 2, 3, 1), R(7, 2, 3, 1)},
		// Single point exactly on a grid line crossing.
		{V(0, 0), R(1, 4, 1, 4)},
	}
	for _, c := range cases {
		assertSameHits(t, c[0], c[1])
		assertSameHits(t, c[1], c[0]) // reversed
	}
}

func TestSegmentCellsRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	denoms := []int64{1, 1, 1, 2, 3, 5, 8}
	rat := func() *big.Rat {
		return big.NewRat(int64(rng.Intn(81)-40), denoms[rng.Intn(len(denoms))])
	}
	for i := 0; i < 2000; i++ {
		p0 := Vec{rat(), rat()}
		p1 := Vec{rat(), rat()}
		assertSameHits(t, p0, p1)
	}
}

func TestVertexHitTouchesFourCells(t *testing.T) {
	hits := SegmentCells(V(0, 0), V(2, 2))
	want := []CellHit{
		{Cell: Cell{-1, -1}, T: big.NewRat(0, 1)},
		{Cell: Cell{-1, 0}, T: big.NewRat(0, 1)},
		{Cell: Cell{0, -1}, T: big.NewRat(0, 1)},
		{Cell: Cell{0, 0}, T: big.NewRat(0, 1)},
		{Cell: Cell{0, 1}, T: big.NewRat(1, 2)},
		{Cell: Cell{1, 0}, T: big.NewRat(1, 2)},
		{Cell: Cell{1, 1}, T: big.NewRat(1, 2)},
		{Cell: Cell{1, 2}, T: big.NewRat(1, 1)},
		{Cell: Cell{2, 1}, T: big.NewRat(1, 1)},
		{Cell: Cell{2, 2}, T: big.NewRat(1, 1)},
	}
	if len(hits) != len(want) {
		t.Fatalf("got %v, want %v", hits, want)
	}
	for i := range want {
		if hits[i].Cell != want[i].Cell || hits[i].T.Cmp(want[i].T) != 0 {
			t.Fatalf("at %d: got %v, want %v", i, hits[i], want[i])
		}
	}
}

func TestGridLineWalkTouchesBothSides(t *testing.T) {
	hits := SegmentCells(V(0, 0), V(0, 3))
	if len(hits) != 10 { // x in {-1,0} x y in {-1..3}
		t.Fatalf("got %d cells: %v", len(hits), hits)
	}
	for _, h := range hits {
		if h.Cell.X < -1 || h.Cell.X > 0 || h.Cell.Y < -1 || h.Cell.Y > 3 {
			t.Fatalf("unexpected cell %v", h.Cell)
		}
	}
}

func TestZeroLengthRay(t *testing.T) {
	// Interior point: exactly one cell.
	hits := SegmentCells(R(1, 2, 1, 2), R(1, 2, 1, 2))
	if len(hits) != 1 || hits[0].Cell != (Cell{0, 0}) || hits[0].T.Sign() != 0 {
		t.Fatalf("got %v", hits)
	}
	// Point on a vertical grid line: two cells.
	hits = SegmentCells(R(2, 1, 1, 2), R(2, 1, 1, 2))
	if len(hits) != 2 || hits[0].Cell != (Cell{1, 0}) || hits[1].Cell != (Cell{2, 0}) {
		t.Fatalf("got %v", hits)
	}
	// Point on a vertex: four cells.
	hits = SegmentCells(V(-1, -1), V(-1, -1))
	want := []Cell{{-2, -2}, {-2, -1}, {-1, -2}, {-1, -1}}
	if len(hits) != 4 {
		t.Fatalf("got %v", hits)
	}
	for i, c := range want {
		if hits[i].Cell != c || hits[i].T.Sign() != 0 {
			t.Fatalf("at %d: got %v, want cell %v at t=0", i, hits[i], c)
		}
	}
}
