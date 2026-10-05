package locator

import (
	"math/rand"
	"reflect"
	"testing"
	"unicode/utf8"
)

// complexDoc exercises every terminator kind, multi-byte and astral runes,
// invalid UTF-8, and a missing trailing newline.
const complexDoc = "hello\r\n" +
	"wörld→🙂\n" +
	"lone\rcr\n" +
	"bad:\xff\xfe\x80ok\n" +
	"trunc:\xc3\n" +
	"trail:\r\n" +
	"last line no newline"

func buildInChunks(doc []byte, sizes []int) *Index {
	ix := New()
	pos := 0
	i := 0
	for pos < len(doc) {
		n := sizes[i%len(sizes)]
		if n > len(doc)-pos {
			n = len(doc) - pos
		}
		ix.Append(doc[pos : pos+n])
		pos += n
		i++
	}
	return ix
}

func snapshot(ix *Index) ([]int, []int) {
	return ix.lineStart, ix.invalid
}

func TestChunkingInvariance(t *testing.T) {
	doc := []byte(complexDoc)
	want := FromBytes(doc)
	wantStarts, wantInvalid := snapshot(want)

	chunkings := [][]int{
		{1}, {2}, {3}, {7}, {13}, {len(doc)},
		{1, 2, 3, 4, 5},
	}
	// Deterministic pseudo-random chunk sizes.
	rng := rand.New(rand.NewSource(42))
	randSizes := make([]int, 64)
	for i := range randSizes {
		randSizes[i] = 1 + rng.Intn(11)
	}
	chunkings = append(chunkings, randSizes)

	for _, sizes := range chunkings {
		got := buildInChunks(doc, sizes)
		gotStarts, gotInvalid := snapshot(got)
		if !reflect.DeepEqual(gotStarts, wantStarts) {
			t.Fatalf("chunk sizes %v: line starts %v, want %v", sizes, gotStarts, wantStarts)
		}
		if !reflect.DeepEqual(gotInvalid, wantInvalid) {
			t.Fatalf("chunk sizes %v: invalid %v, want %v", sizes, gotInvalid, wantInvalid)
		}
		// Every offset must locate identically.
		for off := 0; off <= len(doc); off++ {
			if g, w := got.Locate(off), want.Locate(off); g != w {
				t.Fatalf("chunk sizes %v: Locate(%d) = %+v, want %+v", sizes, off, g, w)
			}
		}
	}
}

func TestCRLFAcrossChunkBoundary(t *testing.T) {
	ix := New()
	ix.Append([]byte("a\r"))
	if got := ix.LineCount(); got != 2 {
		t.Fatalf("after CR: LineCount = %d, want 2", got)
	}
	ix.Append([]byte("\nb"))
	if got := ix.LineCount(); got != 2 {
		t.Fatalf("after LF: LineCount = %d, want 2 (CRLF is one terminator)", got)
	}
	if got := ix.String(); got != "a\r\nb" {
		t.Fatalf("text = %q", got)
	}
	if p := ix.Locate(3); p.Line != 2 || p.Column != 1 {
		t.Fatalf("Locate(3) = %+v, want line 2 col 1", p)
	}
	if s, e, ok := ix.LineBounds(1); !ok || s != 0 || e != 1 {
		t.Fatalf("LineBounds(1) = %d,%d,%v", s, e, ok)
	}
}

func TestNoTrailingNewline(t *testing.T) {
	ix := FromString("a\nb")
	if got := ix.LineCount(); got != 2 {
		t.Fatalf("LineCount = %d, want 2", got)
	}
	if s, e, ok := ix.LineBounds(2); !ok || s != 2 || e != 3 {
		t.Fatalf("LineBounds(2) = %d,%d,%v, want 2,3,true", s, e, ok)
	}
	if p := ix.Locate(3); p.Line != 2 || p.Column != 2 {
		t.Fatalf("Locate(end) = %+v, want line 2 col 2", p)
	}
	// Trailing terminator yields a final empty line.
	ix = FromString("a\n")
	if got := ix.LineCount(); got != 2 {
		t.Fatalf("LineCount = %d, want 2", got)
	}
	if s, e, ok := ix.LineBounds(2); !ok || s != 2 || e != 2 {
		t.Fatalf("LineBounds(2) = %d,%d,%v, want 2,2,true", s, e, ok)
	}
}

