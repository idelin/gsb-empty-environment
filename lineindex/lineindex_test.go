package lineindex

import (
	"math/rand"
	"testing"
	"unicode/utf8"
)

// reference implementation: recompute everything from scratch.

func refStarts(buf []byte) []int {
	starts := []int{0}
	for i := 0; i < len(buf); {
		switch buf[i] {
		case '\n':
			starts = append(starts, i+1)
			i++
		case '\r':
			if i+1 < len(buf) && buf[i+1] == '\n' {
				starts = append(starts, i+2)
				i += 2
			} else {
				starts = append(starts, i+1)
				i++
			}
		default:
			i++
		}
	}
	return starts
}

func refLocate(buf []byte, off int) Location {
	starts := refStarts(buf)
	line := 0
	for line+1 < len(starts) && starts[line+1] <= off {
		line++
	}
	scalar, u16 := 0, 0
	for i := starts[line]; i < off; {
		r, size := utf8.DecodeRune(buf[i:off])
		scalar++
		if r >= 0x10000 {
			u16 += 2
		} else {
			u16++
		}
		i += size
	}
	return Location{Offset: off, Line: line, ScalarCol: scalar, UTF16Col: u16}
}

func checkAgainstRef(t *testing.T, x *Index, buf []byte) {
	t.Helper()
	if string(x.Bytes()) != string(buf) {
		t.Fatalf("buffer mismatch: got %q want %q", x.Bytes(), buf)
	}
	starts := refStarts(buf)
	if x.LineCount() != len(starts) {
		t.Fatalf("LineCount = %d, want %d (buf %q)", x.LineCount(), len(starts), buf)
	}
	for off := 0; off <= len(buf); off++ {
		got := x.Locate(off)
		want := refLocate(buf, off)
		if got != want {
			t.Fatalf("Locate(%d) = %+v, want %+v (buf %q)", off, got, want, buf)
		}
	}
}

func TestTerminators(t *testing.T) {
	cases := []struct {
		in    string
		lines int
	}{
		{"", 1},
		{"a", 1},
		{"a\nb", 2},
		{"a\nb\n", 3},
		{"a\rb", 2},
		{"a\r\nb", 2},
		{"a\r\r\nb", 3},
		{"a\n\rb", 3},
		{"\r\n", 2},
		{"\r", 2},
		{"\n", 2},
		{"a\r\n\r\nb", 3},
	}
	for _, c := range cases {
		x := New()
		x.Append([]byte(c.in))
		if x.LineCount() != c.lines {
			t.Errorf("%q: LineCount = %d, want %d", c.in, x.LineCount(), c.lines)
		}
		checkAgainstRef(t, x, []byte(c.in))
	}
}

func TestNoTrailingNewline(t *testing.T) {
	x := New()
	x.Append([]byte("hello\nworld"))
	if got := x.Locate(8); got.Line != 1 || got.ScalarCol != 2 {
		t.Fatalf("Locate(8) = %+v, want line 1 col 2", got)
	}
	if got := x.Locate(11); got.Line != 1 || got.ScalarCol != 5 {
		t.Fatalf("Locate(11) = %+v, want line 1 col 5", got)
	}
	if off := x.Offset(1, 5); off != 11 {
		t.Fatalf("Offset(1,5) = %d, want 11", off)
	}
}

func TestCRLFSplitAcrossChunks(t *testing.T) {
	x := New()
	x.Append([]byte("ab\r"))
	checkAgainstRef(t, x, []byte("ab\r"))
	x.Append([]byte("\ncd"))
	checkAgainstRef(t, x, []byte("ab\r\ncd"))
	if x.LineCount() != 2 {
		t.Fatalf("LineCount = %d, want 2", x.LineCount())
	}
	if got := x.Locate(4); got.Line != 1 || got.ScalarCol != 0 {
		t.Fatalf("Locate(4) = %+v, want line 1 col 0", got)
	}
}

func TestCRNotFollowedByLF(t *testing.T) {
	x := New()
	x.Append([]byte("ab\r"))
	x.Append([]byte("cd"))
	checkAgainstRef(t, x, []byte("ab\rcd"))
	if x.LineCount() != 2 {
		t.Fatalf("LineCount = %d, want 2", x.LineCount())
	}
}

func TestInsertHalfCRLF(t *testing.T) {
	x := New()
	x.Append([]byte("ab"))
	x.Insert(2, []byte("\r"))
	checkAgainstRef(t, x, []byte("ab\r"))
	if x.LineCount() != 2 {
		t.Fatalf("after CR insert: LineCount = %d, want 2", x.LineCount())
	}
	x.Insert(3, []byte("\n"))
	checkAgainstRef(t, x, []byte("ab\r\n"))
	if x.LineCount() != 2 {
		t.Fatalf("after LF insert: LineCount = %d, want 2 (CRLF must merge)", x.LineCount())
	}

	y := New()
	y.Append([]byte("ab\ncd"))
	y.Insert(2, []byte("\r"))
	checkAgainstRef(t, y, []byte("ab\r\ncd"))
	if y.LineCount() != 2 {
		t.Fatalf("insert CR before LF: LineCount = %d, want 2", y.LineCount())
	}
}

func TestDeleteMergesCRLF(t *testing.T) {
	x := New()
	x.Append([]byte("a\rX\nb"))
	if x.LineCount() != 3 {
		t.Fatalf("LineCount = %d, want 3", x.LineCount())
	}
	x.Delete(2, 1)
	checkAgainstRef(t, x, []byte("a\r\nb"))
	if x.LineCount() != 2 {
		t.Fatalf("after delete: LineCount = %d, want 2", x.LineCount())
	}
}

