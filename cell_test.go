package hexgrid

import (
	"math"
	"testing"
)

func TestGeoToCellResolution8(t *testing.T) {
	cell := GeoToCell(39.9042, 116.4074, 8)
	if !cell.IsValid() {
		t.Fatalf("cell %s is not valid", cell)
	}
	if got := cell.Resolution(); got != 8 {
		t.Fatalf("resolution = %d, want 8", got)
	}
	if got := cell.BaseCell(); got < 0 || got >= numFaces {
		t.Fatalf("base cell = %d, out of range", got)
	}
	s := cell.String()
	if len(s) != 16 {
		t.Fatalf("hex string %q length = %d, want 16", s, len(s))
	}
	parsed, err := ParseCell(s)
	if err != nil {
		t.Fatalf("ParseCell(%q): %v", s, err)
	}
	if parsed != cell {
		t.Fatalf("round trip mismatch: got %s, want %s", parsed, cell)
	}
}

func TestCellCenterRoundTrip(t *testing.T) {
	points := []struct{ lat, lng float64 }{
		{39.9042, 116.4074},  // Beijing
		{51.5074, -0.1278},   // London
		{-33.8688, 151.2093}, // Sydney
		{0.0, 0.0},           // Null Island
		{37.7749, -122.4194}, // San Francisco
		{-23.5505, -46.6333}, // Sao Paulo
	}
	for _, res := range []int{0, 1, 4, 8, 12} {
		for _, p := range points {
			cell := GeoToCell(p.lat, p.lng, res)
			clat, clng := cell.Center()
			a := latLngToVec(p.lat, p.lng)
			b := latLngToVec(clat, clng)
			ang := math.Acos(math.Min(1, math.Max(-1, a.dot(b))))
			// Gnomonic distortion stretches distances near face edges;
			// allow a generous multiple of the local hex size.
			if limit := 3 * hexSize(res); ang > limit {
				t.Errorf("res %d point (%v,%v): center off by %f rad (limit %f)",
					res, p.lat, p.lng, ang, limit)
			}
		}
	}
}

func TestParentChildHierarchy(t *testing.T) {
	cell := GeoToCell(39.9042, 116.4074, 10)

	for res := 0; res <= 10; res++ {
		parent := cell.Parent(res)
		if got := parent.Resolution(); got != res {
			t.Fatalf("parent resolution = %d, want %d", got, res)
		}
		if !parent.IsValid() {
			t.Fatalf("parent %s invalid", parent)
		}
		for i := 1; i <= res; i++ {
			if parent.Digit(i) != cell.Digit(i) {
				t.Fatalf("parent digit %d = %d, want %d", i, parent.Digit(i), cell.Digit(i))
			}
		}
	}
	if cell.Parent(12) != cell {
		t.Fatal("Parent above own resolution should return the cell itself")
	}

	// Child/Parent round trip at every level.
	cur := cell.Parent(3)
	for res := 3; res < 10; res++ {
		children, err := cur.Children()
		if err != nil {
			t.Fatalf("Children: %v", err)
		}
		if len(children) != 7 {
			t.Fatalf("children count = %d, want 7", len(children))
		}
		seen := map[Cell]bool{}
		for _, ch := range children {
			if !ch.IsValid() {
				t.Fatalf("child %s invalid", ch)
			}
			if seen[ch] {
				t.Fatalf("duplicate child %s", ch)
			}
			seen[ch] = true
			if ch.Parent(res) != cur {
				t.Fatalf("child %s parent = %s, want %s", ch, ch.Parent(res), cur)
			}
			if ch.Resolution() != res+1 {
				t.Fatalf("child resolution = %d, want %d", ch.Resolution(), res+1)
			}
		}
		// Descend along the digit chain of the original cell.
		next, err := cur.Child(cell.Digit(res + 1))
		if err != nil {
			t.Fatalf("Child: %v", err)
		}
		cur = next
	}
	if cur != cell {
		t.Fatalf("descending digit chain gave %s, want %s", cur, cell)
	}
}

func TestParseCellErrors(t *testing.T) {
	if _, err := ParseCell("not-hex"); err == nil {
		t.Fatal("expected error for garbage input")
	}
	if _, err := ParseCell("ffffffffffffffff"); err == nil {
		t.Fatal("expected error for invalid mode/digits")
	}
	if _, err := ParseCell(""); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestEncodeDeterministicAndDistinct(t *testing.T) {
	a := GeoToCell(39.9042, 116.4074, 8)
	b := GeoToCell(39.9042, 116.4074, 8)
	if a != b {
		t.Fatal("encoding must be deterministic")
	}
	// A point ~1 degree of latitude away must fall into a different
	// res-8 cell (res-8 hexagons are ~30km in circumradius).
	c := GeoToCell(40.9042, 116.4074, 8)
	if a == c {
		t.Fatal("distinct locations should encode to distinct cells at res 8")
	}
}

func TestFaceCoverageNoInvalidCells(t *testing.T) {
	// Sweep a global grid: every point must encode to a valid cell whose
	// base cell is a real face, at several resolutions.
	for _, res := range []int{0, 2, 5} {
		for lat := -80.0; lat <= 80; lat += 10 {
			for lng := -180.0; lng < 180; lng += 10 {
				cell := GeoToCell(lat, lng, res)
				if !cell.IsValid() {
					t.Fatalf("res %d (%v,%v) -> invalid cell %s", res, lat, lng, cell)
				}
			}
		}
	}
}
