package gridfill

import (
	"math/rand"
	"testing"
)

func path(pts ...Point) []Point { return pts }

// rect returns a closed rectangular loop. ccw selects the orientation.
func rect(x0, y0, x1, y1 float64, ccw bool) []Point {
	if ccw {
		return path(
			Point{x0, y0}, Point{x1, y0}, Point{x1, y1}, Point{x0, y1}, Point{x0, y0},
		)
	}
	return path(
		Point{x0, y0}, Point{x0, y1}, Point{x1, y1}, Point{x1, y0}, Point{x0, y0},
	)
}

func mustSegs(t *testing.T, paths ...[]Point) []Segment {
	t.Helper()
	var all []Segment
	for _, p := range paths {
		segs, err := SegmentsFromPath(p)
		if err != nil {
			t.Fatalf("SegmentsFromPath: %v", err)
		}
		all = append(all, segs...)
	}
	return all
}

// assertGridsEqual fails unless both grids classify every center identically
// over their common bounds.
func assertGridsEqual(t *testing.T, segs []Segment, x0, y0, x1, y1 int) {
	t.Helper()
	batch := ScanlineFill(segs, x0, y0, x1, y1)
	direct := FillGridPointwise(segs, x0, y0, x1, y1)
	for j := y0; j < y1; j++ {
		for i := x0; i < x1; i++ {
			if batch.At(i, j) != direct.At(i, j) {
				t.Fatalf("mismatch at center (%.1f, %.1f): scanline=%v raycast=%v",
					float64(i)+0.5, float64(j)+0.5, batch.At(i, j), direct.At(i, j))
			}
		}
	}
}

func TestSquareFill(t *testing.T) {
	segs := mustSegs(t, rect(0, 0, 4, 4, true))
	g := ScanlineFill(segs, -2, -2, 6, 6)
	for j := -2; j < 6; j++ {
		for i := -2; i < 6; i++ {
			want := Outside
			if i >= 0 && i < 4 && j >= 0 && j < 4 {
				want = Inside
			}
			if g.At(i, j) != want {
				t.Errorf("At(%d, %d) = %v, want %v", i, j, g.At(i, j), want)
			}
		}
	}
	// Orientation must not matter for the non-zero rule.
	segsCW := mustSegs(t, rect(0, 0, 4, 4, false))
	gCW := ScanlineFill(segsCW, -2, -2, 6, 6)
	for j := -2; j < 6; j++ {
		for i := -2; i < 6; i++ {
			if g.At(i, j) != gCW.At(i, j) {
				t.Errorf("orientation changed result at (%d, %d)", i, j)
			}
		}
	}
}

func TestBoundaryCentersOnEdge(t *testing.T) {
	// Edges at half-integer coordinates pass through cell centers.
	segs := mustSegs(t, rect(0.5, 0.5, 4.5, 4.5, true))
	g := ScanlineFill(segs, -1, -1, 6, 6)
	for j := -1; j < 6; j++ {
		for i := -1; i < 6; i++ {
			want := Outside
			switch {
			case i >= 0 && i <= 4 && j >= 0 && j <= 4 &&
				(i == 0 || i == 4 || j == 0 || j == 4):
				want = Boundary
			case i >= 1 && i <= 3 && j >= 1 && j <= 3:
				want = Inside
			}
			if g.At(i, j) != want {
				t.Errorf("At(%d, %d) = %v, want %v", i, j, g.At(i, j), want)
			}
		}
	}
}

func TestBoundaryOnHorizontalAndVerticalEdges(t *testing.T) {
	// A plus-shaped pair of segments crossing at a cell center.
	segs := mustSegs(t,
		path(Point{-2.5, 0.5}, Point{2.5, 0.5}),
		path(Point{0.5, -2.5}, Point{0.5, 2.5}),
	)
	g := ScanlineFill(segs, -3, -3, 3, 3)
	for j := -3; j < 3; j++ {
		for i := -3; i < 3; i++ {
			onEdge := (j == 0 && float64(i)+0.5 >= -2.5 && float64(i)+0.5 <= 2.5) ||
				(i == 0 && float64(j)+0.5 >= -2.5 && float64(j)+0.5 <= 2.5)
			if onEdge && g.At(i, j) != Boundary {
				t.Errorf("At(%d, %d) = %v, want Boundary", i, j, g.At(i, j))
			}
			if !onEdge && g.At(i, j) == Boundary {
				t.Errorf("At(%d, %d) = Boundary, want non-boundary", i, j)
			}
		}
	}
	if g.At(0, 0) != Boundary {
		t.Errorf("crossing center = %v, want Boundary", g.At(0, 0))
	}
	assertGridsEqual(t, segs, -3, -3, 3, 3)
}