func TestInvalidUTF8(t *testing.T) {
	buf := []byte{'a', 0x80, 0xFF, 'b', 0xC3}
	x := New()
	x.Append(buf)
	checkAgainstRef(t, x, buf)

	loc := x.Locate(3)
	if loc.ScalarCol != 3 || loc.UTF16Col != 3 {
		t.Fatalf("Locate(3) = %+v, want scalar/utf16 col 3 (two bad bytes = two replacements)", loc)
	}
	toks := x.LineTokens(0)
	wantInvalid := []bool{false, true, true, false, true}
	if len(toks) != len(wantInvalid) {
		t.Fatalf("got %d tokens, want %d", len(toks), len(wantInvalid))
	}
	for i, tok := range toks {
		if tok.Invalid != wantInvalid[i] {
			t.Errorf("token %d: Invalid = %v, want %v", i, tok.Invalid, wantInvalid[i])
		}
		if tok.Invalid && tok.Text != "�" {
			t.Errorf("token %d: invalid token text = %q, want U+FFFD", i, tok.Text)
		}
	}
	if toks[1].Width != 1 || toks[1].Offset != 1 {
		t.Errorf("bad-byte token: %+v, want Offset 1 Width 1", toks[1])
	}
}

func TestAstralAndUTF16Columns(t *testing.T) {
	x := New()
	x.Append([]byte("a\U0001F600z"))
	loc := x.Locate(5)
	if loc.ScalarCol != 2 || loc.UTF16Col != 3 {
		t.Fatalf("Locate(5) = %+v, want scalar 2 utf16 3", loc)
	}
	if off := x.Offset(0, 2); off != 5 {
		t.Fatalf("Offset(0,2) = %d, want 5", off)
	}
	if off := x.OffsetUTF16(0, 3); off != 5 {
		t.Fatalf("OffsetUTF16(0,3) = %d, want 5", off)
	}
	if off := x.OffsetUTF16(0, 2); off != 1 {
		t.Fatalf("OffsetUTF16(0,2) = %d, want 1 (inside surrogate pair clamps to rune start)", off)
	}
}

func TestRoundTrip(t *testing.T) {
	buf := []byte("αβ\r\nx�y\rz\n\U0001F600q")
	x := New()
	x.Append(buf)
	boundary := make([]bool, len(buf)+1)
	for i := 0; i < len(buf); {
		boundary[i] = true
		_, size := utf8.DecodeRune(buf[i:])
		i += size
	}
	boundary[len(buf)] = true
	for off := 0; off <= len(buf); off++ {
		if !boundary[off] {
			continue
		}
		if off > 0 && off < len(buf) && buf[off-1] == '\r' && buf[off] == '\n' {
			continue // inside a CRLF terminator: no stable round trip
		}
		loc := x.Locate(off)
		if got := x.Offset(loc.Line, loc.ScalarCol); got != loc.Offset {
			t.Errorf("scalar round trip at %d: Offset(%d,%d) = %d", off, loc.Line, loc.ScalarCol, got)
		}
		if got := x.OffsetUTF16(loc.Line, loc.UTF16Col); got != loc.Offset {
			t.Errorf("utf16 round trip at %d: OffsetUTF16(%d,%d) = %d", off, loc.Line, loc.UTF16Col, got)
		}
	}
}

func TestRandomChunking(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pieces := [][]byte{
		[]byte("hello\n"),
		[]byte("wörld\r\n"),
		[]byte("lone\rcr\n"),
		[]byte("bad:\x80\xfe\xff:\n"),
		[]byte("trunc:\xe2\x82\n"),
		[]byte("emoji \U0001F600 end"),
		[]byte("\r"),
		[]byte("\n"),
		[]byte("tail\r\n\r\n\rx"),
	}
	var full []byte
	for _, p := range pieces {
		full = append(full, p...)
	}
	for trial := 0; trial < 50; trial++ {
		x := New()
		pos := 0
		for pos < len(full) {
			n := 1 + rng.Intn(7)
			if pos+n > len(full) {
				n = len(full) - pos
			}
			x.Append(full[pos : pos+n])
			pos += n
		}
		checkAgainstRef(t, x, full)
	}
}

func TestRandomEdits(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alphabet := []byte("ab\n\r\xc3\xa9\xf0\x9f\x98\x80\x80\xff")
	var buf []byte
	x := New()
	for step := 0; step < 300; step++ {
		switch rng.Intn(3) {
		case 0, 1:
			n := 1 + rng.Intn(6)
			data := make([]byte, n)
			for i := range data {
				data[i] = alphabet[rng.Intn(len(alphabet))]
			}
			off := rng.Intn(len(buf) + 1)
			buf = append(buf[:off], append(data, buf[off:]...)...)
			x.Insert(off, data)
		case 2:
			if len(buf) == 0 {
				continue
			}
			off := rng.Intn(len(buf))
			n := 1 + rng.Intn(len(buf)-off)
			buf = append(buf[:off], buf[off+n:]...)
			x.Delete(off, n)
		}
		checkAgainstRef(t, x, buf)
	}
}

func TestClamp(t *testing.T) {
	x := New()
	x.Append([]byte("ab\ncd"))
	if got := x.Locate(-5); got.Offset != 0 || got.Line != 0 {
		t.Fatalf("Locate(-5) = %+v, want offset 0 line 0", got)
	}
	if got := x.Locate(100); got.Offset != 5 || got.Line != 1 || got.ScalarCol != 2 {
		t.Fatalf("Locate(100) = %+v, want offset 5 line 1 col 2", got)
	}
	if off := x.Offset(0, 99); off != 2 {
		t.Fatalf("Offset(0,99) = %d, want 2 (clamped to content end)", off)
	}
	if off := x.Offset(99, 0); off != 3 {
		t.Fatalf("Offset(99,0) = %d, want 3 (line clamped to last)", off)
	}
}
