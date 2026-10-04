package rectunion

import (
	"math/big"
	"math/rand"
	"testing"
)

// cell is a unit grid cell [x,x+1] x [y,y+1].
type cell struct{ x, y int64 }

// bruteForce rasterizes the rectangles into unit cells on a small
// integer grid and derives area and perimeter from the cell set.
// It is only valid for small coordinate ranges and is used as a
// test oracle for the sweep-line implementation.
func bruteForce(rects []Rect) (area, perimeter int64) {
	cells := make(map[cell]bool)
	for _, r := range rects {
		r = r.normalized()
		if r.degenerate() {
			continue
		}
		for x := r.X1; x < r.X2; x++ {
			for y := r.Y1; y < r.Y2; y++ {
				cells[cell{x, y}] = true
			}
		}
	}
	area = int64(len(cells))
	for c := range cells {
		for _, d := range []cell{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			if !cells[cell{c.x + d.x, c.y + d.y}] {
				perimeter++
			}
		}
	}
	return area, perimeter
}

func check(t *testing.T, rects []Rect, wantArea, wantPerim int64) {
	t.Helper()
	area, perim := Union(rects)
	if area.Cmp(big.NewInt(wantArea)) != 0 {
		t.Errorf("area = %v, want %d (rects=%v)", area, wantArea, rects)
	}
	if perim.Cmp(big.NewInt(wantPerim)) != 0 {
		t.Errorf("perimeter = %v, want %d (rects=%v)", perim, wantPerim, rects)
	}
}

func checkAgainstOracle(t *testing.T, rects []Rect) {
	t.Helper()
	wantArea, wantPerim := bruteForce(rects)
	check(t, rects, wantArea, wantPerim)
}

func TestEmpty(t *testing.T) {
	check(t, nil, 0, 0)
	check(t, []Rect{}, 0, 0)
}

func TestDegenerate(t *testing.T) {
	// Zero-area rectangles (lines, points) contribute nothing.
	check(t, []Rect{{0, 0, 5, 0}}, 0, 0)
	check(t, []Rect{{3, 3, 3, 3}}, 0, 0)
	check(t, []Rect{{0, 0, 0, 7}, {2, 2, 2, 2}}, 0, 0)
	// Degenerate rectangles mixed with real ones change nothing.
	check(t, []Rect{{0, 0, 2, 2}, {0, 0, 5, 0}, {1, 1, 1, 1}}, 4, 8)
}

func TestSingle(t *testing.T) {
	check(t, []Rect{{0, 0, 1, 1}}, 1, 4)
	check(t, []Rect{{0, 0, 3, 5}}, 15, 16)
	// Reversed corners are normalized.
	check(t, []Rect{{5, 3, 0, 0}}, 15, 16)
}

func TestNested(t *testing.T) {
	// Inner rectangle fully inside the outer one adds nothing.
	check(t, []Rect{{0, 0, 10, 10}, {2, 2, 5, 5}}, 100, 40)
	check(t, []Rect{{0, 0, 10, 10}, {2, 2, 5, 5}, {3, 3, 4, 4}}, 100, 40)
	// Inner touching the outer boundary: still fully absorbed.
	check(t, []Rect{{0, 0, 10, 10}, {0, 0, 4, 4}}, 100, 40)
}

func TestDuplicate(t *testing.T) {
	check(t, []Rect{{1, 1, 4, 4}, {1, 1, 4, 4}}, 9, 12)
	check(t, []Rect{{1, 1, 4, 4}, {1, 1, 4, 4}, {1, 1, 4, 4}}, 9, 12)
}

func TestCornerTouching(t *testing.T) {
	// Closed rectangles touching at a single corner point share no
	// edge; the union is disconnected and both perimeters count fully.
	check(t, []Rect{{0, 0, 1, 1}, {1, 1, 2, 2}}, 2, 8)
	check(t, []Rect{{0, 0, 2, 2}, {2, 2, 5, 4}}, 10, 8+10)
}

func TestEdgeSharing(t *testing.T) {
	// Full shared edge: internal edge is not part of the perimeter.
	check(t, []Rect{{0, 0, 1, 1}, {1, 0, 2, 1}}, 2, 6)
	// Partial shared edge: only the exposed part counts.
	// [0,2]x[0,2] and [2,4]x[1,2] share the segment y in [1,2] at x=2.
	check(t, []Rect{{0, 0, 2, 2}, {2, 1, 4, 2}}, 6, 12)
	// Long edge partially abutting a shorter one.
	// [0,4]x[0,1] below, [1,3]x[1,3] above: shared segment x in [1,3].
	check(t, []Rect{{0, 0, 4, 1}, {1, 1, 3, 3}}, 8, 14)
	// T-junction on both sides of a long edge.
	check(t, []Rect{{0, 0, 6, 2}, {1, 2, 3, 4}, {4, -2, 5, 0}}, 12+4+2, 16+8+6-2*2-2*1)
}

func TestOverlap(t *testing.T) {
	// Overlapping squares: union is an L-free plus-like shape.
	check(t, []Rect{{0, 0, 2, 2}, {1, 1, 3, 3}}, 7, 12)
	// Cross shape.
	check(t, []Rect{{1, 0, 3, 4}, {0, 1, 4, 3}}, 12, 16)
}

func TestDisconnected(t *testing.T) {
	check(t, []Rect{{0, 0, 1, 1}, {5, 5, 7, 9}}, 9, 4+12)
}

func TestBigCoordinates(t *testing.T) {
	// Results must not overflow 64-bit arithmetic.
	b := int64(1) << 40
	area, perim := Union([]Rect{{0, 0, b, b}, {b / 2, b / 2, 2 * b, 2 * b}})
	// Union area: b^2 + (3b/2)^2 - (b/2)^2 = 3b^2; perimeter: 8b.
	wantArea := new(big.Int).Mul(big.NewInt(3), new(big.Int).Mul(big.NewInt(b), big.NewInt(b)))
	wantPerim := big.NewInt(8 * b)
	if area.Cmp(wantArea) != 0 {
		t.Errorf("area = %v, want %v", area, wantArea)
	}
	if perim.Cmp(wantPerim) != 0 {
		t.Errorf("perimeter = %v, want %v", perim, wantPerim)
	}
}

func TestRandomAgainstOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 2000; trial++ {
		n := rng.Intn(6) + 1
		rects := make([]Rect, 0, n)
		for i := 0; i < n; i++ {
			// Small integer grid; occasionally emit degenerate rects
			// by allowing equal corners.
			r := Rect{
				X1: int64(rng.Intn(9)),
				Y1: int64(rng.Intn(9)),
				X2: int64(rng.Intn(9)),
				Y2: int64(rng.Intn(9)),
			}
			rects = append(rects, r)
		}
		checkAgainstOracle(t, rects)
	}
}

func TestRandomDuplicatesAndNesting(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 500; trial++ {
		var rects []Rect
		base := Rect{0, 0, int64(rng.Intn(6) + 1), int64(rng.Intn(6) + 1)}
		rects = append(rects, base)
		// Duplicates of the base rectangle.
		for i := 0; i < rng.Intn(3); i++ {
			rects = append(rects, base)
		}
		// Rectangles nested inside the base.
		for i := 0; i < rng.Intn(4); i++ {
			x1 := int64(rng.Intn(int(base.X2) + 1))
			x2 := int64(rng.Intn(int(base.X2) + 1))
			y1 := int64(rng.Intn(int(base.Y2) + 1))
			y2 := int64(rng.Intn(int(base.Y2) + 1))
			rects = append(rects, Rect{x1, y1, x2, y2})
		}
		checkAgainstOracle(t, rects)
	}
}
