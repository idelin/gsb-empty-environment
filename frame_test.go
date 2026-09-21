package seqwin

import (
	"bytes"
	"errors"
	"testing"
)

func buildStream(t *testing.T, payloads ...[]byte) []byte {
	t.Helper()
	var stream []byte
	for _, p := range payloads {
		f, err := EncodeFrame(p)
		if err != nil {
			t.Fatal(err)
		}
		stream = append(stream, f...)
	}
	return stream
}

func drain(t *testing.T, p *FrameParser) [][]byte {
	t.Helper()
	var out [][]byte
	for {
		payload, err := p.Next()
		if errors.Is(err, ErrNeedMore) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, payload)
	}
}

// Arbitrary chunking (down to 1 byte at a time) parses identically to
// feeding the whole stream at once.
func TestChunkedEqualsWhole(t *testing.T) {
	payloads := [][]byte{[]byte("hello"), {}, []byte("world!"), {1, 2, 3}}
	stream := buildStream(t, payloads...)

	whole := drain(t, func() *FrameParser {
		p := NewFrameParser(16)
		p.Write(stream)
		return p
	}())

	for _, size := range []int{1, 2, 3, 7} {
		p := NewFrameParser(16)
		var got [][]byte
		for i := 0; i < len(stream); i += size {
			end := i + size
			if end > len(stream) {
				end = len(stream)
			}
			p.Write(stream[i:end])
			got = append(got, drain(t, p)...)
		}
		if len(got) != len(whole) {
			t.Fatalf("chunk=%d: got %d frames; want %d", size, len(got), len(whole))
		}
		for i := range got {
			if !bytes.Equal(got[i], whole[i]) {
				t.Fatalf("chunk=%d frame %d: got %q; want %q", size, i, got[i], whole[i])
			}
		}
	}
}

// Incomplete input yields ErrNeedMore; a header declaring more than the
// quota yields FrameTooLargeError immediately, from the 2-byte header
// alone, before any payload bytes (or payload-sized allocation) exist.
func TestNeedMoreVsBadData(t *testing.T) {
	p := NewFrameParser(10)
	p.Write([]byte{0x00, 0x05}) // declares 5, none arrived yet
	if _, err := p.Next(); !errors.Is(err, ErrNeedMore) {
		t.Fatalf("err = %v; want ErrNeedMore", err)
	}
	p.Write([]byte("abc"))
	if _, err := p.Next(); !errors.Is(err, ErrNeedMore) {
		t.Fatalf("err = %v; want ErrNeedMore", err)
	}
	p.Write([]byte("de"))
	payload, err := p.Next()
	if err != nil || string(payload) != "abcde" {
		t.Fatalf("payload = %q, err = %v; want \"abcde\", nil", payload, err)
	}

	bad := NewFrameParser(10)
	bad.Write([]byte{0x00, 0x64}) // declares 100 > quota 10; no payload sent
	_, err = bad.Next()
	var tooLarge FrameTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v; want FrameTooLargeError", err)
	}
	if tooLarge.Len != 100 || tooLarge.Max != 10 {
		t.Fatalf("got %+v; want {Len:100 Max:10}", tooLarge)
	}
}

func TestMultipleFramesOneWrite(t *testing.T) {
	p := NewFrameParser(64)
	p.Write(buildStream(t, []byte("a"), []byte("bb"), []byte("ccc")))
	got := drain(t, p)
	if len(got) != 3 || string(got[0]) != "a" || string(got[1]) != "bb" || string(got[2]) != "ccc" {
		t.Fatalf("got %q", got)
	}
}
