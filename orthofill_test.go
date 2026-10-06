package orthofill

import (
	"math/rand"
	"reflect"
	"testing"
)

func pt(x, y int) Point { return Point{x, y} }

func pointSet(ps []Point) map[Point]bool {
	m := make(map[Point]bool, len(ps))
	for _, p := range ps {
		m[p] = true
	}
	return m
}

// squarePerimeter returns all integer points on the perimeter of the
// axis-aligned square with corners (lo,lo) and (hi,hi).
func squarePerimeter(lo, hi int) []Point {
	var ps []Point
	for x := lo; x <= hi; x++ {
		ps = append(ps, pt(x, lo), pt(x, hi))
	}
	for y := lo + 1; y < hi; y++ {
		ps = append(ps, pt(lo, y), pt(hi, y))
	}
	return ps
}

func checkFill(t *testing.T, f Fill, wantInside, wantBoundary []Point) {
	t.Helper()
	if !reflect.DeepEqual(pointSet(f.Inside), pointSet(wantInside)) {
		t.Errorf("inside mismatch:\n got %v\nwant %v", f.Inside, wantInside)
	}
	if !reflect.DeepEqual(pointSet(f.Boundary), pointSet(wantBoundary)) {
		t.Errorf("boundary mismatch:\n got %v\nwant %v", f.Boundary, wantBoundary)
	}
	if dup := len(f.Inside) + len(f.Boundary); dup != len(pointSet(append(f.Inside, f.Boundary...))) {
		t.Errorf("duplicate or overlapping points in result: %v", f)
	}
}

func TestSquareCCW(t *testing.T) {
	sq := [][]Point{{pt(0, 0), pt(4, 0), pt(4, 4), pt(0, 4)}}
	area := Rect{-1, -1, 5, 5}
	f, err := FillScanline(sq, area)
	if err != nil {
		t.Fatal(err)
	}
	var inside []Point
	for y := 1; y <= 3; y++ {
		for x := 1; x <= 3; x++ {
			inside = append(inside, pt(x, y))
		}
	}
	checkFill(t, f, inside, squarePerimeter(0, 4))

	w, on, err := Winding(sq, pt(2, 2))
	if err != nil {
		t.Fatal(err)
	}
	if w != 1 || on {
		t.Errorf("Winding((2,2)) = %d, %v; want 1, false", w, on)
	}
	if _, on, _ := Winding(sq, pt(4, 2)); !on {
		t.Error("Winding((4,2)) should report on-edge")
	}
}

func TestSquareCWSameFill(t *testing.T) {
	ccw := [][]Point{{pt(0, 0), pt(4, 0), pt(4, 4), pt(0, 4)}}
	cw := [][]Point{{pt(0, 0), pt(0, 4), pt(4, 4), pt(4, 0)}}
	area := Rect{-1, -1, 5, 5}
	a, err := FillScanline(ccw, area)
	if err != nil {
		t.Fatal(err)
	}
	b, err := FillScanline(cw, area)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("reversed orientation changed fill:\nccw %v\ncw %v", a, b)
	}
	w, _, _ := Winding(cw, pt(2, 2))
	if w != -1 {
		t.Errorf("Winding((2,2)) = %d; want -1", w)
	}
}

func TestNestedReverseLoopsMakeHole(t *testing.T) {
	paths := [][]Point{
		{pt(0, 0), pt(6, 0), pt(6, 6), pt(0, 6)}, // outer CCW
		{pt(2, 2), pt(2, 4), pt(4, 4), pt(4, 2)}, // inner CW
	}
	area := Rect{-1, -1, 7, 7}
	f, err := FillScanline(paths, area)
	if err != nil {
		t.Fatal(err)
	}
	var inside []Point
	for y := 1; y <= 5; y++ {
		for x := 1; x <= 5; x++ {
			if x >= 2 && x <= 4 && y >= 2 && y <= 4 {
				continue // hole interior and its edges
			}
			inside = append(inside, pt(x, y))
		}
	}
	boundary := append(squarePerimeter(0, 6), squarePerimeter(2, 4)...)
	checkFill(t, f, inside, boundary)

	w, _, _ := Winding(paths, pt(3, 3))
	if w != 0 {
		t.Errorf("Winding((3,3)) = %d; want 0 in the hole", w)
	}
}

