package gridray

import (
	"math/big"
	"math/rand"
	"testing"
)

type wantVisit struct {
	x, y   int64
	tn, td int64
}

func checkVisits(t *testing.T, got []Visit, want []wantVisit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d visits, want %d\ngot:  %v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Cell.X != w.x || g.Cell.Y != w.y {
			t.Fatalf("visit %d: got cell (%d,%d), want (%d,%d)", i, g.Cell.X, g.Cell.Y, w.x, w.y)
		}
		if g.T.Cmp(big.NewRat(w.tn, w.td)) != 0 {
			t.Fatalf("visit %d: got t=%s, want %d/%d", i, g.T, w.tn, w.td)
		}
	}
}

// Diagonal through grid points: at each lattice point all four adjacent
// cells must appear, deduplicated, in (t, x, y) order.
func TestTraverseDiagonalThroughCorners(t *testing.T) {
	got := TraverseSegment(Pt(0, 0), Pt(2, 2))
	checkVisits(t, got, []wantVisit{
		{-1, -1, 0, 1}, {-1, 0, 0, 1}, {0, -1, 0, 1}, {0, 0, 0, 1},
		{0, 1, 1, 2}, {1, 0, 1, 2}, {1, 1, 1, 2},
		{1, 2, 1, 1}, {2, 1, 1, 1}, {2, 2, 1, 1},
	})
}

// Segment walking exactly along a horizontal grid line: both the cells
// above and below the line are touched for the whole walk.
func TestTraverseAlongGridLine(t *testing.T) {
	got := TraverseSegment(Pt(0, 0), Pt(3, 0))
	checkVisits(t, got, []wantVisit{
		{-1, -1, 0, 1}, {-1, 0, 0, 1}, {0, -1, 0, 1}, {0, 0, 0, 1},
		{1, -1, 1, 3}, {1, 0, 1, 3},
		{2, -1, 2, 3}, {2, 0, 2, 3},
		{3, -1, 1, 1}, {3, 0, 1, 1},
	})
}

// Negative coordinates, crossing both axes directions.
func TestTraverseNegative(t *testing.T) {
	got := TraverseSegment(PtFrac(-5, 2, -1, 2), PtFrac(-1, 2, -7, 2))
	checkVisits(t, got, []wantVisit{
		{-3, -1, 0, 1},
		{-3, -2, 1, 6},
		{-2, -2, 1, 4},
		{-2, -3, 1, 2},
		{-1, -3, 3, 4},
		{-1, -4, 5, 6},
	})
}

// Zero-length segments: interior point touches one cell, a point on a
// grid line touches two, a grid point touches four.
func TestTraverseZeroLength(t *testing.T) {
	checkVisits(t, TraverseSegment(PtFrac(1, 2, 3, 2), PtFrac(1, 2, 3, 2)),
		[]wantVisit{{0, 1, 0, 1}})

	checkVisits(t, TraverseSegment(Pt(2, 1), Pt(2, 1)),
		[]wantVisit{{1, 0, 0, 1}, {1, 1, 0, 1}, {2, 0, 0, 1}, {2, 1, 0, 1}})

	// On a horizontal grid line only: two cells.
	checkVisits(t, TraverseSegment(PtFrac(-3, 2, 4, 1), PtFrac(-3, 2, 4, 1)),
		[]wantVisit{{-2, 3, 0, 1}, {-2, 4, 0, 1}})

	// Negative grid point: four cells.
	checkVisits(t, TraverseSegment(Pt(-3, -2), Pt(-3, -2)),
		[]wantVisit{{-4, -3, 0, 1}, {-4, -2, 0, 1}, {-3, -3, 0, 1}, {-3, -2, 0, 1}})
}

// Endpoint exactly on a grid point with a fractional direction.
func TestTraverseFractional(t *testing.T) {
	got := TraverseSegment(PtFrac(1, 3, 7, 2), PtFrac(10, 3, 1, 2))
	checkVisits(t, got, []wantVisit{
		{0, 3, 0, 1},
		{0, 2, 1, 6},
		{1, 2, 2, 9},
		{1, 1, 1, 2},
		{2, 1, 5, 9},
		{2, 0, 5, 6},
		{3, 0, 8, 9},
	})
}

// Random rational segments cross-checked against per-cell clipping,
// covering negative coordinates, grid-line walks and zero-length rays.
func TestTraverseAgainstClipping(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	rat := func() *big.Rat {
		return big.NewRat(int64(rng.Intn(17)-8), int64(1+rng.Intn(4)))
	}
	for i := 0; i < 2000; i++ {
		p0 := Point{rat(), rat()}
		p1 := Point{rat(), rat()}
		switch i % 7 {
		case 1: // zero-length
			p1 = p0
		case 2: // walk along a horizontal grid line
			y := big.NewRat(int64(rng.Intn(9)-4), 1)
			p0.Y, p1.Y = y, y
		case 3: // walk along a vertical grid line
			x := big.NewRat(int64(rng.Intn(9)-4), 1)
			p0.X, p1.X = x, x
		case 4: // integer endpoints (corner-heavy)
			p0 = Pt(int64(rng.Intn(9)-4), int64(rng.Intn(9)-4))
			p1 = Pt(int64(rng.Intn(9)-4), int64(rng.Intn(9)-4))
		}
		assertMatchesClip(t, p0, p1)
	}
}

func assertMatchesClip(t *testing.T, p0, p1 Point) {
	t.Helper()
	got := TraverseSegment(p0, p1)
	want := TraverseByClipping(p0, p1)
	if len(got) != len(want) {
		t.Fatalf("segment %v -> %v: traversal has %d cells, clipping has %d\ntrav: %v\nclip: %v",
			p0, p1, len(got), len(want), got, want)
	}
	seen := map[Cell]bool{}
	prevT := big.NewRat(-1, 1)
	for i := range got {
		g, w := got[i], want[i]
		if g.Cell != w.Cell || g.T.Cmp(w.T) != 0 {
			t.Fatalf("segment %v -> %v visit %d: traversal=(%v,%s) clipping=(%v,%s)",
				p0, p1, i, g.Cell, g.T, w.Cell, w.T)
		}
		if seen[g.Cell] {
			t.Fatalf("segment %v -> %v: duplicate cell %v", p0, p1, g.Cell)
		}
		seen[g.Cell] = true
		if g.T.Cmp(prevT) < 0 {
			t.Fatalf("segment %v -> %v: t not nondecreasing at %v", p0, p1, g.Cell)
		}
		prevT = g.T
	}
}
