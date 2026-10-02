package hexgrid

import "testing"

// beijing is the acceptance-test GPS coordinate (mid-face on face 4).
var beijing = struct{ lat, lng float64 }{39.9042, 116.4074}

func TestCellFromLatLngGolden(t *testing.T) {
	c := CellFromLatLng(beijing.lat, beijing.lng, 8)
	if got := c.String(); got != "2440007d180007a0" {
		t.Fatalf("golden encoding changed: got %s", got)
	}
	if c.Resolution() != 8 {
		t.Fatalf("resolution = %d, want 8", c.Resolution())
	}
	if c.Face() >= 20 {
		t.Fatalf("face out of range: %d", c.Face())
	}
}

func TestCellRoundtrip(t *testing.T) {
	// Deterministic spread of coordinates across the globe.
	for i := 0; i < 200; i++ {
		lat := -80.0 + float64(i%40)*4.0
		lng := -180.0 + float64(i)*3.7
		for res := 0; res <= 10; res++ {
			c := CellFromLatLng(lat, lng, res)
			clat, clng := c.LatLng()
			c2 := CellFromLatLng(clat, clng, res)
			if c != c2 {
				t.Fatalf("roundtrip failed at (%v,%v) res %d: %s -> (%v,%v) -> %s",
					lat, lng, res, c, clat, clng, c2)
			}
		}
	}
}

func TestParseCell(t *testing.T) {
	c := CellFromLatLng(beijing.lat, beijing.lng, 8)
	p, err := ParseCell(c.String())
	if err != nil {
		t.Fatal(err)
	}
	if p != c {
		t.Fatalf("parse roundtrip: %s != %s", p, c)
	}
	if _, err := ParseCell("xyz"); err != ErrInvalidCell {
		t.Fatalf("expected ErrInvalidCell, got %v", err)
	}
	if _, err := ParseCell("ff40007d180007a0"); err != ErrInvalidCell {
		t.Fatalf("expected face-range error, got %v", err)
	}
}

func TestParentChild(t *testing.T) {
	c := CellFromLatLng(beijing.lat, beijing.lng, 8)
	p5 := c.Parent(5)
	if p5.Resolution() != 5 {
		t.Fatalf("parent res = %d", p5.Resolution())
	}
	// Parent of parent chain must be consistent.
	p2 := p5.Parent(2)
	if c.Parent(2) != p2 {
		t.Fatalf("parent chain inconsistent")
	}
	// The res-6 parent of c must be one of p5's children.
	p6 := c.Parent(6)
	children := p5.Children(6)
	if len(children) != 7 {
		t.Fatalf("hex cell should have 7 children, got %d", len(children))
	}
	found := false
	for _, ch := range children {
		if ch.Parent(5) != p5 {
			t.Fatalf("child %s has wrong parent", ch)
		}
		if ch == p6 {
			found = true
		}
	}
	if !found {
		t.Fatalf("res-6 parent of cell not among children of res-5 parent")
	}
}

func TestIJKRoundtrip(t *testing.T) {
	// Exercise the float<->grid quantizers in both directions, including
	// negative folds, with deterministic pseudo-random coordinates.
	seed := uint32(12345)
	next := func() int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % 4096
	}
	for n := 0; n < 20000; n++ {
		co := normalizeIJK(ijk{next() - 2048, next() - 2048, next() - 2048})
		x, y := ijkToHex2d(co)
		if got := hex2dToIJK(x, y); got != co {
			t.Fatalf("ijk roundtrip: %v -> (%v,%v) -> %v", co, x, y, got)
		}
	}
}

func TestEncodeDecodeBijection(t *testing.T) {
	seed := uint32(999)
	next := func() int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % 3000
	}
	for n := 0; n < 20000; n++ {
		co := normalizeIJK(ijk{next(), next(), 0})
		face, res := n%20, n%16
		c := encodeIJK(face, res, co)
		f2, r2, co2 := decodeCell(c)
		if f2 != face || r2 != res || co2 != co {
			t.Fatalf("decode(encode(%d,%d,%v)) = (%d,%d,%v)", face, res, co, f2, r2, co2)
		}
	}
}
