package edtgrid

import (
	"math/rand"
	"reflect"
	"testing"
)

// mask builds a row-major value slice from strings of '0'/'1'.
func mask(rows ...string) (int, int, []uint8) {
	h := len(rows)
	w := len(rows[0])
	vals := make([]uint8, 0, w*h)
	for _, r := range rows {
		if len(r) != w {
			panic("ragged mask")
		}
		for i := 0; i < len(r); i++ {
			switch r[i] {
			case '0':
				vals = append(vals, 0)
			case '1':
				vals = append(vals, 1)
			default:
				panic("bad mask char")
			}
		}
	}
	return h, w, vals
}

func mustGrid(t *testing.T, rows ...string) *Grid {
	t.Helper()
	h, w, vals := mask(rows...)
	g, err := FromValues(h, w, vals)
	if err != nil {
		t.Fatalf("FromValues: %v", err)
	}
	return g
}

func assertCell(t *testing.T, g *Grid, row, col int, has bool, dist int64, src Point) {
	t.Helper()
	got := g.At(row, col)
	if got.HasTarget != has {
		t.Fatalf("At(%d,%d).HasTarget = %v, want %v", row, col, got.HasTarget, has)
	}
	if !has {
		if got.Dist != NoTargetDist {
			t.Fatalf("At(%d,%d).Dist = %d, want no-target marker %d", row, col, got.Dist, NoTargetDist)
		}
		return
	}
	if got.Dist != dist || got.Source != src {
		t.Fatalf("At(%d,%d) = {dist:%d src:%v}, want {dist:%d src:%v}",
			row, col, got.Dist, got.Source, dist, src)
	}
}

// sameDistance reports whether two cells agree on the distance-transform
// result (target presence, squared distance, nearest point). The mask value
// itself is deliberately excluded: a flip always changes values inside the
// edit box, but the reported change set covers only distance/nearest-point
// changes.
func sameDistance(a, b Cell) bool {
	if a.HasTarget != b.HasTarget {
		return false
	}
	if !a.HasTarget {
		return true
	}
	return a.Dist == b.Dist && a.Source == b.Source
}

// assertVsReference checks every pixel of g against the all-pairs reference.
func assertVsReference(t *testing.T, g *Grid, vals []uint8) {
	t.Helper()
	ref := ReferenceEDT(g.Rows(), g.Cols(), vals)
	for r := 0; r < g.Rows(); r++ {
		for c := 0; c < g.Cols(); c++ {
			got := g.At(r, c)
			want := ref[r*g.Cols()+c]
			if got != want {
				t.Fatalf("pixel (%d,%d): engine %+v != reference %+v", r, c, got, want)
			}
		}
	}
}

func TestSinglePixelGrid(t *testing.T) {
	g := mustGrid(t, "0")
	assertCell(t, g, 0, 0, false, NoTargetDist, Point{})
	g1 := mustGrid(t, "1")
	assertCell(t, g1, 0, 0, false, NoTargetDist, Point{})
}

func TestUniformGridNoTarget(t *testing.T) {
	for _, fill := range []uint8{0, 1} {
		g, err := New(4, 6, fill)
		if err != nil {
			t.Fatal(err)
		}
		for r := 0; r < 4; r++ {
			for c := 0; c < 6; c++ {
				assertCell(t, g, r, c, false, NoTargetDist, Point{})
			}
		}
	}
}

func TestSingleOppositePixel(t *testing.T) {
	// Sea of 0 with a single 1 at (2,3).
	g := mustGrid(t,
		"000000",
		"000000",
		"000100",
		"000000",
		"000000",
	)
	// Corner (0,0): squared distance 2^2+3^2 = 13 to the unique 1.
	assertCell(t, g, 0, 0, true, 13, Point{Row: 2, Col: 3})
	// The 1-pixel itself: nearest 0 is one step away; the four neighbors
	// tie at distance 1, row-then-column picks (1,3).
	assertCell(t, g, 2, 3, true, 1, Point{Row: 1, Col: 3})
	// Adjacent 0-pixel sees the 1 at distance 1.
	assertCell(t, g, 2, 4, true, 1, Point{Row: 2, Col: 3})
	_, _, vals := mask(
		"000000",
		"000000",
		"000100",
		"000000",
		"000000",
	)
	assertVsReference(t, g, vals)
}

func TestCheckerboard(t *testing.T) {
	g := mustGrid(t,
		"01010",
		"10101",
		"01010",
		"10101",
	)
	// Every pixel has opposite 4-neighbors at distance 1; the source is the
	// row-then-column smallest one.
	assertCell(t, g, 1, 1, true, 1, Point{Row: 0, Col: 1})
	assertCell(t, g, 2, 2, true, 1, Point{Row: 1, Col: 2})
	assertCell(t, g, 0, 0, true, 1, Point{Row: 0, Col: 1})
	assertCell(t, g, 3, 4, true, 1, Point{Row: 2, Col: 4})
	_, _, vals := mask(
		"01010",
		"10101",
		"01010",
		"10101",
	)
	assertVsReference(t, g, vals)
}