func TestInsertHalfCRLF(t *testing.T) {
	// CR first, LF inserted right after it.
	ix := FromString("ab")
	ix.Insert(1, []byte("\r"))
	if got := ix.LineCount(); got != 2 {
		t.Fatalf("after CR insert: LineCount = %d, want 2", got)
	}
	ix.Insert(2, []byte("\n"))
	if ix.String() != "a\r\nb" {
		t.Fatalf("text = %q", ix.String())
	}
	if got := ix.LineCount(); got != 2 {
		t.Fatalf("after LF insert: LineCount = %d, want 2", got)
	}
	if s, _, _ := ix.LineBounds(2); s != 3 {
		t.Fatalf("line 2 starts at %d, want 3", s)
	}

	// LF first, CR inserted right before it.
	ix = FromString("ab")
	ix.Insert(1, []byte("\n"))
	ix.Insert(1, []byte("\r"))
	if ix.String() != "a\r\nb" || ix.LineCount() != 2 {
		t.Fatalf("text = %q lines = %d", ix.String(), ix.LineCount())
	}
	if s, _, _ := ix.LineBounds(2); s != 3 {
		t.Fatalf("line 2 starts at %d, want 3", s)
	}

	// Deleting either half of a CRLF leaves a lone terminator: still 2 lines.
	ix = FromString("a\r\nb")
	ix.Delete(2, 3) // remove LF
	if ix.String() != "a\rb" || ix.LineCount() != 2 {
		t.Fatalf("after LF delete: %q lines = %d", ix.String(), ix.LineCount())
	}
	ix = FromString("a\r\nb")
	ix.Delete(1, 2) // remove CR
	if ix.String() != "a\nb" || ix.LineCount() != 2 {
		t.Fatalf("after CR delete: %q lines = %d", ix.String(), ix.LineCount())
	}
}

func TestInvalidUTF8PerByte(t *testing.T) {
	// \xff \xfe 'A' then a truncated 3-byte sequence \xe2\x82.
	doc := "\xff\xfeA\xe2\x82"
	ix := FromString(doc)
	if got := ix.LineCount(); got != 1 {
		t.Fatalf("LineCount = %d, want 1", got)
	}
	wantInvalid := []int{0, 1, 3, 4}
	if got := ix.InvalidOffsets(1); !reflect.DeepEqual(got, wantInvalid) {
		t.Fatalf("InvalidOffsets = %v, want %v", got, wantInvalid)
	}
	// Each bad byte is one replacement char: 5 scalar columns of content.
	for off, wantCol := range []int{1, 2, 3, 4, 5, 6} {
		p := ix.Locate(off)
		if p.Column != wantCol || p.ColumnUTF16 != wantCol {
			t.Fatalf("Locate(%d) = col %d/%d, want %d", off, p.Column, p.ColumnUTF16, wantCol)
		}
	}
	for _, off := range wantInvalid {
		if !ix.Locate(off).Replaced {
			t.Fatalf("Locate(%d).Replaced = false, want true", off)
		}
	}
	if ix.Locate(2).Replaced {
		t.Fatal("Locate(2).Replaced = true for 'A'")
	}

	// A valid rune split across two appends must not stay marked invalid.
	ix = New()
	ix.Append([]byte("\xc3"))
	if got := ix.InvalidOffsets(1); len(got) != 1 {
		t.Fatalf("after half rune: invalid = %v, want 1 mark", got)
	}
	ix.Append([]byte("\xa9")) // completes "é"
	if got := ix.InvalidOffsets(1); len(got) != 0 {
		t.Fatalf("after completed rune: invalid = %v, want none", got)
	}
	if p := ix.Locate(2); p.Column != 2 {
		t.Fatalf("Locate(2) col = %d, want 2", p.Column)
	}
}

