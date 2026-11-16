package netsec

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestParserHasEventsResetAndEnd(t *testing.T) {
	p := NewParser(0)
	if p.HasEvents() {
		t.Fatal("fresh parser should have no events")
	}
	if err := p.Feed(frame("abc")); err != nil {
		t.Fatal(err)
	}
	if !p.HasEvents() {
		t.Fatal("parser should report a buffered frame")
	}
	fr, err := p.Next()
	if err != nil || string(fr) != "abc" {
		t.Fatalf("Next = %q, %v", fr, err)
	}
	if p.HasEvents() {
		t.Fatal("queue should be drained")
	}
	if err := p.End(); !errors.Is(err, io.EOF) {
		t.Fatalf("clean boundary End = %v", err)
	}

	// End while skipping an oversized frame is unexpected EOF.
	p2 := NewParser(2)
	_ = p2.Feed([]byte{VT, 'a', 'b', 'c', 'd'})
	if err := p2.End(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("mid-skip End = %v, want ErrUnexpectedEOF", err)
	}

	// End immediately after an FS (trailer split across the connection close).
	p3 := NewParser(0)
	_ = p3.Feed([]byte{VT, 'a', FS})
	if err := p3.End(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("post-FS End = %v, want ErrUnexpectedEOF", err)
	}

	// A dead parser comes back only via Reset.
	_ = p.Feed([]byte{'X'})
	if _, err := p.Next(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("want ErrProtocol, got %v", err)
	}
	if err := p.End(); !errors.Is(err, ErrProtocol) {
		t.Fatalf("End on a dead parser must stay fatal, got %v", err)
	}
	p.Reset()
	if p.HasEvents() {
		t.Fatal("Reset must clear the sticky error")
	}
	if err := p.Feed(frame("ok")); err != nil {
		t.Fatalf("parser unusable after Reset: %v", err)
	}
}

func TestFrameReaderDefaultConstructorAndParser(t *testing.T) {
	stream := bytes.Join([][]byte{frame("a"), frame("b")}, nil)
	fr := NewFrameReader(bytes.NewReader(stream), 0)
	if cap(fr.buf) != DefaultReadChunkSize {
		t.Fatalf("default chunk size = %d, want %d", cap(fr.buf), DefaultReadChunkSize)
	}
	if fr.Parser() == nil {
		t.Fatal("Parser() returned nil")
	}
	first, err := fr.ReadFrame()
	if err != nil || string(first) != "a" {
		t.Fatalf("first = %q, %v", first, err)
	}

	// Non-positive chunk size falls back to the default too.
	fr2 := NewFrameReaderWithChunkSize(bytes.NewReader(stream), 0, -1)
	if cap(fr2.buf) != DefaultReadChunkSize {
		t.Fatal("non-positive chunk size should fall back to default")
	}
}

func TestEscapeAdditionalBranches(t *testing.T) {
	rules := DefaultFieldRules()
	malformed := []string{
		`\`,     // lone escape
		`abc\`,  // trailing lone escape
		`\X\`,   // hex with no digits
		`\X1\`,  // odd hex digit count
		`\Xg0\`, // non-hex digit
		`\Q\`,   // unknown single letter
		`\AB\`,  // unknown multi letter
		`\\`,    // empty escape body
		`\X1G\`, // second nibble invalid
	}
	for _, input := range malformed {
		if _, err := UnescapeFieldWithRules([]byte(input), rules); !errors.Is(err, ErrEscape) {
			t.Fatalf("input %q: want ErrEscape, got %v", input, err)
		}
	}
	// Hex escapes decoding to framing bytes are rejected even in a longer word.
	if _, err := UnescapeField([]byte(`xx\X0b\yy`)); !errors.Is(err, ErrEscape) {
		t.Fatalf("lowercase hex VT escape must be rejected")
	}
}
