package ttfglyph

import (
	"bytes"
	"errors"
	"testing"
)

// on builds an on-curve point.
func on(x, y int32) Point { return Point{X: x, Y: y, OnCurve: true} }

// off builds an off-curve control point.
func off(x, y int32) Point { return Point{X: x, Y: y, OnCurve: false} }

func flatten(o *Outline) []Point {
	var pts []Point
	for _, c := range o.Contours {
		pts = append(pts, c.Points...)
	}
	return pts
}

func testFont(t *testing.T) (*Font, [][]byte) {
	t.Helper()
	square := []Point{
		on(10, 0), off(20, 0), on(20, 10), on(20, 20),
		on(10, 20), on(0, 20), on(0, 10), on(0, 0),
	}
	triangle := []Point{off(0, -10), off(10, -10), on(5, -19)}
	// Two consecutive off-curve points (1,2) force an implicit midpoint.
	midpointGlyph := []Point{
		on(0, 0), off(10, 0), off(20, 10), on(20, 20),
	}

	g1 := buildSimpleGlyph(square, []uint16{7})
	g2 := buildSimpleGlyph(triangle, []uint16{2})
	gMid := buildSimpleGlyph(midpointGlyph, []uint16{3})
	// Glyph 3: uniform scale 0.5 of glyph 1, translated by (100, 200).
	g3 := buildCompositeGlyph([]compDesc{{
		glyphIndex: 1, arg1: 100, arg2: 200, argsAreXY: true, words: true,
		scale: []int16{8192}, // 0.5 in F2Dot14
	}}, [4]int16{100, 200, 110, 210})
	// Glyph 4: independent x/y scales (x2, x0.5), no translation.
	g4 := buildCompositeGlyph([]compDesc{{
		glyphIndex: 1, argsAreXY: true,
		scale: []int16{16384, 8192}, // 1.0, 0.5
	}}, [4]int16{0, 0, 40, 10})
	// Glyph 5: 2x2 matrix swapping x and y (B=1, C=1).
	g5 := buildCompositeGlyph([]compDesc{{
		glyphIndex: 1, argsAreXY: true, words: true, arg1: 50, arg2: 60,
		scale: []int16{0, 16384, 16384, 0}, // A=0 B=1 C=1 D=0
	}}, [4]int16{50, 60, 70, 80})
	// Glyph 6: point alignment. Attach glyph 2's point 0 to glyph 1's point 0.
	g6 := buildCompositeGlyph([]compDesc{
		{glyphIndex: 1, argsAreXY: true, words: true}, // base square at offset (0,0)
		{glyphIndex: 2, argsAreXY: false, words: false, arg1: 0, arg2: 0},
	}, [4]int16{0, -10, 20, 20})
	// Glyph 7: self-referencing composite to exercise the recursion cap.
	g7 := buildCompositeGlyph([]compDesc{{
		glyphIndex: 7, argsAreXY: true, words: true,
	}}, [4]int16{0, 0, 0, 0})
	// Glyph 7b: a composite carrying an instruction stream must still parse.
	gInstr := buildCompositeGlyph([]compDesc{{
		glyphIndex: 1, argsAreXY: true, words: true,
		instr: []byte{0xAA, 0xBB, 0xCC},
	}}, [4]int16{0, 0, 20, 20})
	// Nested composite: wraps glyph 3 (which itself wraps glyph 1 at 0.5).
	gNested := buildCompositeGlyph([]compDesc{{
		glyphIndex: 3, argsAreXY: true, words: true, arg1: 1, arg2: 2,
	}}, [4]int16{101, 202, 111, 212})
	// Glyph 8: truncated simple glyph (bbox only, no endPts).
	var trunc be16
	trunc.i16(1)
	trunc.i16(0)
	trunc.i16(0)
	// Glyph 9: point cap: declares endPts[0] beyond maxPoints.
	var huge be16
	huge.i16(1)
	for i := 0; i < 4; i++ {
		huge.i16(0)
	}
	huge.u16(maxPoints + 5)

	blocks := [][]byte{
		nil, // glyph 0: empty (.notdef)
		g1, g2, g3, g4, g5, g6, g7, trunc.b, huge.b,
		gMid,
		gInstr,
		gNested,
	}
	data := buildTestFont(blocks)
	f, err := New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f, blocks
}

func TestNewRejectsTruncatedFont(t *testing.T) {
	if _, err := New(bytes.NewReader([]byte{0, 1, 0, 0}), 4); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected ErrCorrupt for tiny font, got %v", err)
	}
}

