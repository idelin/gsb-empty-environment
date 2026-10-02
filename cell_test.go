package hexgrid

import "testing"

// Golden acceptance vector: a GPS coordinate encoded at resolution 8 must
// produce a stable hexadecimal string.
func TestEncodeGolden(t *testing.T) {
	c := LatLngToCell(39.9042, 116.4074, 8) // Beijing
	if got, want := c.String(), "181c818136fffff8"; got != want {
		t.Fatalf("golden encoding mismatch: got %s want %s", got, want)
	}
	if c.Resolution() != 8 {
		t.Fatalf("resolution = %d, want 8", c.Resolution())
	}
	if !c.IsValid() {
		t.Fatal("golden cell is not valid")
	}
}

func TestStringRoundTrip(t *testing.T) {
	coords := [][2]float64{
		{39.9042, 116.4074}, {0, 0}, {51.5074, -0.1278},
		{-33.8688, 151.2093}, {90, 0}, {-90, 0}, {0, 180}, {0, -180},
	}
	for _, ll := range coords {
		for res := 0; res <= maxRes; res++ {
			c := LatLngToCell(ll[0], ll[1], res)
			s := c.String()
			back, err := ParseCell(s)
			if err != nil {
				t.Fatalf("ParseCell(%q): %v", s, err)
			}
			if back != c {
				t.Fatalf("round trip %v res %d: %s -> %s", ll, res, c, back)
			}
		}
	}
}

func TestParseCellInvalid(t *testing.T) {
	if _, err := ParseCell("zz"); err == nil {
		t.Fatal("expected error for non-hex input")
	}
	if _, err := ParseCell("0"); err == nil {
		t.Fatal("expected error for mode 0")
	}
	// Corrupt a valid cell: set a used digit to 7.
	c := LatLngToCell(39.9042, 116.4074, 8)
	bad := Cell(uint64(c) | 7<<digitShift(1))
	if bad.IsValid() {
		t.Fatal("cell with digit 7 at used level must be invalid")
	}
	if _, err := ParseCell(bad.String()); err == nil {
		t.Fatal("expected parse error for invalid cell")
	}
}

func TestIsValidBoundaries(t *testing.T) {
	var zero Cell
	if zero.IsValid() {
		t.Fatal("zero cell must be invalid")
	}
	// Base cell out of range.
	v := modeCell<<modeShift | uint64(numBaseCells)<<baseShift
	for i := 1; i <= numDigits; i++ {
		v |= uint64(unusedDigit) << digitShift(i)
	}
	if Cell(v).IsValid() {
		t.Fatal("base cell >= 140 must be invalid")
	}
}

func TestParentChildHierarchy(t *testing.T) {
	c := LatLngToCell(39.9042, 116.4074, 12)
	// Parent chain: resolutions decrease, digits are a strict prefix.
	p := c
	for res := 11; res >= 0; res-- {
		p = p.Parent(res)
		if p.Resolution() != res {
			t.Fatalf("parent resolution = %d, want %d", p.Resolution(), res)
		}
		for i := 1; i <= res; i++ {
			if p.Digit(i) != c.Digit(i) {
				t.Fatalf("parent digit %d = %d, want prefix %d", i, p.Digit(i), c.Digit(i))
			}
		}
		for i := res + 1; i <= numDigits; i++ {
			if p.Digit(i) != unusedDigit {
				t.Fatalf("parent digit %d = %d, want 7 (unused)", i, p.Digit(i))
			}
		}
	}
	// Parent of same/higher resolution is identity.
	if c.Parent(12) != c || c.Parent(15) != c {
		t.Fatal("Parent(res >= own) must be identity")
	}
	// Every child maps back to the parent.
	p8 := c.Parent(8)
	for d := 0; d < 7; d++ {
		child := p8.Child(d)
		if child.Resolution() != 9 {
			t.Fatalf("child resolution = %d, want 9", child.Resolution())
		}
		if child.Digit(9) != d {
			t.Fatalf("child digit = %d, want %d", child.Digit(9), d)
		}
		if child.Parent(8) != p8 {
			t.Fatalf("parent of child %d mismatch", d)
		}
	}
	children := p8.Children()
	seen := map[Cell]bool{}
	for _, ch := range children {
		if seen[ch] {
			t.Fatal("duplicate child")
		}
		seen[ch] = true
		if ch.Parent(8) != p8 {
			t.Fatal("children() entry does not map back to parent")
		}
	}
	if len(seen) != 7 {
		t.Fatalf("want 7 distinct children, got %d", len(seen))
	}
	// Child at max resolution is a no-op.
	top := c.Parent(0)
	deep := top
	for deep.Resolution() < maxRes {
		deep = deep.Child(0)
	}
	if deep.Child(3) != deep {
		t.Fatal("Child at max resolution must be a no-op")
	}
}

func TestChildCenterNearParent(t *testing.T) {
	// Geometric containment is approximate (aperture-7 rotates each level),
	// but the center child must stay within one parent hexagon diameter.
	c := LatLngToCell(39.9042, 116.4074, 6)
	plat, plng := CellToLatLng(c)
	for d := 0; d < 7; d++ {
		clat, clng := CellToLatLng(c.Child(d))
		dlat := (plat - clat) * 111.32 // km per degree
		dlng := (plng - clng) * 85.4   // km per degree at lat 40
		dist := dlat*dlat + dlng*dlng
		// Parent hexagon at res 6 is ~30 km across; children must be well
		// inside that neighbourhood.
		if dist > 30*30 {
			t.Fatalf("child %d center too far from parent: %v km^2", d, dist)
		}
	}
}