func TestTwoEquidistantSeeds(t *testing.T) {
	// 1x5 row with 1s at both ends: the middle pixel is equidistant.
	g := mustGrid(t, "10001")
	assertCell(t, g, 0, 2, true, 4, Point{Row: 0, Col: 0}) // tie -> smaller col
	assertCell(t, g, 0, 1, true, 1, Point{Row: 0, Col: 0})
	assertCell(t, g, 0, 3, true, 1, Point{Row: 0, Col: 4})
	assertCell(t, g, 0, 0, true, 1, Point{Row: 0, Col: 1})

	// 2D: seeds at (0,0) and (2,4); pixel (1,2) is equidistant (dist^2 5).
	g2 := mustGrid(t,
		"10000",
		"00000",
		"00001",
	)
	assertCell(t, g2, 1, 2, true, 5, Point{Row: 0, Col: 0}) // tie -> smaller row
	assertCell(t, g2, 1, 1, true, 2, Point{Row: 0, Col: 0})
	assertCell(t, g2, 2, 4, true, 1, Point{Row: 1, Col: 4})
	_, _, vals2 := mask(
		"10000",
		"00000",
		"00001",
	)
	assertVsReference(t, g2, vals2)
}

func TestDeleteUniqueOppositePixel(t *testing.T) {
	g0, err := New(5, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Add the unique 1 at (2,2).
	g1, d1 := g0.Flip(Rect{R0: 2, C0: 2, R1: 3, C1: 3})
	if d1.NoOp || !d1.FullRecompute || d1.ExaminedPixels != 25 {
		t.Fatalf("unexpected diff: %+v", d1)
	}
	// Every pixel gains a target: all 25 changed.
	if len(d1.Changed) != 25 {
		t.Fatalf("got %d changed pixels, want 25", len(d1.Changed))
	}
	assertCell(t, g1, 0, 0, true, 8, Point{Row: 2, Col: 2})
	assertCell(t, g1, 2, 2, true, 1, Point{Row: 1, Col: 2})

	// Delete the unique opposite pixel again: the whole grid falls back to
	// the no-target marker, so the change set is the entire image even
	// though the edit box is a single pixel.
	g2, d2 := g1.Flip(Rect{R0: 2, C0: 2, R1: 3, C1: 3})
	if d2.ExaminedPixels != 25 || !d2.FullRecompute {
		t.Fatalf("unexpected diff: %+v", d2)
	}
	if len(d2.Changed) != 25 {
		t.Fatalf("deletion changed %d pixels, want all 25", len(d2.Changed))
	}
	for r := 0; r < 5; r++ {
		for c := 0; c < 5; c++ {
			assertCell(t, g2, r, c, false, NoTargetDist, Point{})
		}
	}

	// Old snapshots still answer with their old distances.
	assertCell(t, g1, 0, 0, true, 8, Point{Row: 2, Col: 2})
	assertCell(t, g0, 0, 0, false, NoTargetDist, Point{})
}

func TestNoOpFlip(t *testing.T) {
	g := mustGrid(t,
		"010",
		"101",
	)
	// Empty rectangle.
	g2, d := g.Flip(Rect{R0: 1, C0: 1, R1: 1, C1: 2})
	if !d.NoOp || d.ExaminedPixels != 0 || len(d.Changed) != 0 || g2 != g {
		t.Fatalf("empty flip not a no-op: %+v", d)
	}
	// Rectangle fully outside the grid.
	g3, d3 := g.Flip(Rect{R0: 10, C0: 10, R1: 20, C1: 20})
	if !d3.NoOp || d3.ExaminedPixels != 0 || g3 != g {
		t.Fatalf("out-of-range flip not a no-op: %+v", d3)
	}
}

// allRects enumerates every non-empty half-open rectangle of an hxw grid.
func allRects(h, w int) []Rect {
	var out []Rect
	for r0 := 0; r0 < h; r0++ {
		for r1 := r0 + 1; r1 <= h; r1++ {
			for c0 := 0; c0 < w; c0++ {
				for c1 := c0 + 1; c1 <= w; c1++ {
					out = append(out, Rect{R0: r0, C0: c0, R1: r1, C1: c1})
				}
			}
		}
	}
	return out
}

func flippedCopy(vals []uint8, cols int, r Rect) []uint8 {
	cp := make([]uint8, len(vals))
	copy(cp, vals)
	for row := r.R0; row < r.R1; row++ {
		for col := r.C0; col < r.C1; col++ {
			i := row*cols + col
			cp[i] = 1 - cp[i]
		}
	}
	return cp
}

// checkFlip verifies one flip against the reference: the new snapshot must
// match the all-pairs ground truth, and the reported change set must equal
// the exact pixel-wise difference of the two snapshots.
func checkFlip(t *testing.T, g *Grid, vals []uint8, r Rect) {
	t.Helper()
	ng, d := g.Flip(r)
	if d.NoOp {
		t.Fatalf("non-empty rect reported no-op")
	}
	if !d.FullRecompute || d.ExaminedPixels != g.Rows()*g.Cols() {
		t.Fatalf("examined = %d, want full %d", d.ExaminedPixels, g.Rows()*g.Cols())
	}
	newVals := flippedCopy(vals, g.Cols(), r)
	assertVsReference(t, ng, newVals)

	wantChanged := make([]Point, 0)
	refNew := ReferenceEDT(g.Rows(), g.Cols(), newVals)
	for i := range vals {
		old := g.At(i/g.Cols(), i%g.Cols())
		if !sameDistance(old, refNew[i]) {
			wantChanged = append(wantChanged, Point{Row: i / g.Cols(), Col: i % g.Cols()})
		}
	}
	if !reflect.DeepEqual(d.Changed, wantChanged) {
		t.Fatalf("change set mismatch:\n got %v\nwant %v", d.Changed, wantChanged)
	}
}

// TestEnumerateSmallGrids exhaustively enumerates every 2x2, 2x3 and 3x3
// mask together with every rectangular flip, checking the engine and the
// reported change sets against the all-pairs reference.
func TestEnumerateSmallGrids(t *testing.T) {
	for _, dim := range [][2]int{{2, 2}, {2, 3}, {3, 2}, {3, 3}} {
		h, w := dim[0], dim[1]
		n := h * w
		rects := allRects(h, w)
		for bits := 0; bits < 1<<uint(n); bits++ {
			vals := make([]uint8, n)
			for i := 0; i < n; i++ {
				vals[i] = uint8((bits >> uint(i)) & 1)
			}
			g, err := FromValues(h, w, vals)
			if err != nil {
				t.Fatal(err)
			}
			// Static transform must match the reference too.
			assertVsReference(t, g, vals)
			for _, r := range rects {
				checkFlip(t, g, vals, r)
			}
		}
	}
}

// TestRandomFlipsVsReference fuzzes longer flip sequences on larger grids,
// including snapshot queries of older versions.
func TestRandomFlipsVsReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 30; trial++ {
		h := 2 + rng.Intn(6)
		w := 2 + rng.Intn(6)
		vals := make([]uint8, h*w)
		for i := range vals {
			vals[i] = uint8(rng.Intn(2))
		}
		g, err := FromValues(h, w, vals)
		if err != nil {
			t.Fatal(err)
		}
		snaps := []*Grid{g}
		snapVals := [][]uint8{append([]uint8(nil), vals...)}
		for step := 0; step < 12; step++ {
			r0, r1 := rng.Intn(h), rng.Intn(h)
			c0, c1 := rng.Intn(w), rng.Intn(w)
			if r0 > r1 {
				r0, r1 = r1, r0
			}
			if c0 > c1 {
				c0, c1 = c1, c0
			}
			r1++
			c1++
			r := Rect{R0: r0, C0: c0, R1: r1, C1: c1}
			ng, d := g.Flip(r)
			vals = flippedCopy(vals, w, r)
			assertVsReference(t, ng, vals)

			// Change set must equal the exact snapshot difference.
			refNew := ReferenceEDT(h, w, vals)
			want := 0
			for i := range vals {
				if !sameDistance(g.At(i/w, i%w), refNew[i]) {
					want++
				}
			}
			if len(d.Changed) != want {
				t.Fatalf("trial %d step %d: %d changed, want %d", trial, step, len(d.Changed), want)
			}

			g = ng
			snaps = append(snaps, g)
			snapVals = append(snapVals, append([]uint8(nil), vals...))
		}
		// Every old snapshot must still match its own ground truth.
		for v, snap := range snaps {
			assertVsReference(t, snap, snapVals[v])
		}
	}
}

// TestLargerGridsVsReference spot-checks bigger random masks where the
// all-pairs reference is still affordable.
func TestLargerGridsVsReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, dim := range [][2]int{{8, 8}, {13, 9}, {20, 17}, {31, 5}, {1, 40}, {40, 1}} {
		h, w := dim[0], dim[1]
		for trial := 0; trial < 5; trial++ {
			vals := make([]uint8, h*w)
			for i := range vals {
				vals[i] = uint8(rng.Intn(2))
			}
			// Bias some trials toward sparse/dense masks.
			if trial == 0 {
				for i := range vals {
					if rng.Intn(10) > 0 {
						vals[i] = 0
					}
				}
			}
			g, err := FromValues(h, w, vals)
			if err != nil {
				t.Fatal(err)
			}
			assertVsReference(t, g, vals)
		}
	}
}