func TestNewRejectsTablePastEOF(t *testing.T) {
	var w be16
	w.u32(0x00010000)
	w.u16(1)
	w.u16(16)
	w.u16(0)
	w.u16(0)
	w.bytes('h', 'e', 'a', 'd')
	w.u32(0)
	w.u32(1000) // offset beyond EOF
	w.u32(54)
	_, err := New(bytes.NewReader(w.b), int64(len(w.b)))
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected ErrCorrupt for out-of-bounds table, got %v", err)
	}
}

func TestCmapLookups(t *testing.T) {
	f, _ := testFont(t)
	cases := map[rune]uint16{
		'A': 1, 'B': 2, 'C': 3, 'D': 4, // format-4 delta segments
		'E':     5, // second format-4 segment
		'P':     7, // format-4 idRangeOffset path
		' ':     0, // maps to glyph 0
		'Z':     0, // not in any segment
		0x1F600: 6, // format-12-only rune
		0x20000: 0, // not in format 12
	}
	for r, want := range cases {
		got, err := f.GlyphIndex(r)
		if err != nil {
			t.Fatalf("GlyphIndex(%q): %v", r, err)
		}
		if got != want {
			t.Errorf("GlyphIndex(%q)=%d, want %d", r, got, want)
		}
	}
}