func TestColumnsAstralAndAccents(t *testing.T) {
	ix := FromString("a🙂b")
	// 'a'=1 scalar/1 utf16, 🙂=1 scalar/2 utf16, 'b' follows.
	cases := []struct {
		off, col, col16 int
	}{
		{0, 1, 1},
		{1, 2, 2},
		{5, 3, 4},
		{6, 4, 5},
	}
	for _, c := range cases {
		p := ix.Locate(c.off)
		if p.Column != c.col || p.ColumnUTF16 != c.col16 {
			t.Fatalf("Locate(%d) = col %d utf16 %d, want %d/%d", c.off, p.Column, p.ColumnUTF16, c.col, c.col16)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	doc := []byte(complexDoc)
	ix := FromBytes(doc)
	for off := 0; off <= len(doc); off++ {
		// Terminator bytes belong to the line they end and have no
		// inverse coordinate; skip them.
		if off < len(doc) && (doc[off] == '\n' || doc[off] == '\r') {
			continue
		}
		pos := ix.Locate(off)
		back, ok := ix.Offset(pos.Line, pos.Column)
		if !ok {
			t.Fatalf("Offset(%d,%d) not ok", pos.Line, pos.Column)
		}
		back16, ok := ix.OffsetUTF16(pos.Line, pos.ColumnUTF16)
		if !ok {
			t.Fatalf("OffsetUTF16(%d,%d) not ok", pos.Line, pos.ColumnUTF16)
		}
		// Round trip lands on the start of the decoded unit (rune or
		// single bad byte) containing off.
		lineStart, _, _ := ix.LineBounds(pos.Line)
		want := lineStart
		for p := lineStart; p < off; {
			_, size := utf8.DecodeRune(doc[p:])
			if p+size > off {
				break
			}
			want = p + size
			p += size
		}
		if back != want || back16 != want {
			t.Fatalf("off %d -> %+v -> %d/%d, want %d", off, pos, back, back16, want)
		}
	}
}

func TestRoundTripOutOfRange(t *testing.T) {
	ix := FromString("ab\ncd")
	if _, ok := ix.Offset(0, 1); ok {
		t.Fatal("Offset(line 0) ok = true")
	}
	if _, ok := ix.Offset(3, 1); ok {
		t.Fatal("Offset(line 3) ok = true")
	}
	// Column past line end clamps to line end with ok=false.
	if off, ok := ix.Offset(1, 10); ok || off != 2 {
		t.Fatalf("Offset(1,10) = %d,%v, want 2,false", off, ok)
	}
	if off, ok := ix.OffsetUTF16(2, 10); ok || off != 5 {
		t.Fatalf("OffsetUTF16(2,10) = %d,%v, want 5,false", off, ok)
	}
	// Column exactly one past the last char is the valid line-end position.
	if off, ok := ix.Offset(1, 3); !ok || off != 2 {
		t.Fatalf("Offset(1,3) = %d,%v, want 2,true", off, ok)
	}
}

func TestTerminatorsAndBounds(t *testing.T) {
	ix := FromString("a\r\nb\rc\nd")
	wantStarts := []int{0, 3, 5, 7}
	if got := ix.lineStart; !reflect.DeepEqual(got, wantStarts) {
		t.Fatalf("line starts = %v, want %v", got, wantStarts)
	}
	wantBounds := [][2]int{{0, 1}, {3, 4}, {5, 6}, {7, 8}}
	for line := 1; line <= 4; line++ {
		s, e, ok := ix.LineBounds(line)
		if !ok || s != wantBounds[line-1][0] || e != wantBounds[line-1][1] {
			t.Fatalf("LineBounds(%d) = %d,%d,%v, want %v", line, s, e, ok, wantBounds[line-1])
		}
	}
}

func TestEditsShiftCoordinates(t *testing.T) {
	ix := FromString("one\ntwo\nthree")
	ix.Insert(4, []byte("🙂🙂\n")) // new line 2
	if got := ix.LineCount(); got != 4 {
		t.Fatalf("LineCount = %d, want 4", got)
	}
	if p := ix.Locate(ix.Len()); p.Line != 4 || p.Column != 6 {
		t.Fatalf("Locate(end) = %+v, want line 4 col 6", p)
	}
	ix.Delete(0, 4) // drop "one\n"
	if ix.String() != "🙂🙂\ntwo\nthree" {
		t.Fatalf("text = %q", ix.String())
	}
	if p := ix.Locate(0); p.Line != 1 || p.ColumnUTF16 != 1 {
		t.Fatalf("Locate(0) = %+v", p)
	}
	if p := ix.Locate(8); p.Line != 1 || p.Column != 3 || p.ColumnUTF16 != 5 {
		t.Fatalf("Locate(8) = %+v, want line 1 col 3 utf16 5", p)
	}
}

func TestEmpty(t *testing.T) {
	ix := New()
	if ix.LineCount() != 1 {
		t.Fatalf("LineCount = %d, want 1", ix.LineCount())
	}
	if p := ix.Locate(0); p.Line != 1 || p.Column != 1 || p.ColumnUTF16 != 1 {
		t.Fatalf("Locate(0) = %+v", p)
	}
	if p := ix.Locate(100); p.Offset != 0 { // clamped
		t.Fatalf("Locate(100).Offset = %d, want clamped 0", p.Offset)
	}
}
