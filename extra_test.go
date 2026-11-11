package ttfglyph

import (
	"bytes"
	"errors"
	"testing"
)

func TestShortLocaFormat(t *testing.T) {
	g := buildSimpleGlyph(
		[]Point{on(2, 0), on(4, 0), on(4, -3)},
		[]uint16{2},
	)
	data := buildTestFontWith([][]byte{nil, g}, fontOptions{shortLoca: true})
	f, err := New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	o, err := f.GlyphOutline(1)
	if err != nil {
		t.Fatalf("GlyphOutline: %v", err)
	}
	want := []Point{on(2, 0), on(4, 0), on(4, -3)}
	got := flatten(o)
	if len(got) != len(want) {
		t.Fatalf("points=%d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCmapFormat4Only(t *testing.T) {
	data := buildTestFontWith(nil, fontOptions{format4Only: true})
	f, err := New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := map[rune]uint16{
		'A': 1, 'C': 3, 'E': 5, 'P': 7, 'Z': 0, ' ': 0,
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
	// BMP-only cmap cannot map astral runes, but must not error.
	gid, err := f.GlyphIndex(0x1F600)
	if err != nil || gid != 0 {
		t.Errorf("astral lookup = (%d,%v), want (0,nil)", gid, err)
	}
}

func TestUnsupportedCmap(t *testing.T) {
	// A cmap whose only subtable is format 0 (not implemented).
	var cmap be16
	cmap.u16(0)
	cmap.u16(1)
	cmap.u16(0) // platform Unicode
	cmap.u16(0)
	cmap.u32(12)
	cmap.u16(0) // format 0
	cmap.u16(262)
	cmap.bytes(make([]byte, 258)...)
	data := buildTestFontWith(nil, fontOptions{cmapOverride: cmap.b})
	f, err := New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.GlyphIndex('A'); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestCorruptCmapLength(t *testing.T) {
	// Format 4 subtable claiming an impossible length.
	var cmap be16
	cmap.u16(0)
	cmap.u16(1)
	cmap.u16(3)
	cmap.u16(1)
	cmap.u32(12)
	cmap.u16(4)
	cmap.u16(9999) // length beyond table
	cmap.u16(0)
	data := buildTestFontWith(nil, fontOptions{cmapOverride: cmap.b})
	f, err := New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.GlyphIndex('A'); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestCorruptHeadFormat(t *testing.T) {
	head := make([]byte, 54)
	// Leave version 0 instead of 0x00010000.
	data := buildTestFontWith(nil, fontOptions{headOverride: head})
	f, err := New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.GlyphOutline(0); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt for bad head, got %v", err)
	}
}

func TestCorruptLocaIndexFormat(t *testing.T) {
	head := make([]byte, 54)
	copy(head[0:4], []byte{0x00, 0x01, 0x00, 0x00})
	// magicNumber
	head[12] = 0x5F
	head[13] = 0x0F
	head[14] = 0x3C
	head[15] = 0xF5
	head[50], head[51] = 0x00, 0x07 // invalid indexToLocFormat
	data := buildTestFontWith(nil, fontOptions{headOverride: head})
	f, err := New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.GlyphOutline(0); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt for bad loca format, got %v", err)
	}
}

func TestMissingTable(t *testing.T) {
	// Strip the hmtx table by overwriting its tag.
	data := buildTestFont([][]byte{nil, nil})
	idx := bytes.Index(data, []byte("hmtx"))
	if idx < 0 {
		t.Fatal("hmtx tag not found")
	}
	copy(data[idx:idx+4], "xxxx")
	f, err := New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.HMetrics(0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestCmapFormat12InvertedGroup(t *testing.T) {
	var cmap be16
	cmap.u16(0)
	cmap.u16(1)
	cmap.u16(3)
	cmap.u16(10)
	cmap.u32(12)
	cmap.u16(12)
	cmap.u16(0)
	cmap.u32(28) // length
	cmap.u32(0)  // language
	cmap.u32(1)  // numGroups
	cmap.u32(100)
	cmap.u32(99) // inverted range
	cmap.u32(1)
	data := buildTestFontWith(nil, fontOptions{cmapOverride: cmap.b})
	f, err := New(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.GlyphIndex('A'); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}