func TestNestedSameDirectionLoopsAddUp(t *testing.T) {
	paths := [][]Point{
		{pt(0, 0), pt(6, 0), pt(6, 6), pt(0, 6)}, // outer CCW
		{pt(2, 2), pt(4, 2), pt(4, 4), pt(2, 4)}, // inner CCW
	}
	w, _, err := Winding(paths, pt(3, 3))
	if err != nil {
		t.Fatal(err)
	}
	if w != 2 {
		t.Errorf("Winding((3,3)) = %d; want 2", w)
	}
	f, err := FillScanline(paths, Rect{1, 1, 5, 5})
	if err != nil {
		t.Fatal(err)
	}
	// The ring has winding 1 and the hole interior winding 2: all inside.
	var inside []Point
	for y := 1; y <= 5; y++ {
		for x := 1; x <= 5; x++ {
			if x >= 2 && x <= 4 && y >= 2 && y <= 4 {
				continue // inner square edges are boundary
			}
			inside = append(inside, pt(x, y))
		}
	}
	inside = append(inside, pt(3, 3))
	checkFill(t, f, inside, squarePerimeter(2, 4))
}

func TestRetracedEdgesCancel(t *testing.T) {
	// Up and back down the right edge, then back along the bottom edge:
	// every vertical crossing cancels, so nothing is filled.
	paths := [][]Point{{pt(0, 0), pt(4, 0), pt(4, 4), pt(4, 0), pt(0, 0)}}
	f, err := FillScanline(paths, Rect{-1, -1, 5, 5})
	if err != nil {
		t.Fatal(err)
	}
	var boundary []Point
	for x := 0; x <= 4; x++ {
		boundary = append(boundary, pt(x, 0))
	}
	for y := 1; y <= 4; y++ {
		boundary = append(boundary, pt(4, y))
	}
	checkFill(t, f, nil, boundary)
}

func TestPartialRetraceSelfTouching(t *testing.T) {
	// The closing edge (3,0)->(0,0) retraces part of the bottom edge, and the
	// downward edge x=3 cancels the upward edge x=6 for centers left of x=3.
	paths := [][]Point{{pt(0, 0), pt(6, 0), pt(6, 4), pt(3, 4), pt(3, 0)}}
	f, err := FillScanline(paths, Rect{-1, -1, 7, 5})
	if err != nil {
		t.Fatal(err)
	}
	var inside []Point
	for y := 1; y <= 3; y++ {
		for x := 4; x <= 5; x++ {
			inside = append(inside, pt(x, y))
		}
	}
	var boundary []Point
	for x := 0; x <= 6; x++ {
		boundary = append(boundary, pt(x, 0))
	}
	for y := 1; y <= 4; y++ {
		boundary = append(boundary, pt(6, y), pt(3, y))
	}
	for x := 4; x <= 5; x++ {
		boundary = append(boundary, pt(x, 4))
	}
	checkFill(t, f, inside, boundary)
}

func TestSharedEdgeBetweenSubpaths(t *testing.T) {
	// Two adjacent CCW squares: the shared edge x=2 is traversed in opposite
	// directions and cancels, giving a uniformly filled 4x2 union.
	paths := [][]Point{
		{pt(0, 0), pt(2, 0), pt(2, 2), pt(0, 2)},
		{pt(2, 0), pt(4, 0), pt(4, 2), pt(2, 2)},
	}
	f, err := FillScanline(paths, Rect{-1, -1, 5, 3})
	if err != nil {
		t.Fatal(err)
	}
	inside := []Point{pt(1, 1), pt(3, 1)}
	boundary := append(squarePerimeter(0, 2), []Point{
		pt(2, 0), pt(3, 0), pt(4, 0),
		pt(2, 1), pt(4, 1),
		pt(2, 2), pt(3, 2), pt(4, 2),
	}...)
	checkFill(t, f, inside, boundary)

	w, _, _ := Winding(paths, pt(1, 1))
	if w != 1 {
		t.Errorf("Winding((1,1)) = %d; want 1", w)
	}
}