func TestSharedEndpointsCollinearSplit(t *testing.T) {
	// Same rectangle, but each edge is split at an extra collinear vertex.
	plain := mustSegs(t, rect(0, 0, 4, 4, true))
	split := mustSegs(t, path(
		Point{0, 0}, Point{2, 0}, Point{4, 0},
		Point{4, 2}, Point{4, 4},
		Point{2, 4}, Point{0, 4},
		Point{0, 2}, Point{0, 0},
	))
	gPlain := ScanlineFill(plain, -2, -2, 6, 6)
	gSplit := ScanlineFill(split, -2, -2, 6, 6)
	for j := -2; j < 6; j++ {
		for i := -2; i < 6; i++ {
			if gPlain.At(i, j) != gSplit.At(i, j) {
				t.Errorf("split edges changed result at (%d, %d): %v vs %v",
					i, j, gPlain.At(i, j), gSplit.At(i, j))
			}
		}
	}
	assertGridsEqual(t, split, -2, -2, 6, 6)
}

func TestReverseRepeatedEdgesCancel(t *testing.T) {
	// Rectangle with an out-and-back spike retraced in reverse: the spike
	// must not affect the fill.
	spike := mustSegs(t, path(
		Point{0, 0}, Point{4, 0}, Point{4, 4}, Point{0, 4}, Point{0, 0},
		Point{0, -3}, Point{0, 0}, // spike down and back up along x=0
		Point{-3, 0}, Point{0, 0}, // spike left and back right along y=0
	))
	plain := mustSegs(t, rect(0, 0, 4, 4, true))
	gSpike := ScanlineFill(spike, -5, -5, 6, 6)
	gPlain := ScanlineFill(plain, -5, -5, 6, 6)
	for j := -5; j < 6; j++ {
		for i := -5; i < 6; i++ {
			if gSpike.At(i, j) != gPlain.At(i, j) {
				t.Errorf("spike changed result at (%d, %d): %v vs %v",
					i, j, gSpike.At(i, j), gPlain.At(i, j))
			}
		}
	}
	assertGridsEqual(t, spike, -5, -5, 6, 6)

	// A ring traced twice in opposite directions cancels entirely.
	ring := mustSegs(t, rect(0, 0, 4, 4, true), rect(0, 0, 4, 4, false))
	gRing := ScanlineFill(ring, -2, -2, 6, 6)
	for j := -2; j < 6; j++ {
		for i := -2; i < 6; i++ {
			if gRing.At(i, j) != Outside {
				t.Errorf("cancelled ring at (%d, %d) = %v, want Outside", i, j, gRing.At(i, j))
			}
		}
	}
}

func TestNestedReversedRings(t *testing.T) {
	outer := rect(0, 0, 10, 10, true)

	// Inner ring reversed: it punches a hole.
	hole := mustSegs(t, outer, rect(3, 3, 7, 7, false))
	g := ScanlineFill(hole, -1, -1, 11, 11)
	for j := -1; j < 11; j++ {
		for i := -1; i < 11; i++ {
			want := Outside
			if i >= 0 && i < 10 && j >= 0 && j < 10 {
				want = Inside
			}
			if i >= 3 && i < 7 && j >= 3 && j < 7 {
				want = Outside
			}
			if g.At(i, j) != want {
				t.Errorf("hole: At(%d, %d) = %v, want %v", i, j, g.At(i, j), want)
			}
		}
	}
	assertGridsEqual(t, hole, -1, -1, 11, 11)

	// Inner ring same orientation: stays filled.
	solid := mustSegs(t, outer, rect(3, 3, 7, 7, true))
	gSolid := ScanlineFill(solid, -1, -1, 11, 11)
	for j := 0; j < 10; j++ {
		for i := 0; i < 10; i++ {
			if gSolid.At(i, j) != Inside {
				t.Errorf("solid: At(%d, %d) = %v, want Inside", i, j, gSolid.At(i, j))
			}
		}
	}

	// Inner ring traced once each way cancels; a third trace fills again.
	three := mustSegs(t, outer, rect(3, 3, 7, 7, false), rect(3, 3, 7, 7, false), rect(3, 3, 7, 7, true))
	gThree := ScanlineFill(three, -1, -1, 11, 11)
	for j := 3; j < 7; j++ {
		for i := 3; i < 7; i++ {
			if gThree.At(i, j) != Outside {
				t.Errorf("net-reversed ring: At(%d, %d) = %v, want Outside", i, j, gThree.At(i, j))
			}
		}
	}
}

