package utf8chunk

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

// transform is the independent content oracle: invalid bytes become
// U+FFFD, everything else passes through unchanged.
func transform(src []byte) []byte {
	var out []byte
	for i := 0; i < len(src); {
		r, size := utf8.DecodeRune(src[i:])
		if r == utf8.RuneError && size == 1 {
			out = append(out, []byte("�")...)
		} else {
			out = append(out, src[i:i+size]...)
		}
		i += size
	}
	return out
}

// firstOversize returns the source offset and output size of the first
// character whose output encoding exceeds limit, or -1 if none.
func firstOversize(src []byte, limit int) (offset, size int) {
	for i := 0; i < len(src); {
		r, size := utf8.DecodeRune(src[i:])
		n := size
		if r == utf8.RuneError && size == 1 {
			n = 3 // U+FFFD
		}
		if n > limit {
			return i, n
		}
		i += size
	}
	return -1, 0
}

// check verifies content, provenance and budget for one (input, limit) pair.
func check(t *testing.T, src []byte, limit int) {
	t.Helper()
	chunks, err := Segment(src, limit)

	ovOff, ovSize := firstOversize(src, limit)
	if ovOff >= 0 {
		ov, ok := err.(*OversizeRuneError)
		if !ok {
			t.Fatalf("src=%q limit=%d: want *OversizeRuneError, got chunks=%v err=%v", src, limit, chunks, err)
		}
		if ov.SrcOffset != ovOff || ov.Size != ovSize || ov.Limit != limit {
			t.Fatalf("src=%q limit=%d: wrong oversize error %+v, want offset=%d size=%d", src, limit, ov, ovOff, ovSize)
		}
		return
	}
	if err != nil {
		t.Fatalf("src=%q limit=%d: unexpected error %v", src, limit, err)
	}

	want := transform(src)
	var got []byte
	pos := 0
	for i, c := range chunks {
		// Budget: non-empty and within limit.
		if len(c.Data) == 0 || len(c.Data) > limit {
			t.Fatalf("src=%q limit=%d: chunk %d has %d bytes", src, limit, i, len(c.Data))
		}
		if !utf8.Valid(c.Data) {
			t.Fatalf("src=%q limit=%d: chunk %d is not valid UTF-8: %q", src, limit, i, c.Data)
		}
		// Provenance: contiguous, in-order coverage of the source.
		if c.SrcOffset != pos || c.SrcLen <= 0 || c.SrcEnd() > len(src) {
			t.Fatalf("src=%q limit=%d: chunk %d bad provenance %+v, want offset %d", src, limit, i, c, pos)
		}
		// The covered source slice must transform into exactly this
		// chunk: proves cuts land on rune boundaries, so a straddling
		// sequence is never misread as bad bytes.
		if back := transform(src[c.SrcOffset:c.SrcEnd()]); !bytes.Equal(back, c.Data) {
			t.Fatalf("src=%q limit=%d: chunk %d data %q does not match source slice %q (transforms to %q)",
				src, limit, i, c.Data, src[c.SrcOffset:c.SrcEnd()], back)
		}
		got = append(got, c.Data...)
		pos = c.SrcEnd()
	}
	if pos != len(src) {
		t.Fatalf("src=%q limit=%d: chunks cover %d source bytes, want %d", src, limit, pos, len(src))
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("src=%q limit=%d: joined output %q, want %q", src, limit, got, want)
	}
}

// pieces exercise ASCII, 2/3/4-byte runes, lone continuation/lead bytes,
// truncated sequences, overlong encodings and surrogate halves.
var pieces = []string{
	"a",
	"é",            // 2 bytes
	"中",            // 3 bytes
	"😀",            // 4 bytes
	"�",            // genuine U+FFFD passes through
	"\xFF",         // invalid byte
	"\x80",         // lone continuation
	"\xC3",         // lone lead byte
	"\xE4\xB8",     // truncated 3-byte sequence
	"\xC0\xAF",     // overlong encoding
	"\xED\xA0\x80", // surrogate half
}

func TestEnumerate(t *testing.T) {
	inputs := []string{""}
	for _, p := range pieces {
		inputs = append(inputs, p)
	}
	for _, p1 := range pieces {
		for _, p2 := range pieces {
			inputs = append(inputs, p1+p2)
		}
	}
	for _, p1 := range pieces {
		for _, p2 := range pieces {
			for _, p3 := range pieces {
				inputs = append(inputs, p1+p2+p3)
			}
		}
	}
	for _, in := range inputs {
		for limit := 1; limit <= 10; limit++ {
			check(t, []byte(in), limit)
		}
	}
}

func TestTailStraddleIsNotBadByte(t *testing.T) {
	// "é" (0xC3 0xA9) straddles the budget: it must move whole into the
	// next chunk, not be truncated and replaced by U+FFFD.
	chunks, err := Segment([]byte("abé"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || string(chunks[0].Data) != "ab" || string(chunks[1].Data) != "é" {
		t.Fatalf("got %q", chunks)
	}
	if chunks[1].SrcOffset != 2 || chunks[1].SrcLen != 2 {
		t.Fatalf("bad provenance: %+v", chunks[1])
	}
}

func TestInvalidByteAtTail(t *testing.T) {
	chunks, err := Segment([]byte("ab\xFF"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || string(chunks[0].Data) != "ab" || string(chunks[1].Data) != "�" {
		t.Fatalf("got %q", chunks)
	}
	// Replacement expands to 3 output bytes but keeps 1-byte provenance.
	if chunks[1].SrcOffset != 2 || chunks[1].SrcLen != 1 {
		t.Fatalf("bad provenance: %+v", chunks[1])
	}
}

func TestOversizeRune(t *testing.T) {
	_, err := Segment([]byte("x😀y"), 3)
	ov, ok := err.(*OversizeRuneError)
	if !ok {
		t.Fatalf("want *OversizeRuneError, got %v", err)
	}
	if ov.SrcOffset != 1 || ov.Size != 4 || ov.Limit != 3 {
		t.Fatalf("wrong error fields: %+v", ov)
	}
	if !strings.Contains(ov.Error(), "offset 1") {
		t.Fatalf("unhelpful message: %q", ov.Error())
	}
}

func TestInvalidByteOversizeWhenLimitBelowThree(t *testing.T) {
	// U+FFFD needs 3 bytes, so a bad byte is oversize when limit < 3.
	_, err := Segment([]byte("\xFF"), 2)
	if _, ok := err.(*OversizeRuneError); !ok {
		t.Fatalf("want *OversizeRuneError, got %v", err)
	}
}

func TestEmptyAndInvalidLimit(t *testing.T) {
	chunks, err := Segment(nil, 4)
	if err != nil || len(chunks) != 0 {
		t.Fatalf("empty input: chunks=%v err=%v", chunks, err)
	}
	if _, err := Segment([]byte("a"), 0); err == nil {
		t.Fatal("limit 0 must be rejected")
	}
}
