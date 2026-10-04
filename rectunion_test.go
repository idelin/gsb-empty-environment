package rectunion

import (
	"math/big"
	"math/rand"
	"testing"
)

// bruteForce computes union area and perimeter on a unit-cell grid for
// rectangles with small integer coordinates. A unit cell [x,x+1]x[y,y+1]
// is covered iff its lower-left corner lies inside some closed rectangle
// (valid because all coordinates are integers). The perimeter counts
// cell sides bordering an uncovered cell.
func bruteForce(rects []Rect) (area, perimeter int64) {
	type cell struct{ x, y int64 }
	covered := make(map[cell]bool)
	for _, r := range rects {
		x1, x2 := r.X1.Int64(), r.X2.Int64()
		y1, y2 := r.Y1.Int64(), r.Y2.Int64()
		if x1 > x2 {
			x1, x2 = x2, x1
		}
		if y1 > y2 {
			y1, y2 = y2, y1
		}
		for x := x1; x < x2; x++ {
			for y := y1; y < y2; y++ {
				covered[cell{x, y}] = true
			}
		}
	}
	area = int64(len(covered))
	dirs := []cell{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for c := range covered {
		for _, d := range dirs {
			if !covered[cell{c.x + d.x, c.y + d.y}] {
				perimeter++
			}
		}
	}
	return area, perimeter
}

func checkAgainstBruteForce(t *testing.T, rects []Rect) {
	t.Helper()
	wantArea, wantPerim := bruteForce(rects)
	area, perim := Union(rects)
	if area.Cmp(big.NewInt(wantArea)) != 0 || perim.Cmp(big.NewInt(wantPerim)) != 0 {
		t.Errorf("Union(%v) = (%v, %v), want (%d, %d)",
			rects, area, perim, wantArea, wantPerim)
	}
}

func expect(t *testing.T, rects []Rect, wantArea, wantPerim int64) {
	t.Helper()
	area, perim := Union(rects)
	if area.Cmp(big.NewInt(wantArea)) != 0 || perim.Cmp(big.NewInt(wantPerim)) != 0 {
		t.Errorf("Union = (%v, %v), want (%d, %d)", area, perim, wantArea, wantPerim)
	}
	checkAgainstBruteForce(t, rects)
}

func TestEmpty(t *testing.T) {
	expect(t, nil, 0, 0)
}

func TestSingle(t *testing.T) {
	expect(t, []Rect{NewRect(0, 0, 3, 4)}, 12, 14)
}

func TestZeroAreaIgnored(t *testing.T) {
	rects := []Rect{
		NewRect(0, 0, 2, 2),
		NewRect(5, 5, 5, 9), // vertical line, zero area
		NewRect(7, 1, 7, 1), // point, zero area
		NewRect(3, 4, 9, 4), // horizontal line, zero area
	}
	expect(t, rects, 4, 8)
}

func TestNested(t *testing.T) {
	rects := []Rect{
		NewRect(0, 0, 10, 10),
		NewRect(2, 2, 8, 8),
		NewRect(4, 4, 5, 6),
	}
	expect(t, rects, 100, 40)
}

func TestDuplicate(t *testing.T) {
	rects := []Rect{
		NewRect(1, 1, 6, 4),
		NewRect(1, 1, 6, 4),
		NewRect(1, 1, 6, 4),
	}
	expect(t, rects, 15, 16)
}

// Corner-touching rectangles share only a point: no edge is absorbed,
// so the perimeter is the sum of both perimeters.
func TestCornerTouching(t *testing.T) {
	rects := []Rect{
		NewRect(0, 0, 2, 2),
		NewRect(2, 2, 5, 4),
	}
	expect(t, rects, 4+6, 8+10)
}

// Long-edge partial contact: the shared portion of the edges is interior
// and must not be counted; the exposed remainder must be.
func TestLongEdgePartialContact(t *testing.T) {
	rects := []Rect{
		NewRect(0, 0, 2, 10), // tall left rectangle
		NewRect(2, 3, 6, 7),  // touches part of the right edge
	}
	// Union: 2x10 + 4x4 = 36. Perimeter: outer 6x4 block on [3,7] plus
	// exposed left-rectangle edge: trace gives 2*(2+10) + 2*(4+4) - 2*4
	// shared = 24 + 16 - 8 = 32.
	expect(t, rects, 36, 32)
}

// Full edge contact merges two rectangles into one larger rectangle.
func TestFullEdgeContact(t *testing.T) {
	rects := []Rect{
		NewRect(0, 0, 3, 4),
		NewRect(3, 0, 7, 4),
	}
	expect(t, rects, 28, 22)
}

func TestOverlap(t *testing.T) {
	rects := []Rect{
		NewRect(0, 0, 4, 4),
		NewRect(2, 2, 6, 6),
	}
	// Area 16+16-4 = 28, perimeter 16+16-2*4(exposed overlap trim)...
	// verified against the grid brute force.
	expect(t, rects, 28, 24)
}

func TestDisjoint(t *testing.T) {
	rects := []Rect{
		NewRect(0, 0, 1, 1),
		NewRect(5, 5, 8, 9),
	}
	expect(t, rects, 1+12, 4+14)
}

// Contained edge: a rectangle whose edge lies along the middle of a
// longer edge of another, fully inside its span.
func TestEdgeContainedContact(t *testing.T) {
	rects := []Rect{
		NewRect(0, 0, 10, 2),
		NewRect(3, 2, 7, 5),
	}
	expect(t, rects, 20+12, 24+14-2*4)
}

func TestUnnormalizedAndBigCoordinates(t *testing.T) {
	// Coordinates given in reverse order and beyond int64 range.
	pow := new(big.Int).Lsh(big.NewInt(1), 100)
	a := Rect{
		X1: new(big.Int).Set(pow), Y1: new(big.Int).Set(pow),
		X2: big.NewInt(0), Y2: big.NewInt(0),
	}
	b := Rect{
		X1: new(big.Int).Set(pow), Y1: big.NewInt(0),
		X2: new(big.Int).Mul(pow, big.NewInt(2)), Y2: new(big.Int).Set(pow),
	}
	area, perim := Union([]Rect{a, b})
	wantArea := new(big.Int).Mul(pow, new(big.Int).Mul(pow, big.NewInt(2)))
	wantPerim := new(big.Int).Mul(pow, big.NewInt(6))
	if area.Cmp(wantArea) != 0 || perim.Cmp(wantPerim) != 0 {
		t.Errorf("big coords: got (%v, %v), want (%v, %v)", area, perim, wantArea, wantPerim)
	}
}

// Fuzz against the unit-cell brute force on small integer grids,
// including zero-area and duplicate rectangles.
func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 500; trial++ {
		n := 1 + rng.Intn(6)
		rects := make([]Rect, n)
		for i := range rects {
			x1 := int64(rng.Intn(9))
			y1 := int64(rng.Intn(9))
			x2 := x1 + int64(rng.Intn(5))
			y2 := y1 + int64(rng.Intn(5))
			rects[i] = NewRect(x1, y1, x2, y2)
		}
		checkAgainstBruteForce(t, rects)
	}
}
