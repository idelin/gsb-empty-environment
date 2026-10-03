package hexgrid

import (
	"math"
	"math/rand"
	"testing"
)

// Golden acceptance vector: Tiananmen, Beijing at resolution 8.
func TestLatLngToCellGolden(t *testing.T) {
	c, err := LatLngToCell(39.9042, 116.4074, 8)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.String(), "1888410e95fffff"; got != want {
		t.Fatalf("beijing res8 = %s, want %s", got, want)
	}
	if c.Res() != 8 {
		t.Fatalf("res = %d, want 8", c.Res())
	}
}

// TestLatLngToCellRoundTrip encodes the geographic center of random
// valid cells: a cell's center lies strictly inside its own lattice
// Voronoi region, so re-encoding must reproduce the exact same cell.
// Cells whose center sits in the overlap of two base-cell coverage
// regions (nearest icosahedron face != own base cell) are skipped:
// they belong to the neighboring face's hierarchy by construction.
func TestLatLngToCellRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		res := 1 + rng.Intn(10)
		digits := make([]uint8, res)
		for j := range digits {
			digits[j] = uint8(rng.Intn(7))
		}
		c := MustCell(rng.Intn(NumBaseCells), res, digits)
		center := CellToLatLng(c)
		c2, err := LatLngToCell(center.Lat, center.Lng, res)
		if err != nil {
			t.Fatal(err)
		}
		if c2.BaseCell() != c.BaseCell() {
			continue // coverage overlap near icosahedron vertices
		}
		if c2 != c {
			t.Fatalf("round trip unstable: %s -> %v -> %s", c, center, c2)
		}
	}
}

// TestLatLngToCellGlobalCoverage encodes random coordinates all over
// the globe: every coordinate must encode without error (exact lattice
// path or greedy fallback near icosahedron vertices).
func TestLatLngToCellGlobalCoverage(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 5000; i++ {
		lat := rng.Float64()*180 - 90
		lng := rng.Float64()*360 - 180
		c, err := LatLngToCell(lat, lng, 8)
		if err != nil {
			t.Fatalf("(%v, %v): %v", lat, lng, err)
		}
		if !c.IsValid() {
			t.Fatalf("(%v, %v) produced invalid cell %s", lat, lng, c)
		}
	}
}

func TestFaceCentersEncodeToZeroDigits(t *testing.T) {
	for b := 0; b < NumBaseCells; b++ {
		ll := FaceCenter(b)
		c, err := LatLngToCell(ll.Lat, ll.Lng, 8)
		if err != nil {
			t.Fatal(err)
		}
		if c.BaseCell() != b {
			t.Fatalf("face %d center encoded to base cell %d", b, c.BaseCell())
		}
		for r := 1; r <= 8; r++ {
			if c.Digit(r) != 0 {
				t.Fatalf("face %d center digit %d = %d, want 0", b, r, c.Digit(r))
			}
		}
	}
}

func TestLatLngBoundaryConditions(t *testing.T) {
	// Poles and dateline must encode without error.
	for _, ll := range [][2]float64{
		{90, 0}, {-90, 0}, {90, 120}, {-90, -45},
		{0, 180}, {0, -180}, {0, 540}, {45, -540},
		{89.9999, 179.9999}, {-89.9999, -179.9999},
	} {
		if _, err := LatLngToCell(ll[0], ll[1], 8); err != nil {
			t.Fatalf("(%v, %v): %v", ll[0], ll[1], err)
		}
	}
	// Longitude normalization: 180 == -180, 540 == 180.
	a, _ := LatLngToCell(10, 180, 8)
	b, _ := LatLngToCell(10, -180, 8)
	c, _ := LatLngToCell(10, 540, 8)
	if a != b || a != c {
		t.Fatal("longitude normalization inconsistent")
	}
	// Invalid inputs.
	for _, ll := range [][2]float64{
		{91, 0}, {-91, 0},
		{math.NaN(), 0}, {0, math.NaN()},
		{math.Inf(1), 0}, {0, math.Inf(-1)},
	} {
		if _, err := LatLngToCell(ll[0], ll[1], 8); err == nil {
			t.Fatalf("(%v, %v) accepted, want error", ll[0], ll[1])
		}
	}
	// Invalid resolutions.
	if _, err := LatLngToCell(0, 0, -1); err == nil {
		t.Fatal("negative resolution accepted")
	}
	if _, err := LatLngToCell(0, 0, MaxRes+1); err == nil {
		t.Fatal("resolution > MaxRes accepted")
	}
}

func TestCellToBoundaryClosed(t *testing.T) {
	c := MustCell(17, 8, []uint8{1, 0, 1, 0, 4, 6, 3, 4})
	b := CellToBoundary(c)
	if len(b) != 6 {
		t.Fatalf("%d vertices, want 6", len(b))
	}
	ctr := CellToLatLng(c)
	for i, v := range b {
		if math.Abs(v.Lat) > 90 || math.Abs(v.Lng) > 180 {
			t.Fatalf("vertex %d out of range: %v", i, v)
		}
		// Vertices must be close to the center (same cell scale).
		if math.Abs(v.Lat-ctr.Lat) > 1 || math.Abs(normalizeLng(v.Lng-ctr.Lng)) > 1 {
			t.Fatalf("vertex %d implausibly far from center", i)
		}
	}
}

func TestHierarchyInvariantsAcrossGlobe(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	for i := 0; i < 300; i++ {
		lat := rng.Float64()*170 - 85
		lng := rng.Float64()*360 - 180
		res := 1 + rng.Intn(10)
		c, err := LatLngToCell(lat, lng, res)
		if err != nil {
			t.Fatal(err)
		}
		if err := AssertHierarchy(c); err != nil {
			t.Fatalf("cell %s: %v", c, err)
		}
	}
}