func TestSharedEndpointCornerTouch(t *testing.T) {
	// Two squares touching only at the corner (2,2); a ray through the shared
	// vertex must count exactly one crossing per square.
	paths := [][]Point{
		{pt(0, 0), pt(2, 0), pt(2, 2), pt(0, 2)},
		{pt(2, 2), pt(4, 2), pt(4, 4), pt(2, 4)},
	}
	f, err := FillScanline(paths, Rect{-1, -1, 5, 5})
	if err != nil {
		t.Fatal(err)
	}
	inside := []Point{pt(1, 1), pt(3, 3)}
	boundary := append(squarePerimeter(0, 2), squarePerimeter(2, 4)...)
	checkFill(t, f, inside, boundary)

	// (3,1) lies on the horizontal line through no edge; its ray passes below
	// the shared vertex. (1,3) casts a ray straight through vertex (2,2).
	for _, p := range []Point{pt(3, 1), pt(1, 3)} {
		w, on, _ := Winding(paths, p)
		if w != 0 || on {
			t.Errorf("Winding(%v) = %d, %v; want 0, false", p, w, on)
		}
	}
}

func TestDegenerateAndInvalid(t *testing.T) {
	if f, err := FillScanline(nil, Rect{0, 0, 2, 2}); err != nil || len(f.Inside) != 0 || len(f.Boundary) != 0 {
		t.Errorf("empty paths: got %v, %v", f, err)
	}
	// Zero-length segments are dropped.
	paths := [][]Point{{pt(0, 0), pt(0, 0), pt(2, 0), pt(2, 2), pt(0, 2)}}
	f, err := FillScanline(paths, Rect{0, 0, 2, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Inside) != 1 || f.Inside[0] != pt(1, 1) {
		t.Errorf("got inside %v; want [(1,1)]", f.Inside)
	}
	// Diagonal segment must be rejected (including a diagonal closing edge).
	for _, bad := range [][]Point{
		{pt(0, 0), pt(1, 1)},
		{pt(0, 0), pt(2, 0), pt(1, 1)},
	} {
		if _, err := FillScanline([][]Point{bad}, Rect{0, 0, 2, 2}); err == nil {
			t.Errorf("path %v: expected ErrNotOrthogonal", bad)
		}
	}
}

// randomClosedWalk returns a random axis-aligned closed walk starting and
// ending at the origin, with unit to length-3 steps.
func randomClosedWalk(r *rand.Rand, steps int) []Point {
	path := []Point{pt(0, 0)}
	x, y := 0, 0
	for i := 0; i < steps; i++ {
		n := 1 + r.Intn(3)
		switch r.Intn(4) {
		case 0:
			x += n
		case 1:
			x -= n
		case 2:
			y += n
		case 3:
			y -= n
		}
		path = append(path, pt(x, y))
	}
	if y != 0 {
		path = append(path, pt(x, 0))
	}
	if x != 0 {
		path = append(path, pt(0, 0))
	}
	return path
}

func reversed(path []Point) []Point {
	out := make([]Point, len(path))
	for i, p := range path {
		out[len(path)-1-i] = p
	}
	return out
}

func equalFill(a, b Fill) bool {
	return reflect.DeepEqual(a.Inside, b.Inside) && reflect.DeepEqual(a.Boundary, b.Boundary)
}

func TestScanlineMatchesDirectRay(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		var paths [][]Point
		for i, n := 0, 1+r.Intn(3); i < n; i++ {
			paths = append(paths, randomClosedWalk(r, r.Intn(24)))
		}
		area := Rect{-8, -8, 8, 8}
		batch, err := FillScanline(paths, area)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		direct, err := FillRay(paths, area)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !equalFill(batch, direct) {
			t.Fatalf("seed %d: scanline and direct ray disagree\npaths %v\nscanline %v\ndirect  %v",
				seed, paths, batch, direct)
		}
		// Every center must also agree with the per-point Winding query.
		inSet, bdSet := pointSet(batch.Inside), pointSet(batch.Boundary)
		for y := area.MinY; y <= area.MaxY; y++ {
			for x := area.MinX; x <= area.MaxX; x++ {
				p := pt(x, y)
				w, on, _ := Winding(paths, p)
				if on != bdSet[p] || (w != 0) != inSet[p] && !on {
					t.Fatalf("seed %d: point %v: winding %d onEdge %v, batch inside %v boundary %v",
						seed, p, w, on, inSet[p], bdSet[p])
				}
			}
		}
	}
}

