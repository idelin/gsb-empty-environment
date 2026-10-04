package onion_test

import (
	"fmt"
	"math/rand"
	"testing"

	"onion"
)

// orientFast is an independent int64 orientation predicate. It is only used
// with small test coordinates (|coord| <= 1<<20), where the cross product
// cannot overflow.
func orientFast(a, b, c onion.Point) int {
	cross := (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
	switch {
	case cross > 0:
		return 1
	case cross < 0:
		return -1
	}
	return 0
}

func less(pts []onion.Point, a, b int) bool {
	pa, pb := pts[a], pts[b]
	if pa.X != pb.X {
		return pa.X < pb.X
	}
	if pa.Y != pb.Y {
		return pa.Y < pb.Y
	}
	return a < b
}

// onBoundary independently decides whether pts[p] lies on the boundary of
// the convex hull of pts restricted to ids: p is on the boundary iff some
// line through p and another point of the set has every point on one side
// (a supporting closed half-plane).
func onBoundary(pts []onion.Point, ids []int, p int, orient func(a, b, c onion.Point) int) bool {
	hasDistinct := false
	for _, q := range ids {
		if q == p || pts[q] == pts[p] {
			continue
		}
		hasDistinct = true
		pos, neg := false, false
		for _, r := range ids {
			switch orient(pts[p], pts[q], pts[r]) {
			case 1:
				pos = true
			case -1:
				neg = true
			}
			if pos && neg {
				break
			}
		}
		if !pos || !neg {
			return true
		}
	}
	return !hasDistinct
}

// verify checks the full decomposition of pts against an independent
// half-plane characterization, peeling one layer at a time.
func verify(t *testing.T, pts []onion.Point, orient func(a, b, c onion.Point) int) {
	t.Helper()
	layerOf, rings := onion.Layers(pts)
	if len(layerOf) != len(pts) {
		t.Fatalf("layerOf has length %d, want %d", len(layerOf), len(pts))
	}
	count := make([]int, len(pts))
	for li, ring := range rings {
		if len(ring) == 0 {
			t.Fatalf("layer %d: empty ring", li)
		}
		for _, id := range ring {
			if id < 0 || id >= len(pts) {
				t.Fatalf("layer %d: index %d out of range", li, id)
			}
			count[id]++
			if layerOf[id] != li {
				t.Errorf("point %d: layerOf = %d, but appears in ring %d", id, layerOf[id], li)
			}
		}
	}
	for i, c := range count {
		if c != 1 {
			t.Fatalf("point %d appears in %d rings, want exactly 1 (pts=%v)", i, c, pts)
		}
	}

	remaining := make([]int, len(pts))
	for i := range remaining {
		remaining[i] = i
	}
	for li, ring := range rings {
		want := map[int]bool{}
		for _, id := range remaining {
			if onBoundary(pts, remaining, id, orient) {
				want[id] = true
			}
		}
		got := map[int]bool{}
		for _, id := range ring {
			got[id] = true
		}
		if len(got) != len(ring) {
			t.Fatalf("layer %d: ring contains duplicate indices (pts=%v)", li, pts)
		}
		for id := range want {
			if !got[id] {
				t.Fatalf("layer %d: point %d %v missing from ring (pts=%v)", li, id, pts[id], pts)
			}
		}
		for id := range got {
			if !want[id] {
				t.Fatalf("layer %d: point %d %v should not be on boundary yet (pts=%v)", li, id, pts[id], pts)
			}
		}
		verifyRingShape(t, pts, ring, orient)
		next := remaining[:0]
		for _, id := range remaining {
			if !got[id] {
				next = append(next, id)
			}
		}
		remaining = next
	}
	if len(remaining) != 0 {
		t.Fatalf("%d points never assigned to a layer (pts=%v)", len(remaining), pts)
	}
}

// verifyRingShape checks the canonical form of one ring: it starts at the
// smallest (X, Y, index) point, degenerate rings are sorted ascending, and
// non-degenerate rings are strictly counterclockwise and convex.
func verifyRingShape(t *testing.T, pts []onion.Point, ring []int, orient func(a, b, c onion.Point) int) {
	t.Helper()
	for _, id := range ring[1:] {
		if less(pts, id, ring[0]) {
			t.Errorf("ring %v does not start at the smallest point (pts=%v)", ring, pts)
			break
		}
	}
	area := onion.Area2(pts, ring)
	if area.Sign() == 0 {
		for i := 1; i < len(ring); i++ {
			if less(pts, ring[i], ring[i-1]) {
				t.Errorf("degenerate ring %v not sorted ascending (pts=%v)", ring, pts)
				break
			}
		}
		return
	}
	if area.Sign() < 0 {
		t.Errorf("ring %v is clockwise, Area2 = %s (pts=%v)", ring, area, pts)
	}
	m := len(ring)
	for i := 0; i < m; i++ {
		a, b, c := ring[i], ring[(i+1)%m], ring[(i+2)%m]
		if orient(pts[a], pts[b], pts[c]) < 0 {
			t.Errorf("ring %v makes a right turn at point %d (pts=%v)", ring, b, pts)
			break
		}
	}
}

func TestExhaustiveGrid3x3(t *testing.T) {
	var grid []onion.Point
	for x := int64(0); x < 3; x++ {
		for y := int64(0); y < 3; y++ {
			grid = append(grid, onion.Point{X: x, Y: y})
		}
	}
	for mask := 1; mask < 1<<uint(len(grid)); mask++ {
		var pts []onion.Point
		for i := range grid {
			if mask&(1<<uint(i)) != 0 {
				pts = append(pts, grid[i])
			}
		}
		verify(t, pts, orientFast)
	}
}

// multisets returns all multisets of size k drawn from types elements, each
// represented as a sorted slice of element indices.
func multisets(types, k int) [][]int {
	var out [][]int
	cur := make([]int, 0, k)
	var rec func(start, left int)
	rec = func(start, left int) {
		if left == 0 {
			cp := make([]int, k)
			copy(cp, cur)
			out = append(out, cp)
			return
		}
		for i := start; i < types; i++ {
			cur = append(cur, i)
			rec(i, left-1)
			cur = cur[:len(cur)-1]
		}
	}
	rec(0, k)
	return out
}

func TestExhaustiveMultisetsGrid2x2(t *testing.T) {
	grid := []onion.Point{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	for size := 1; size <= 5; size++ {
		for _, ms := range multisets(len(grid), size) {
			pts := make([]onion.Point, len(ms))
			for i, g := range ms {
				pts[i] = grid[g]
			}
			verify(t, pts, orientFast)
		}
	}
}

func TestAllCollinear(t *testing.T) {
	cases := [][]onion.Point{
		{{0, 0}, {1, 0}, {2, 0}, {3, 0}, {5, 0}},
		{{0, 0}, {0, -2}, {0, 7}, {0, 3}},
		{{0, 0}, {1, 2}, {2, 4}, {3, 6}, {-1, -2}},
		{{3, -1}, {6, -2}, {0, 0}, {9, -3}, {-3, 1}},
		{{1, 1}, {1, 1}, {2, 2}, {2, 2}, {2, 2}, {0, 0}},
	}
	for _, pts := range cases {
		layerOf, rings := onion.Layers(pts)
		if len(rings) != 1 {
			t.Fatalf("collinear set %v produced %d layers, want 1", pts, len(rings))
		}
		for i, l := range layerOf {
			if l != 0 {
				t.Fatalf("collinear set %v: point %d on layer %d, want 0", pts, i, l)
			}
		}
		verify(t, pts, orientFast)
	}
}

func TestSinglePoint(t *testing.T) {
	layerOf, rings := onion.Layers([]onion.Point{{7, -3}})
	if len(layerOf) != 1 || layerOf[0] != 0 {
		t.Fatalf("layerOf = %v, want [0]", layerOf)
	}
	if len(rings) != 1 || len(rings[0]) != 1 || rings[0][0] != 0 {
		t.Fatalf("rings = %v, want [[0]]", rings)
	}
}

func TestEmpty(t *testing.T) {
	layerOf, rings := onion.Layers(nil)
	if len(layerOf) != 0 || len(rings) != 0 {
		t.Fatalf("got %v, %v; want empty", layerOf, rings)
	}
}

func TestAllIdentical(t *testing.T) {
	pts := []onion.Point{{5, 5}, {5, 5}, {5, 5}, {5, 5}, {5, 5}}
	layerOf, rings := onion.Layers(pts)
	if len(rings) != 1 {
		t.Fatalf("got %d layers, want 1", len(rings))
	}
	for i, l := range layerOf {
		if l != 0 {
			t.Fatalf("point %d on layer %d, want 0", i, l)
		}
	}
	for i, id := range rings[0] {
		if id != i {
			t.Fatalf("ring = %v, want [0 1 2 3 4]", rings[0])
		}
	}
}

func TestDuplicatesShareLayer(t *testing.T) {
	// Three copies of each corner of a square, plus interior points with
	// duplicates; duplicates must always land on the same layer.
	var pts []onion.Point
	for _, p := range []onion.Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {5, 5}, {4, 5}} {
		pts = append(pts, p, p, p)
	}
	layerOf, rings := onion.Layers(pts)
	byCoord := map[onion.Point]int{}
	for i, p := range pts {
		if l, ok := byCoord[p]; ok && l != layerOf[i] {
			t.Fatalf("duplicate %v split across layers %d and %d", p, l, layerOf[i])
		}
		byCoord[p] = layerOf[i]
	}
	if len(rings) != 2 {
		t.Fatalf("got %d layers, want 2", len(rings))
	}
	verify(t, pts, orientFast)
}

func TestOrientExactNoOverflow(t *testing.T) {
	const M = int64(1) << 62
	// Differences are 1<<63, which already overflows int64; the products
	// reach 1<<126. Exact arithmetic must still give the right sign.
	a := onion.Point{X: -M, Y: -M}
	b := onion.Point{X: M, Y: M}
	c := onion.Point{X: M, Y: M - 1}
	if got := onion.Orient(a, b, c); got != -1 {
		t.Errorf("Orient(%v, %v, %v) = %d, want -1", a, b, c, got)
	}
	if got := onion.Orient(a, b, onion.Point{X: M - 1, Y: M}); got != 1 {
		t.Errorf("Orient sign flip case = %d, want 1", got)
	}
	if got := onion.Orient(a, b, onion.Point{X: 0, Y: 0}); got != 0 {
		t.Errorf("Orient collinear case = %d, want 0", got)
	}
}

func TestLayersExtremeCoordinates(t *testing.T) {
	const M = int64(1) << 62
	pts := []onion.Point{
		{-M, -M}, {M, -M}, {M, M}, {-M, M}, // corners: layer 0
		{M, 0}, {0, -M}, // edge midpoints: layer 0
		{0, 0}, {1, -1}, // interior: layer 1
	}
	layerOf, rings := onion.Layers(pts)
	want := []int{0, 0, 0, 0, 0, 0, 1, 1}
	for i := range want {
		if layerOf[i] != want[i] {
			t.Fatalf("point %d %v: layer %d, want %d", i, pts[i], layerOf[i], want[i])
		}
	}
	if len(rings) != 2 {
		t.Fatalf("got %d layers, want 2", len(rings))
	}
	verify(t, pts, onion.Orient)
}

func TestArea2(t *testing.T) {
	pts := []onion.Point{{0, 0}, {4, 0}, {0, 3}}
	if got := onion.Area2(pts, []int{0, 1, 2}); got.Sign() <= 0 || got.Int64() != 12 {
		t.Errorf("Area2 ccw triangle = %v, want 12", got)
	}
	if got := onion.Area2(pts, []int{0, 2, 1}); got.Int64() != -12 {
		t.Errorf("Area2 cw triangle = %v, want -12", got)
	}
	if got := onion.Area2(pts, []int{0, 1}); got.Sign() != 0 {
		t.Errorf("Area2 segment = %v, want 0", got)
	}
}

func TestRandomSmall(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(40)
		pts := make([]onion.Point, n)
		for i := range pts {
			pts[i] = onion.Point{X: int64(rng.Intn(17) - 8), Y: int64(rng.Intn(17) - 8)}
		}
		verify(t, pts, orientFast)
	}
}

func TestRandomExtremeCoordinates(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 60; iter++ {
		n := 1 + rng.Intn(12)
		pts := make([]onion.Point, n)
		for i := range pts {
			pts[i] = onion.Point{X: rng.Int63() - (1 << 62), Y: rng.Int63() - (1 << 62)}
		}
		verify(t, pts, onion.Orient)
	}
}

func ExampleLayers() {
	pts := []onion.Point{
		{0, 0}, {4, 0}, {4, 4}, {0, 4}, // square corners
		{2, 0},         // on the bottom edge: same layer as the corners
		{1, 1}, {2, 2}, // interior
	}
	layerOf, rings := onion.Layers(pts)
	fmt.Println(layerOf)
	fmt.Println(rings)
	// Output:
	// [0 0 0 0 0 1 1]
	// [[0 4 1 2 3] [5 6]]
}
