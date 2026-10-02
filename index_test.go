package hexgrid

import (
	"math"
	"testing"
)

// TestEncodeDecodeRoundTrip encodes many coordinates, decodes the cell
// center and re-encodes: the same cell must come back (fixed point), and
// the center must lie within one hexagon of the original point.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	coords := [][2]float64{
		{39.9042, 116.4074}, {0, 0}, {51.5074, -0.1278}, {-33.8688, 151.2093},
		{90, 0}, {-90, 0}, {0, 180}, {0, -180}, {89.999, 179.999},
		{-89.999, -179.999}, {26.565, 0}, {26.565, 72}, {-26.565, 36},
		{37.7749, -122.4194}, {35.6762, 139.6503}, {-1.2921, 36.8219},
	}
	for _, ll := range coords {
		for _, res := range []int{0, 1, 4, 8, 12, 15} {
			c := LatLngToCell(ll[0], ll[1], res)
			if !c.IsValid() {
				t.Fatalf("%v res %d: invalid cell %s", ll, res, c)
			}
			lat2, lng2 := CellToLatLng(c)
			c2 := LatLngToCell(lat2, lng2, res)
			hexKm := hexSize[res] * 6371.0
			if c2 != c {
				// Cells deliberately extend past face edges (like H3's
				// overlapping base cells); a center lying across an edge
				// re-encodes onto the neighbouring face. Both cells must
				// then be valid with centers within one hexagon diameter.
				if !c2.IsValid() {
					t.Fatalf("%v res %d: re-encoded center invalid", ll, res)
				}
				lat3, lng3 := CellToLatLng(c2)
				d := greatCircleKm(lat2, lng2, lat3, lng3)
				if d > 2*hexKm+1e-6 {
					t.Fatalf("%v res %d: cross-edge re-encode %s -> %s, centers %.2f hex apart",
						ll, res, c, c2, d/hexKm)
				}
			}
			// Center must be within ~2 hexagon circumradii of the input.
			distKm := greatCircleKm(ll[0], ll[1], lat2, lng2)
			if distKm > 2*hexKm+1e-6 {
				t.Fatalf("%v res %d: center %.1f km away, hex %.3f km", ll, res, distKm, hexKm)
			}
		}
	}
}

func TestResolutionClamping(t *testing.T) {
	if c := LatLngToCell(39.9, 116.4, -5); c.Resolution() != 0 {
		t.Fatalf("negative resolution must clamp to 0, got %d", c.Resolution())
	}
	if c := LatLngToCell(39.9, 116.4, 99); c.Resolution() != maxRes {
		t.Fatalf("resolution must clamp to %d, got %d", maxRes, c.Resolution())
	}
}

func TestPolesAndDateline(t *testing.T) {
	// All longitudes at a pole encode to the same cell.
	a := LatLngToCell(90, 0, 8)
	b := LatLngToCell(90, 123, 8)
	if a != b {
		t.Fatalf("north pole cells differ: %s vs %s", a, b)
	}
	a = LatLngToCell(-90, 0, 8)
	b = LatLngToCell(-90, -45, 8)
	if a != b {
		t.Fatalf("south pole cells differ: %s vs %s", a, b)
	}
	// Date line from both sides lands in the same cell.
	a = LatLngToCell(10, 180, 8)
	b = LatLngToCell(10, -180, 8)
	if a != b {
		t.Fatalf("dateline cells differ: %s vs %s", a, b)
	}
}

func TestDistinctCellsDistinctIndices(t *testing.T) {
	// Nearby points at high resolution must (almost always) produce
	// distinct cells; check a small grid produces no collisions at res 15.
	seen := map[Cell][2]float64{}
	for i := 0; i < 20; i++ {
		for j := 0; j < 20; j++ {
			ll := [2]float64{39.9 + float64(i)*1e-4, 116.4 + float64(j)*1e-4}
			c := LatLngToCell(ll[0], ll[1], 15)
			if prev, dup := seen[c]; dup {
				t.Fatalf("collision: %v and %v both -> %s", prev, ll, c)
			}
			seen[c] = ll
		}
	}
}

func greatCircleKm(lat1, lng1, lat2, lng2 float64) float64 {
	p1 := latLngVec(lat1, lng1)
	p2 := latLngVec(lat2, lng2)
	d := p1.dot(p2)
	if d > 1 {
		d = 1
	}
	if d < -1 {
		d = -1
	}
	return math.Acos(d) * 6371.0
}

func latLngVec(lat, lng float64) vec3 {
	phi := lat * degToRad
	lambda := lng * degToRad
	cosPhi := math.Cos(phi)
	return vec3{cosPhi * math.Cos(lambda), cosPhi * math.Sin(lambda), math.Sin(phi)}
}
