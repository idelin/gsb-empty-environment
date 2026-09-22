package framemux

import (
	"bytes"
	"errors"
	"testing"
)

func recvAll(c *Conn) []Frame {
	var fs []Frame
	for {
		f, ok := c.Recv()
		if !ok {
			return fs
		}
		fs = append(fs, f)
	}
}

// Byte input may be chunked anywhere; in particular the escape byte and the
// byte it escapes may land in different chunks.
func TestChunkedFeedSplitsEscapePair(t *testing.T) {
	payload := []byte{0x7D, 0x7E, 0x5A, 0x7B, 'x', 0x7D, 0x7D}
	a, b := NewConn(Config{}), NewConn(Config{})
	if err := a.Send(1, payload); err != nil {
		t.Fatal(err)
	}
	wire := a.TakeOutput()

	// Find a wire escape byte and confirm the parser is mid-escape right
	// after consuming it.
	esc := configForEpoch(0).Esc
	cut := bytes.IndexByte(wire, esc) + 1
	if cut <= 0 {
		t.Fatal("no escape byte on the wire")
	}
	if _, err := b.Write(wire[:cut]); err != nil {
		t.Fatalf("chunk1: %v", err)
	}
	if !b.p.escPending {
		t.Fatal("expected dangling escape state after chunk boundary")
	}
	if _, err := b.Write(wire[cut:]); err != nil {
		t.Fatalf("chunk2: %v", err)
	}
	fs := recvAll(b)
	if len(fs) != 1 || !bytes.Equal(fs[0].Payload, payload) {
		t.Fatalf("got %+v, want one frame with original payload", fs)
	}

	// Now feed the same frame one byte at a time.
	b2 := NewConn(Config{})
	sawPending := false
	for i, c := range wire {
		if _, err := b2.Write(wire[i : i+1 : i+1]); err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
		if b2.p.escPending {
			sawPending = true
		}
		_ = c
	}
	if !sawPending {
		t.Fatal("escape state never observed during byte-by-byte feed")
	}
	fs = recvAll(b2)
	if len(fs) != 1 || !bytes.Equal(fs[0].Payload, payload) {
		t.Fatalf("byte-by-byte: got %+v", fs)
	}
}

// Magic bytes inside a payload are escaped on the wire and must never be
// mistaken for a frame boundary.
func TestPseudoMagicInPayloadNotAFrame(t *testing.T) {
	var payload []byte
	for i := 0; i < 50; i++ {
		payload = append(payload, Magic0, Magic1, Magic0, Magic0, Magic1)
	}
	a, b := NewConn(Config{}), NewConn(Config{})
	if err := a.Send(1, payload); err != nil {
		t.Fatal(err)
	}
	if err := a.Send(1, []byte("after")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(a.TakeOutput()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	fs := recvAll(b)
	if len(fs) != 2 {
		t.Fatalf("got %d frames, want 2", len(fs))
	}
	if !bytes.Equal(fs[0].Payload, payload) || !bytes.Equal(fs[1].Payload, []byte("after")) {
		t.Fatal("payload mismatch")
	}
	if st := b.Stats(); st.BadFrames != 0 || st.Resyncs != 0 {
		t.Fatalf("false frame boundary detected: %+v", st)
	}
}

// After garbage containing a false delimiter, the parser resynchronizes in a
// bounded way and still delivers the next valid frame.
func TestResyncAfterFalseDelimiter(t *testing.T) {
	b := NewConn(Config{})
	garbage := append([]byte{Magic0, Magic1, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, []byte("junk")...)
	a := NewConn(Config{})
	if err := a.Send(2, []byte("real frame")); err != nil {
		t.Fatal(err)
	}
	input := append(garbage, a.TakeOutput()...)
	_, err := b.Write(input)
	if !errors.Is(err, ErrBadData) {
		t.Fatalf("got %v, want ErrBadData", err)
	}
	fs := recvAll(b)
	if len(fs) != 1 || !bytes.Equal(fs[0].Payload, []byte("real frame")) {
		t.Fatalf("got %+v after resync", fs)
	}
	if st := b.Stats(); st.BadFrames == 0 || st.Resyncs == 0 {
		t.Fatalf("expected resync activity: %+v", st)
	}
}

// A long delimiter-less input must keep memory flat and scanning bounded,
// surface ErrNoDelimiter, and not wedge the parser.
func TestOversizedDelimiterLessInputIsBounded(t *testing.T) {
	cfg := Config{MaxResyncScan: 2048}
	b := NewConn(cfg)
	garbage := bytes.Repeat([]byte{0x11}, 1<<20) // 1 MiB, no delimiter

	var noDelim int
	chunk := 4096
	for i := 0; i < len(garbage); i += chunk {
		end := i + chunk
		if end > len(garbage) {
			end = len(garbage)
		}
		if _, err := b.Write(garbage[i:end]); errors.Is(err, ErrNoDelimiter) {
			noDelim++
		}
	}
	if noDelim == 0 {
		t.Fatal("ErrNoDelimiter never surfaced")
	}
	st := b.Stats()
	if st.ScanBytes > int64(len(garbage))+int64(b.p.maxRaw) {
		t.Fatalf("scanning not bounded: scanned %d for %d input bytes", st.ScanBytes, len(garbage))
	}
	if len(b.p.in) != 0 || len(b.p.raw) != 0 {
		t.Fatalf("parser retained input: in=%d raw=%d", len(b.p.in), len(b.p.raw))
	}
	if st.MaxRawBuffer > b.p.maxRaw {
		t.Fatalf("raw buffer exceeded bound: %d > %d", st.MaxRawBuffer, b.p.maxRaw)
	}
	if b.BufferedBytes() != 0 {
		t.Fatalf("buffer quota consumed by garbage: %d", b.BufferedBytes())
	}

	// The parser must still accept a valid frame afterwards.
	a := NewConn(cfg)
	if err := a.Send(1, []byte("still alive")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(a.TakeOutput()); err != nil {
		t.Fatalf("post-garbage write: %v", err)
	}
	fs := recvAll(b)
	if len(fs) != 1 || !bytes.Equal(fs[0].Payload, []byte("still alive")) {
		t.Fatalf("got %+v after garbage storm", fs)
	}
}

// A header claiming a huge length is rejected as bad data before any payload
// buffer is allocated.
func TestHugeLengthRejectedBeforeAlloc(t *testing.T) {
	b := NewConn(Config{MaxPayload: 64})
	var hdr [headerLen]byte
	hdr[4], hdr[5] = 0xFF, 0xFF // len 65535
	input := append([]byte{Magic0, Magic1}, hdr[:]...)
	_, err := b.Write(input)
	if !errors.Is(err, ErrBadData) {
		t.Fatalf("got %v, want ErrBadData", err)
	}
	if b.p.payload != nil {
		t.Fatalf("payload buffer allocated before length check: %d bytes", len(b.p.payload))
	}
	if b.BufferedBytes() != 0 {
		t.Fatal("quota charged for rejected frame")
	}
}