func TestSimpleGlyphSquare(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(1)
	if err != nil {
		t.Fatalf("GlyphOutline: %v", err)
	}
	if len(o.Contours) != 1 {
		t.Fatalf("contours=%d, want 1", len(o.Contours))
	}
	want := []Point{
		on(10, 0), off(20, 0), on(20, 10), on(20, 20),
		on(10, 20), on(0, 20), on(0, 10), on(0, 0),
	}
	got := o.Contours[0].Points
	if len(got) != len(want) {
		t.Fatalf("points=%d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got[1].OnCurve {
		t.Errorf("control point must remain off-curve")
	}
}

func TestSimpleGlyphAllOffCurve(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(2)
	if err != nil {
		t.Fatalf("GlyphOutline: %v", err)
	}
	got := flatten(o)
	want := []Point{
		on(5, -19), // the sole on-curve point; expansion starts here
		off(0, -10),
		on(5, -10), // implicit midpoint p0,p1: (0+10,-10-10)/2
		off(10, -10),
	}
	// 1 original on-curve + 2 off-curve + 1 implicit midpoint = 4 points.
	if len(got) != len(want) {
		t.Fatalf("points=%d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCompositeUniformScale(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(3)
	if err != nil {
		t.Fatalf("GlyphOutline: %v", err)
	}
	got := flatten(o)
	// Half scale then translate (100,200); midpoint inserted at (115,205).
	wantOn := []Point{
		{X: 105, Y: 200, OnCurve: true},
		{X: 110, Y: 200, OnCurve: false},
		{X: 110, Y: 205, OnCurve: true},
		{X: 110, Y: 210, OnCurve: true},
		{X: 105, Y: 210, OnCurve: true},
		{X: 100, Y: 210, OnCurve: true},
		{X: 100, Y: 205, OnCurve: true},
		{X: 100, Y: 200, OnCurve: true},
	}
	if len(got) != len(wantOn) {
		t.Fatalf("points=%d, want %d: %+v", len(got), len(wantOn), got)
	}
	for i := range wantOn {
		if got[i] != wantOn[i] {
			t.Errorf("point %d = %+v, want %+v", i, got[i], wantOn[i])
		}
	}
}

func TestCompositeXYScale(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(4)
	if err != nil {
		t.Fatalf("GlyphOutline: %v", err)
	}
	got := flatten(o)
	// (x*1.0, y*0.5): glyph 1's first point (10,0) stays (10,0).
	if got[0] != (Point{X: 10, Y: 0, OnCurve: true}) {
		t.Fatalf("first point = %+v, want (10,0) on-curve", got[0])
	}
	// (20,20) -> (20,10), still on-curve, index 3 in the 8-point contour.
	if got[3] != (Point{X: 20, Y: 10, OnCurve: true}) {
		t.Errorf("point 3 = %+v, want (20,10)", got[3])
	}
}

func TestImplicitMidpointInsertion(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(10)
	if err != nil {
		t.Fatalf("GlyphOutline: %v", err)
	}
	got := flatten(o)
	// Raw: on(0,0), off(10,0), off(20,10), on(20,20).
	// The off/off edge (10,0)->(20,10) gains midpoint (15,5).
	want := []Point{
		on(0, 0),
		off(10, 0),
		on(15, 5),
		off(20, 10),
		on(20, 20),
	}
	if len(got) != len(want) {
		t.Fatalf("points=%d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCompositeWithInstructions(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(11)
	if err != nil {
		t.Fatalf("GlyphOutline with instructions: %v", err)
	}
	if len(o.Contours) != 1 || len(o.Contours[0].Points) == 0 {
		t.Fatalf("expected one non-empty contour, got %+v", o.Contours)
	}
}

func TestNestedComposite(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(12)
	if err != nil {
		t.Fatalf("GlyphOutline nested: %v", err)
	}
	got := flatten(o)
	// Glyph 1's (10,0) -> glyph 3: (105,200) -> nested: identity +(1,2).
	if got[0] != (Point{X: 106, Y: 202, OnCurve: true}) {
		t.Errorf("nested first point = %+v, want (106,202)", got[0])
	}
}

func TestComposite2x2Matrix(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(5)
	if err != nil {
		t.Fatalf("GlyphOutline: %v", err)
	}
	got := flatten(o)
	// Matrix swaps axes: (x,y)->(y,x), then translate (50,60).
	// Glyph 1 point (10,0) becomes (0,10)+(50,60) = (50,70).
	if got[0] != (Point{X: 50, Y: 70, OnCurve: true}) {
		t.Errorf("first point = %+v, want (50,70)", got[0])
	}
	// Off-curve (20,0) -> (0,20)+(50,60) = (50,80).
	if got[1] != (Point{X: 50, Y: 80, OnCurve: false}) {
		t.Errorf("second point = %+v, want off-curve (50,80)", got[1])
	}
}

func TestCompositePointAlignment(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(6)
	if err != nil {
		t.Fatalf("GlyphOutline: %v", err)
	}
	if len(o.Contours) != 2 {
		t.Fatalf("contours=%d, want 2", len(o.Contours))
	}
	// First component is glyph 1 unchanged: its point 0 is (10,0).
	if o.Contours[0].Points[0] != (Point{X: 10, Y: 0, OnCurve: true}) {
		t.Fatalf("base point = %+v, want (10,0)", o.Contours[0].Points[0])
	}
	// Triangle point 0 is (0,-10); identity transform, so dx=10-0=10,
	// dy=0-(-10)=10. Its first expanded point is midpoint (5,-10)+(10,10).
	first := o.Contours[1].Points[0]
	// Expansion runs on the shifted contour, whose only original on-curve
	// point (15,-9) remains the start; verify alignment kept that anchor.
	if first != (Point{X: 15, Y: -9, OnCurve: true}) {
		t.Errorf("aligned first point = %+v, want on-curve (15,-9)", first)
	}
}

func TestRecursionDepthLimit(t *testing.T) {
	f, _ := testFont(t)
	_, err := f.GlyphOutline(7)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected ErrCorrupt on recursive composite, got %v", err)
	}
}

func TestTruncatedGlyph(t *testing.T) {
	f, _ := testFont(t)
	_, err := f.GlyphOutline(8)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected ErrCorrupt on truncated glyph, got %v", err)
	}
}

func TestPointCap(t *testing.T) {
	f, _ := testFont(t)
	_, err := f.GlyphOutline(9)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected ErrCorrupt on oversized glyph, got %v", err)
	}
}

func TestEmptyGlyph(t *testing.T) {
	f, _ := testFont(t)
	o, err := f.GlyphOutline(0)
	if err != nil {
		t.Fatalf("GlyphOutline(0): %v", err)
	}
	if len(o.Contours) != 0 {
		t.Fatalf("empty glyph has %d contours", len(o.Contours))
	}
}

func TestHMetrics(t *testing.T) {
	f, _ := testFont(t)
	m, err := f.HMetrics(2)
	if err != nil {
		t.Fatalf("HMetrics: %v", err)
	}
	if m.AdvanceWidth != 520 || m.LeftSideBearing != 12 {
		t.Errorf("metrics(2) = %+v, want {520 12}", m)
	}
	// Glyphs past numberOfHMetrics reuse the last advance width.
	m9, err := f.HMetrics(9)
	if err != nil {
		t.Fatalf("HMetrics(9): %v", err)
	}
	if m9.AdvanceWidth != 550 || m9.LeftSideBearing != -14 {
		t.Errorf("metrics(9) = %+v, want {550 -14}", m9)
	}
}

func TestGlyphIndexOutOfRange(t *testing.T) {
	f, _ := testFont(t)
	if _, err := f.GlyphOutline(99); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected ErrCorrupt, got %v", err)
	}
	if _, err := f.HMetrics(99); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected ErrCorrupt from HMetrics, got %v", err)
	}
}

func TestReaderAtErrorsSurface(t *testing.T) {
	// A ReaderAt that always fails must surface the error instead of panicking.
	f, err := New(errReaderAt{}, 1024)
	if err == nil {
		t.Fatalf("expected error from failing ReaderAt")
	}
	_ = f
}

type errReaderAt struct{}

func (errReaderAt) ReadAt(p []byte, off int64) (int, error) {
	return 0, errors.New("boom")
}
