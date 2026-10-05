package convlayers

import (
	"math/big"
	"sort"
	"testing"
)

// onBoundaryHalfPlane reports whether p lies on the boundary of the
// convex hull of set, decided by an independent supporting-half-plane
// test: p is on the boundary iff some line through p and another point
// of the set has every point on one side.
func onBoundaryHalfPlane(set []Point, p Point) bool {
	if len(set) <= 2 {
		return true
	}
	for _, q := range set {
		if q == p {
			continue
		}
		allLeft, allRight := true, true
		for _, r := range set {
			switch s := Orient(p, q, r); {
			case s < 0:
				allLeft = false
			case s > 0:
				allRight = false
			}
		}
		if allLeft || allRight {
			return true
		}
	}
	return false
}

// expectedLayers peels layers using only the half-plane boundary test,
// independent of the monotone-chain implementation under test.
func expectedLayers(coords []Point) map[Point]int {
	layers := make(map[Point]int, len(coords))
	remaining := append([]Point(nil), coords...)
	for layer := 0; len(remaining) > 0; layer++ {
		var next []Point
		for _, p := range remaining {
			if onBoundaryHalfPlane(remaining, p) {
				layers[p] = layer
			} else {
				next = append(next, p)
			}
		}
		remaining = next
	}
	return layers
}

func uniqueSorted(pts []Point) []Point {
	seen := make(map[Point]bool, len(pts))
	var out []Point
	for _, p := range pts {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].X != out[j].X {
			return out[i].X < out[j].X
		}
		return out[i].Y < out[j].Y
	})
	return out
}

// verifyLayers checks layer numbers against the independent half-plane
// peeling and validates every ring's canonical form.
func verifyLayers(t *testing.T, pts []Point) {
	t.Helper()
	layerOf, rings := Layers(pts)
	if len(layerOf) != len(pts) {
		t.Fatalf("layerOf has %d entries, want %d", len(layerOf), len(pts))
	}
	coords := uniqueSorted(pts)
	expected := expectedLayers(coords)

	for i, p := range pts {
		if layerOf[i] != expected[p] {
			t.Fatalf("pts[%d]=%v: layer %d, want %d (pts=%v)", i, p, layerOf[i], expected[p], pts)
		}
	}

	numLayers := 0
	for _, l := range expected {
		if l+1 > numLayers {
			numLayers = l + 1
		}
	}
	if len(rings) != numLayers {
		t.Fatalf("got %d rings, want %d (pts=%v)", len(rings), numLayers, pts)
	}

	seenID := make([]bool, len(pts))
	for layer, ring := range rings {
		if len(ring) == 0 {
			t.Fatalf("layer %d: empty ring (pts=%v)", layer, pts)
		}
		// IDs are valid, unique across rings, and on this layer.
		ringCoordCount := make(map[Point]int)
		for _, id := range ring {
			if id < 0 || id >= len(pts) {
				t.Fatalf("layer %d: bad id %d", layer, id)
			}
			if seenID[id] {
				t.Fatalf("layer %d: id %d appears twice", layer, id)
			}
			seenID[id] = true
			if layerOf[id] != layer {
				t.Fatalf("layer %d: id %d has layerOf %d", layer, id, layerOf[id])
			}
			ringCoordCount[pts[id]]++
		}
		// Ring coordinates are unique and match the boundary set.
		var ringCoords []Point
		coordSeen := make(map[Point]bool)
		for _, id := range ring {
			p := pts[id]
			if !coordSeen[p] {
				coordSeen[p] = true
				ringCoords = append(ringCoords, p)
			}
		}
		var boundary []Point
		for _, c := range coords {
			if expected[c] == layer {
				boundary = append(boundary, c)
			}
		}
		if len(ringCoords) != len(boundary) {
			t.Fatalf("layer %d: ring has %d coords, want %d (pts=%v)", layer, len(ringCoords), len(boundary), pts)
		}
		for _, c := range boundary {
			if ringCoordCount[c] == 0 {
				t.Fatalf("layer %d: boundary coord %v missing from ring (pts=%v)", layer, c, pts)
			}
		}
		// Duplicate IDs of one coordinate are adjacent and ascending.
		lastOf := make(map[Point]int)
		closed := make(map[Point]bool)
		for k, id := range ring {
			p := pts[id]
			if closed[p] {
				t.Fatalf("layer %d: ids of %v are not adjacent", layer, p)
			}
			if last, ok := lastOf[p]; ok && id < last {
				t.Fatalf("layer %d: ids of %v not ascending", layer, p)
			}
			if k > 0 && pts[ring[k-1]] != p {
				closed[pts[ring[k-1]]] = true
			}
			lastOf[p] = id
		}
		// Canonical start: lexicographically smallest coordinate first.
		for _, c := range ringCoords[1:] {
			if c.X < ringCoords[0].X || (c.X == ringCoords[0].X && c.Y < ringCoords[0].Y) {
				t.Fatalf("layer %d: ring does not start at lex-min coord (pts=%v)", layer, pts)
			}
		}
		degenerate := len(ringCoords) <= 2 || allCollinear(ringCoords)
		if degenerate {
			// Coordinates listed once, in lexicographic order.
			sorted := append([]Point(nil), ringCoords...)
			sortPoints(sorted)
			for i := range sorted {
				if sorted[i] != ringCoords[i] {
					t.Fatalf("layer %d: degenerate ring not lexicographic (pts=%v)", layer, pts)
				}
			}
			continue
		}
		// Non-degenerate: strictly CCW and every remaining point lies
		// left of (or on) every ring edge.
		if Area2(ringCoords).Sign() <= 0 {
			t.Fatalf("layer %d: ring not counterclockwise (pts=%v)", layer, pts)
		}
		for i, a := range ringCoords {
			b := ringCoords[(i+1)%len(ringCoords)]
			for _, c := range coords {
				if expected[c] >= layer && Orient(a, b, c) < 0 {
					t.Fatalf("layer %d: point %v right of edge %v->%v (pts=%v)", layer, c, a, b, pts)
				}
			}
		}
	}
	for i := range pts {
		if !seenID[i] {
			t.Fatalf("id %d missing from all rings (pts=%v)", i, pts)
		}
	}
}