func TestFillInvarianceProperties(t *testing.T) {
	for seed := int64(1000); seed < 1050; seed++ {
		r := rand.New(rand.NewSource(seed))
		paths := [][]Point{randomClosedWalk(r, 20), randomClosedWalk(r, 12)}
		area := Rect{-8, -8, 8, 8}
		base, err := FillScanline(paths, area)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}

		// Reversing every loop must not change a non-zero-rule fill.
		var rev [][]Point
		for _, p := range paths {
			rev = append(rev, reversed(p))
		}
		got, err := FillScanline(rev, area)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !equalFill(base, got) {
			t.Fatalf("seed %d: reversing all loops changed the fill", seed)
		}

		// Retracing the first loop in reverse cancels exactly that loop's
		// winding contribution. Its edges are still drawn, so centers on
		// them remain boundary even though the loop no longer fills.
		withRetrace := append(append([][]Point{}, paths...), reversed(paths[0]))
		got, err = FillScanline(withRetrace, area)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		rest, err := FillScanline(paths[1:], area)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		es0, err := collectEdges(paths[:1])
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		retracedBoundary := boundarySet(es0, area)
		var wantInside, wantBoundary []Point
		for _, p := range rest.Inside {
			if !retracedBoundary[p] {
				wantInside = append(wantInside, p)
			}
		}
		wantBoundary = append(wantBoundary, rest.Boundary...)
		for p := range retracedBoundary {
			wantBoundary = append(wantBoundary, p)
		}
		if !reflect.DeepEqual(pointSet(got.Inside), pointSet(wantInside)) ||
			!reflect.DeepEqual(pointSet(got.Boundary), pointSet(wantBoundary)) {
			t.Fatalf("seed %d: retraced reverse loop did not cancel\nwant inside %v boundary %v\ngot  %v",
				seed, wantInside, wantBoundary, got)
		}

		// A fresh loop plus its reverse leaves every winding number unchanged;
		// centers on the new edges become boundary but nothing flips in/out.
		extra := randomClosedWalk(r, 16)
		withCancel := append(append([][]Point{}, paths...), extra, reversed(extra))
		got, err = FillScanline(withCancel, area)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		baseBoundary := pointSet(base.Boundary)
		gotBoundary := pointSet(got.Boundary)
		for p := range baseBoundary {
			if !gotBoundary[p] {
				t.Fatalf("seed %d: boundary point %v lost after loop+reverse", seed, p)
			}
		}
		for y := area.MinY; y <= area.MaxY; y++ {
			for x := area.MinX; x <= area.MaxX; x++ {
				p := pt(x, y)
				w0, _, _ := Winding(paths, p)
				w1, _, _ := Winding(withCancel, p)
				if w0 != w1 {
					t.Fatalf("seed %d: winding at %v changed from %d to %d", seed, p, w0, w1)
				}
			}
		}
	}
}