func TestSelfIntersecting(t *testing.T) {
	// The vertical edge x=2 crosses the horizontal edge y=0.
	segs := mustSegs(t, path(
		Point{0, 0}, Point{4, 0}, Point{4, 4}, Point{2, 4},
		Point{2, -2}, Point{0, -2}, Point{0, 0},
	))
	g := ScanlineFill(segs, -3, -4, 6, 6)
	cases := []struct {
		i, j int
		want Class
	}{
		{2, 0, Inside},   // right lobe
		{3, 3, Inside},   // right lobe, top
		{0, 0, Outside},  // left of crossing edge, winding sums to 0
		{1, 3, Outside},  // top middle, winding sums to 0
		{0, -2, Inside},  // bottom-left lobe
		{1, -1, Inside},  // bottom-left lobe
		{2, -2, Outside}, // bottom right of x=2
		{-1, 0, Outside},
	}
	for _, c := range cases {
		if g.At(c.i, c.j) != c.want {
			t.Errorf("At(%d, %d) = %v, want %v", c.i, c.j, g.At(c.i, c.j), c.want)
		}
	}
	assertGridsEqual(t, segs, -3, -4, 6, 6)
}

func TestOpenPath(t *testing.T) {
	// Open paths still produce a well-defined winding classification.
	segs := mustSegs(t, path(
		Point{0, 0}, Point{4, 0}, Point{4, 4}, Point{0, 4},
	))
	assertGridsEqual(t, segs, -2, -2, 6, 6)
	if got := ClassifyPoint(segs, Point{2.5, 1.5}); got != Inside {
		t.Errorf("ClassifyPoint = %v, want Inside", got)
	}
}

func TestSegmentsFromPathErrors(t *testing.T) {
	if _, err := SegmentsFromPath(path(Point{0, 0})); err == nil {
		t.Error("expected error for single-point path")
	}
	if _, err := SegmentsFromPath(path(Point{0, 0}, Point{1, 1})); err == nil {
		t.Error("expected error for diagonal segment")
	}
	segs, err := SegmentsFromPath(path(Point{0, 0}, Point{0, 0}, Point{2, 0}))
	if err != nil || len(segs) != 1 {
		t.Errorf("zero-length segment handling: segs=%v err=%v", segs, err)
	}
}

// TestScanlineMatchesRaycast cross-checks the batch scanline fill against
// direct per-center ray casting on randomized paths, including collinear
// shared endpoints, repeated edges, self-intersections, and edges running
// through cell centers.
func TestScanlineMatchesRaycast(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		half := seed%2 == 0 // half-integer coordinates put edges through centers
		coord := func() float64 {
			v := float64(rng.Intn(13) - 6)
			if half {
				v += 0.5
			}
			return v
		}
		var paths [][]Point
		for p := 0; p < 1+rng.Intn(3); p++ {
			n := 2 + rng.Intn(12)
			pts := make([]Point, 0, n+3)
			cur := Point{coord(), coord()}
			pts = append(pts, cur)
			for k := 0; k < n; k++ {
				next := cur
				if rng.Intn(2) == 0 {
					next.X = coord()
				} else {
					next.Y = coord()
				}
				// Occasionally retrace the previous edge in reverse.
				if k > 0 && rng.Intn(6) == 0 {
					pts = append(pts, pts[len(pts)-2])
					cur = pts[len(pts)-1]
					continue
				}
				pts = append(pts, next)
				cur = next
			}
			// Sometimes close the loop with an axis-aligned detour.
			if rng.Intn(2) == 0 {
				start := pts[0]
				pts = append(pts, Point{cur.X, start.Y}, start)
			}
			paths = append(paths, pts)
		}
		segs := mustSegs(t, paths...)
		batch := ScanlineFill(segs, -8, -8, 8, 8)
		direct := FillGridPointwise(segs, -8, -8, 8, 8)
		for j := -8; j < 8; j++ {
			for i := -8; i < 8; i++ {
				if batch.At(i, j) != direct.At(i, j) {
					t.Fatalf("seed %d: mismatch at (%.1f, %.1f): scanline=%v raycast=%v",
						seed, float64(i)+0.5, float64(j)+0.5, batch.At(i, j), direct.At(i, j))
				}
			}
		}
	}
}