func gridPoints(w, h int) []Point {
	var pts []Point
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			pts = append(pts, Point{int64(x), int64(y)})
		}
	}
	return pts
}

func TestExhaustiveGrid3x3(t *testing.T) {
	grid := gridPoints(3, 3)
	for mask := 0; mask < 1<<uint(len(grid)); mask++ {
		var pts []Point
		for i := range grid {
			if mask&(1<<uint(i)) != 0 {
				pts = append(pts, grid[i])
			}
		}
		verifyLayers(t, pts)
	}
}

func TestExhaustiveGrid4x3(t *testing.T) {
	grid := gridPoints(4, 3)
	for mask := 0; mask < 1<<uint(len(grid)); mask++ {
		var pts []Point
		for i := range grid {
			if mask&(1<<uint(i)) != 0 {
				pts = append(pts, grid[i])
			}
		}
		verifyLayers(t, pts)
	}
}

func TestAllCollinear(t *testing.T) {
	cases := [][]Point{
		{{0, 0}, {3, 6}, {1, 2}, {-2, -4}, {2, 4}}, // slope 2
		{{5, 0}, {5, 7}, {5, -3}, {5, 2}},          // vertical
		{{1, 1}, {4, 1}, {-2, 1}},                  // horizontal
	}
	for _, pts := range cases {
		layerOf, rings := Layers(pts)
		if len(rings) != 1 {
			t.Fatalf("%v: got %d rings, want 1", pts, len(rings))
		}
		for i := range pts {
			if layerOf[i] != 0 {
				t.Fatalf("%v: pts[%d] on layer %d, want 0", pts, i, layerOf[i])
			}
		}
		verifyLayers(t, pts)
	}
}

func TestSinglePointLayer(t *testing.T) {
	pts := []Point{
		{0, 0}, {9, 0}, {0, 9}, // outer triangle: layer 0
		{2, 2}, {5, 2}, {2, 5}, // inner triangle: layer 1
		{3, 3}, // lone center point: layer 2
	}
	layerOf, rings := Layers(pts)
	want := []int{0, 0, 0, 1, 1, 1, 2}
	for i, w := range want {
		if layerOf[i] != w {
			t.Fatalf("pts[%d]: layer %d, want %d", i, layerOf[i], w)
		}
	}
	if len(rings) != 3 || len(rings[2]) != 1 || rings[2][0] != 6 {
		t.Fatalf("rings = %v, want last ring to be [6]", rings)
	}
	verifyLayers(t, pts)
}

