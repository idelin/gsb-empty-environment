package hexgrid

import (
	"strings"
	"testing"
)

func TestNewCellRoundTrip(t *testing.T) {
	digits := []uint8{1, 0, 1, 0, 4, 6, 3, 4}
	c, err := NewCell(17, 8, digits)
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseCell() != 17 || c.Res() != 8 {
		t.Fatalf("base/res = %d/%d, want 17/8", c.BaseCell(), c.Res())
	}
	got := c.Digits()
	if len(got) != len(digits) {
		t.Fatalf("digits len %d, want %d", len(got), len(digits))
	}
	for i := range digits {
		if got[i] != digits[i] || c.Digit(i+1) != digits[i] {
			t.Fatalf("digit %d = %d, want %d", i, got[i], digits[i])
		}
	}
	for r := 9; r <= MaxRes; r++ {
		if c.Digit(r) != 7 {
			t.Fatalf("unused digit %d = %d, want 7", r, c.Digit(r))
		}
	}
	if !c.IsValid() {
		t.Fatal("constructed cell is invalid")
	}
}

func TestCellResZero(t *testing.T) {
	c, err := NewCell(5, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsValid() || c.Res() != 0 || len(c.Digits()) != 0 {
		t.Fatalf("bad res-0 cell: %s", c)
	}
	if _, err := c.Parent(); err == nil {
		t.Fatal("res-0 parent must fail")
	}
}

func TestCellBoundaries(t *testing.T) {
	if _, err := NewCell(0, -1, nil); err == nil {
		t.Fatal("negative resolution accepted")
	}
	if _, err := NewCell(0, MaxRes+1, nil); err == nil {
		t.Fatal("resolution > MaxRes accepted")
	}
	if _, err := NewCell(-1, 0, nil); err == nil {
		t.Fatal("negative base cell accepted")
	}
	if _, err := NewCell(NumBaseCells, 0, nil); err == nil {
		t.Fatal("base cell 20 accepted")
	}
	if _, err := NewCell(0, 2, []uint8{1}); err == nil {
		t.Fatal("wrong digit count accepted")
	}
	if _, err := NewCell(0, 1, []uint8{7}); err == nil {
		t.Fatal("digit 7 accepted")
	}
	maxCell := MustCell(19, MaxRes, []uint8{6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6})
	if !maxCell.IsValid() {
		t.Fatal("max cell invalid")
	}
	if _, err := maxCell.Child(0); err == nil {
		t.Fatal("child of max-res cell must fail")
	}
	if _, err := maxCell.Children(); err == nil {
		t.Fatal("children of max-res cell must fail")
	}
	if _, err := maxCell.Parent(); err != nil {
		t.Fatalf("parent of max-res cell: %v", err)
	}
}

func TestCellStringParseRoundTrip(t *testing.T) {
	c := MustCell(17, 8, []uint8{1, 0, 1, 0, 4, 6, 3, 4})
	s := c.String()
	if strings.ContainsAny(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Fatalf("hex string not lowercase: %q", s)
	}
	back, err := ParseCell(s)
	if err != nil {
		t.Fatal(err)
	}
	if back != c {
		t.Fatalf("round trip: %s -> %s -> %s", c, s, back)
	}
	back, err = ParseCell("0x" + s)
	if err != nil || back != c {
		t.Fatalf("0x-prefixed parse failed: %v", err)
	}
}

func TestParseCellInvalid(t *testing.T) {
	for _, s := range []string{"", "zz", "0x", "ffffffffffffffff", "12345", "g"} {
		if _, err := ParseCell(s); err == nil {
			t.Fatalf("ParseCell(%q) succeeded, want error", s)
		}
	}
}

func TestCellIsValidRejectsCorruption(t *testing.T) {
	c := uint64(MustCell(3, 4, []uint8{1, 2, 3, 4}))
	corrupt := func(mask uint64) Cell { return Cell(c ^ mask) }
	cases := map[string]Cell{
		"top bit":        corrupt(1 << 63),
		"mode zero":      Cell(c &^ (0x7 << 56)),
		"spare bit 45":   corrupt(1 << 45),
		"spare bit 46":   corrupt(1 << 46),
		"base cell 31":   Cell(c&^(0x1f<<47) | 31<<47),
		"used digit 7":   Cell(c&^(0x7<<digitShift(1)) | 7<<digitShift(1)),
		"unused digit 0": Cell(c &^ (0x7 << digitShift(15))),
	}
	for name, bad := range cases {
		if bad.IsValid() {
			t.Fatalf("%s: corrupted cell accepted as valid", name)
		}
	}
}

func TestParentChildHierarchy(t *testing.T) {
	digits := []uint8{1, 0, 1, 0, 4, 6, 3, 4}
	c := MustCell(17, 8, digits)
	p, err := c.Parent()
	if err != nil {
		t.Fatal(err)
	}
	if p.Res() != 7 {
		t.Fatalf("parent res %d, want 7", p.Res())
	}
	for i, d := range digits[:7] {
		if p.Digit(i+1) != d {
			t.Fatalf("parent digit %d = %d, want %d", i, p.Digit(i+1), d)
		}
	}
	if p.Digit(8) != 7 {
		t.Fatal("parent digit 8 must be unused (7)")
	}
	kids, err := c.Children()
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 7 {
		t.Fatalf("%d children, want 7", len(kids))
	}
	seen := map[Cell]bool{}
	for d, k := range kids {
		if seen[k] {
			t.Fatalf("duplicate child %s", k)
		}
		seen[k] = true
		if k.Res() != 9 || k.Digit(9) != uint8(d) {
			t.Fatalf("child %d malformed: %s", d, k)
		}
		kp, err := k.Parent()
		if err != nil || kp != c {
			t.Fatalf("child parent mismatch: %v", err)
		}
	}
	if _, err := c.Child(7); err == nil {
		t.Fatal("child digit 7 accepted")
	}
}
