package hexgrid

import "testing"

// beijingCell is the golden acceptance value: GPS (39.9042, 116.4074)
// encoded at resolution 8.
const beijingCell = "1800000000009457"

func TestLatLngToCellGolden(t *testing.T) {
	c, err := LatLngToCell(39.9042, 116.4074, 8)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if got := c.String(); got != beijingCell {
		t.Fatalf("golden mismatch: got %s, want %s", got, beijingCell)
	}
	if c.Resolution() != 8 {
		t.Fatalf("resolution = %d, want 8", c.Resolution())
	}
	if !c.IsValid() {
		t.Fatal("golden cell is not valid")
	}
	if !c.IsReachable() {
		t.Fatal("golden cell is not reachable")
	}
}

func TestParseRoundTrip(t *testing.T) {
	c, err := ParseCell(beijingCell)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if c.String() != beijingCell {
		t.Fatalf("round trip mismatch: %s", c)
	}
	if _, err := ParseCell("not-a-cell"); err == nil {
		t.Fatal("expected parse error for garbage")
	}
	if _, err := ParseCell("ffffffffffffffff"); err == nil {
		t.Fatal("expected validation error for bad mode bits")
	}
}

func TestInvalidInputs(t *testing.T) {
	for _, res := range []int{-1, MaxRes + 1, 100} {
		if _, err := LatLngToCell(0, 0, res); err != ErrInvalidResolution {
			t.Fatalf("res %d: got %v, want ErrInvalidResolution", res, err)
		}
	}
	for _, ll := range [][2]float64{
		{90.0001, 0}, {-90.0001, 0}, {0, 180.0001}, {0, -180.0001},
		{nan(), 0}, {0, nan()},
	} {
		if _, err := LatLngToCell(ll[0], ll[1], 4); err != ErrInvalidLatLng {
			t.Fatalf("coord %v: got %v, want ErrInvalidLatLng", ll, err)
		}
	}
}

func nan() float64 {
	var z float64
	return z / z
}

func TestBoundaryCoords(t *testing.T) {
	coords := [][2]float64{
		{90, 0}, {-90, 0}, // poles
		{0, 180}, {0, -180}, // antimeridian
		{0, 0}, // null island
		{89.9999, 179.9999},
		{-89.9999, -179.9999},
	}
	for _, ll := range coords {
		for res := 0; res <= MaxRes; res++ {
			c, err := LatLngToCell(ll[0], ll[1], res)
			if err != nil {
				t.Fatalf("coord %v res %d: %v", ll, res, err)
			}
			if !c.IsValid() {
				t.Fatalf("coord %v res %d: invalid cell %s", ll, res, c)
			}
			if v := AssertEncodeRoundTrip(ll[0], ll[1], res); len(v) > 0 {
				t.Fatalf("coord %v res %d: %v", ll, res, v)
			}
		}
	}
	// The antimeridian is a single line: +180 and -180 must agree.
	a, _ := LatLngToCell(0, 180, 8)
	b, _ := LatLngToCell(0, -180, 8)
	if a != b {
		t.Fatalf("antimeridian mismatch: %s != %s", a, b)
	}
}

func TestParentChildScaling(t *testing.T) {
	c, _ := LatLngToCell(39.9042, 116.4074, 8)
	// Walking up to res 0 and verifying each step.
	cur := c
	for res := 8; res > 0; res-- {
		p, ok := cur.Parent()
		if !ok {
			t.Fatalf("res %d cell has no parent", res)
		}
		if p.Resolution() != res-1 {
			t.Fatalf("parent resolution = %d, want %d", p.Resolution(), res-1)
		}
		children, ok := p.Children()
		if !ok {
			t.Fatalf("res %d parent has no children", res-1)
		}
		found := false
		for _, ch := range children {
			if ch == cur {
				found = true
			}
		}
		if !found {
			t.Fatalf("cell %s not among children of parent %s", cur, p)
		}
		cur = p
	}
	if _, ok := cur.Parent(); ok {
		t.Fatal("res-0 cell must not have a parent")
	}
	// ParentTo across multiple levels at once.
	root, err := c.ParentTo(0)
	if err != nil {
		t.Fatal(err)
	}
	if root != cur {
		t.Fatalf("ParentTo(0) = %s, want %s", root, cur)
	}
	if _, err := c.ParentTo(9); err == nil {
		t.Fatal("ParentTo finer resolution must fail")
	}
	if _, err := c.ParentTo(-1); err != ErrInvalidResolution {
		t.Fatalf("got %v, want ErrInvalidResolution", err)
	}
	// Children are exactly the 4 subcells.
	children, _ := c.Children()
	seen := map[Cell]bool{}
	for _, ch := range children {
		if ch.Resolution() != 9 {
			t.Fatalf("child resolution = %d, want 9", ch.Resolution())
		}
		if p, _ := ch.Parent(); p != c {
			t.Fatalf("parent of child %s = %s, want %s", ch, p, c)
		}
		if seen[ch] {
			t.Fatalf("duplicate child %s", ch)
		}
		seen[ch] = true
	}
	// MaxRes boundary: no children.
	maxCell, _ := LatLngToCell(39.9042, 116.4074, MaxRes)
	if _, ok := maxCell.Children(); ok {
		t.Fatal("MaxRes cell must not have children")
	}
}

func TestIsValidCorrupted(t *testing.T) {
	c, _ := LatLngToCell(39.9042, 116.4074, 8)
	if !c.IsValid() {
		t.Fatal("freshly encoded cell must be valid")
	}
	// Zero mode bits.
	if Cell(0).IsValid() {
		t.Fatal("zero cell must be invalid")
	}
	// Face out of range: face 31 with valid mode/res.
	bad := Cell(1<<60 | 4<<56 | 31<<51)
	if bad.IsValid() {
		t.Fatal("face 31 must be invalid")
	}
	// Index out of range for the resolution: res 0 allows only (0,0).
	badIdx := Cell(1<<60 | 0<<56 | 0<<51 | 1)
	if badIdx.IsValid() {
		t.Fatal("res-0 cell with index 1 must be invalid")
	}
}

func TestUniverse(t *testing.T) {
	u, err := Universe(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(u) != 20*64 {
		t.Fatalf("universe res 3 has %d cells, want %d", len(u), 20*64)
	}
	seen := map[Cell]bool{}
	for _, c := range u {
		if !c.IsValid() {
			t.Fatalf("universe contains invalid cell %s", c)
		}
		if seen[c] {
			t.Fatalf("universe contains duplicate %s", c)
		}
		seen[c] = true
	}
	if _, err := Universe(MaxRes + 1); err != ErrInvalidResolution {
		t.Fatalf("got %v, want ErrInvalidResolution", err)
	}
}

func TestHierarchySweep(t *testing.T) {
	u, _ := Universe(4)
	for _, c := range u {
		if v := AssertHierarchy(c); len(v) > 0 {
			t.Fatalf("hierarchy violations for %s: %v", c, v)
		}
	}
}