func TestDuplicatePoints(t *testing.T) {
	pts := []Point{
		{0, 0}, {4, 0}, {0, 4}, // triangle vertices
		{0, 0}, {0, 0}, // duplicate outer vertex
		{1, 1}, {1, 1}, {1, 1}, // duplicate interior point
		{2, 0}, {2, 0}, // duplicate collinear edge point
	}
	layerOf, rings := Layers(pts)
	wantLayer := []int{0, 0, 0, 0, 0, 1, 1, 1, 0, 0}
	for i, w := range wantLayer {
		if layerOf[i] != w {
			t.Fatalf("pts[%d]: layer %d, want %d", i, layerOf[i], w)
		}
	}
	if len(rings) != 2 {
		t.Fatalf("got %d rings, want 2", len(rings))
	}
	if len(rings[0]) != 7 || len(rings[1]) != 3 {
		t.Fatalf("ring sizes = %d,%d; want 7,3", len(rings[0]), len(rings[1]))
	}
	// Every input ID is preserved exactly once.
	count := make(map[int]int)
	for _, ring := range rings {
		for _, id := range ring {
			count[id]++
		}
	}
	for i := range pts {
		if count[i] != 1 {
			t.Fatalf("id %d appears %d times in rings", i, count[i])
		}
	}
	verifyLayers(t, pts)
}

func TestExactArithmeticNoOverflow(t *testing.T) {
	const B = 1 << 62
	// Naive int64 cross products here overflow (magnitudes near 2^124)
	// and would report collinear or flip sign.
	if got := Orient(Point{0, 0}, Point{B, B}, Point{B, 0}); got != -1 {
		t.Fatalf("Orient = %d, want -1", got)
	}
	if got := Orient(Point{0, 0}, Point{B, B}, Point{0, B}); got != 1 {
		t.Fatalf("Orient = %d, want 1", got)
	}
	if got := Orient(Point{-B, -B}, Point{B, B}, Point{0, 0}); got != 0 {
		t.Fatalf("Orient = %d, want 0", got)
	}
	// Layers of a huge square plus its center must not flip.
	pts := []Point{{-B, -B}, {B, -B}, {B, B}, {-B, B}, {0, 0}}
	layerOf, rings := Layers(pts)
	for i, w := range []int{0, 0, 0, 0, 1} {
		if layerOf[i] != w {
			t.Fatalf("pts[%d]: layer %d, want %d", i, layerOf[i], w)
		}
	}
	if len(rings) != 2 {
		t.Fatalf("got %d rings, want 2", len(rings))
	}
	// Twice the square's area is 2*(2B)^2 = 2^127, beyond int64.
	ringPts := make([]Point, 4)
	for i, id := range rings[0] {
		ringPts[i] = pts[id]
	}
	want := new(big.Int).Lsh(big.NewInt(1), 127)
	if got := Area2(ringPts); got.Cmp(want) != 0 {
		t.Fatalf("Area2 = %v, want %v", got, want)
	}
	verifyLayers(t, pts)
}

func TestEmptyAndSingleton(t *testing.T) {
	if layerOf, rings := Layers(nil); len(layerOf) != 0 || len(rings) != 0 {
		t.Fatalf("empty input: got %v, %v", layerOf, rings)
	}
	layerOf, rings := Layers([]Point{{7, -3}})
	if len(layerOf) != 1 || layerOf[0] != 0 || len(rings) != 1 || len(rings[0]) != 1 {
		t.Fatalf("singleton: layerOf=%v rings=%v", layerOf, rings)
	}
}

func TestCanonicalRingOrder(t *testing.T) {
	pts := []Point{{4, 2}, {0, 0}, {4, 0}, {0, 2}, {2, 0}} // (2,0) collinear on bottom edge
	_, rings := Layers(pts)
	if len(rings) != 1 {
		t.Fatalf("got %d rings, want 1", len(rings))
	}
	var got []Point
	for _, id := range rings[0] {
		got = append(got, pts[id])
	}
	want := []Point{{0, 0}, {2, 0}, {4, 0}, {4, 2}, {0, 2}}
	if len(got) != len(want) {
		t.Fatalf("ring = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ring = %v, want %v", got, want)
		}
	}
}
