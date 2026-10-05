package adaptivehuffman

import (
	"bytes"
	"testing"
)

// encodePayload encodes data with a fresh encoder and returns the payload
// bytes and the number of significant payload bits (before padding).
func encodePayload(t *testing.T, data []byte) (payload []byte, bits uint64) {
	t.Helper()
	enc := NewEncoder()
	buf := new(bytes.Buffer)
	bw := NewBitWriter(buf)
	for _, b := range data {
		if err := enc.EncodeSymbol(bw, b); err != nil {
			t.Fatal(err)
		}
	}
	bits = bw.BitsWritten()
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), bits
}

func makeFrame(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := EncodeFrame(&buf, data); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func asError(t *testing.T, err error) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("error %v (%T) is not an *adaptivehuffman.Error", err, err)
	}
	return e
}

// TestTruncatedFrame cuts a valid frame at every possible byte and requires
// an ErrKindTruncated error each time.
func TestTruncatedFrame(t *testing.T) {
	data := []byte("the quick brown fox jumps over the lazy dog")
	frame := makeFrame(t, data)
	for cut := 0; cut < len(frame); cut++ {
		_, err := DecodeFrame(bytes.NewReader(frame[:cut]))
		if err == nil {
			t.Fatalf("cut=%d: expected an error", cut)
		}
		e := asError(t, err)
		if e.Kind != ErrKindTruncated {
			t.Fatalf("cut=%d: kind %v, want %v", cut, e.Kind, ErrKindTruncated)
		}
		if e.Byte < 0 || e.Byte > int64(cut) {
			t.Fatalf("cut=%d: implausible error offset byte %d", cut, e.Byte)
		}
	}
}

// TestTruncatedRawStream truncates a raw symbol stream mid-codeword and
// checks the reported bit offset points at the start of the final,
// incomplete codeword.
func TestTruncatedRawStream(t *testing.T) {
	data := []byte("aaaaab")
	payload, bits := encodePayload(t, data)
	// Drop the final bit so the last codeword is incomplete.
	truncated := bits - 1
	fullBytes := int(truncated / 8)
	dec := NewDecoder()
	br := NewBitReader(bytes.NewReader(payload[:fullBytes]))
	var got []byte
	var err error
	var b byte
	for err == nil {
		b, err = dec.DecodeSymbol(br)
		if err == nil {
			got = append(got, b)
		}
	}
	e := asError(t, err)
	if e.Kind != ErrKindTruncated {
		t.Fatalf("kind %v, want %v", e.Kind, ErrKindTruncated)
	}
	if len(got) >= len(data) {
		t.Fatalf("decoded %d symbols from a truncated stream", len(got))
	}
	// The error must be reported at the bit position where the incomplete
	// codeword started, which cannot be beyond the truncation point.
	if e.Byte*8+int64(e.Bit) > int64(truncated) {
		t.Fatalf("error offset %d.%d beyond truncation point %d", e.Byte, e.Bit, truncated)
	}
}

// TestNonZeroPadding flips the first padding bit of a frame and requires an
// ErrKindNonZeroPadding error at exactly that byte/bit offset.
func TestNonZeroPadding(t *testing.T) {
	// Find an input whose payload is not byte-aligned.
	var data []byte
	var bits uint64
	for n := 1; n < 64; n++ {
		data = []byte("hello world")[:n%11+1]
		_, bits = encodePayload(t, data)
		if bits%8 != 0 {
			break
		}
	}
	if bits%8 == 0 {
		t.Fatal("could not find a misaligned payload")
	}
	frame := makeFrame(t, data)
	byteIdx := FrameHeaderSize + int(bits/8)
	bitIdx := uint(bits % 8)
	frame[byteIdx] |= 1 << (7 - bitIdx)

	_, err := DecodeFrame(bytes.NewReader(frame))
	if err == nil {
		t.Fatal("expected an error for non-zero padding")
	}
	e := asError(t, err)
	if e.Kind != ErrKindNonZeroPadding {
		t.Fatalf("kind %v, want %v", e.Kind, ErrKindNonZeroPadding)
	}
	if e.Byte != int64(byteIdx) || e.Bit != uint8(bitIdx) {
		t.Fatalf("offset %d.%d, want %d.%d", e.Byte, e.Bit, byteIdx, bitIdx)
	}
}

// TestTrailingData appends one garbage byte to a frame and requires an
// ErrKindTrailingData error at exactly that byte offset.
func TestTrailingData(t *testing.T) {
	data := []byte("some sensor readings")
	frame := makeFrame(t, data)
	frame = append(frame, 0x00)
	_, err := DecodeFrame(bytes.NewReader(frame))
	if err == nil {
		t.Fatal("expected an error for trailing data")
	}
	e := asError(t, err)
	if e.Kind != ErrKindTrailingData {
		t.Fatalf("kind %v, want %v", e.Kind, ErrKindTrailingData)
	}
	if want := int64(len(frame) - 1); e.Byte != want || e.Bit != 0 {
		t.Fatalf("offset %d.%d, want %d.0", e.Byte, e.Bit, want)
	}
}

// writeNodePath writes the current code of node n to bw (test helper
// mirroring the encoder's path encoding).
func writeNodePath(bw *BitWriter, n *node) {
	var path []byte
	for p := n; p.parent != nil; p = p.parent {
		b := byte(0)
		if p.parent.right == p {
			b = 1
		}
		path = append(path, b)
	}
	for i := len(path) - 1; i >= 0; i-- {
		bw.WriteBit(uint32(path[i]))
	}
}

// TestInvalidEscapeFullAlphabet feeds an escape codeword after all 256
// symbols are already known.
func TestInvalidEscapeFullAlphabet(t *testing.T) {
	enc := NewEncoder()
	buf := new(bytes.Buffer)
	bw := NewBitWriter(buf)
	for i := 0; i < 256; i++ {
		if err := enc.EncodeSymbol(bw, byte(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Manually append an escape code plus a raw byte: illegal now.
	writeNodePath(bw, enc.m.nyt)
	bw.WriteBits(0x55, 8)
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}

	dec := NewDecoder()
	br := NewBitReader(bytes.NewReader(buf.Bytes()))
	for i := 0; i < 256; i++ {
		got, err := dec.DecodeSymbol(br)
		if err != nil {
			t.Fatalf("symbol %d: %v", i, err)
		}
		if got != byte(i) {
			t.Fatalf("symbol %d: got 0x%02x", i, got)
		}
	}
	_, err := dec.DecodeSymbol(br)
	e := asError(t, err)
	if e.Kind != ErrKindInvalidEscape {
		t.Fatalf("kind %v, want %v", e.Kind, ErrKindInvalidEscape)
	}
}

// TestInvalidEscapeKnownSymbol feeds an escape codeword whose raw byte
// already has a codeword.
func TestInvalidEscapeKnownSymbol(t *testing.T) {
	enc := NewEncoder()
	buf := new(bytes.Buffer)
	bw := NewBitWriter(buf)
	if err := enc.EncodeSymbol(bw, 'A'); err != nil { // escape + raw 'A'
		t.Fatal(err)
	}
	// Manually emit escape + raw 'A' again: 'A' is already known.
	writeNodePath(bw, enc.m.nyt)
	bw.WriteBits('A', 8)
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}

	dec := NewDecoder()
	br := NewBitReader(bytes.NewReader(buf.Bytes()))
	if got, err := dec.DecodeSymbol(br); err != nil || got != 'A' {
		t.Fatalf("first symbol: got %q, err %v", got, err)
	}
	_, err := dec.DecodeSymbol(br)
	e := asError(t, err)
	if e.Kind != ErrKindInvalidEscape {
		t.Fatalf("kind %v, want %v", e.Kind, ErrKindInvalidEscape)
	}
	if e.Bit != 0 || e.Byte < 0 {
		t.Fatalf("implausible offset %d.%d", e.Byte, e.Bit)
	}
}

func TestBadHeader(t *testing.T) {
	frame := makeFrame(t, []byte("payload"))

	bad := append([]byte(nil), frame...)
	bad[0] ^= 0xFF
	if _, err := DecodeFrame(bytes.NewReader(bad)); err == nil {
		t.Fatal("expected bad magic error")
	} else if e := asError(t, err); e.Kind != ErrKindBadMagic || e.Byte != 0 {
		t.Fatalf("got %v", e)
	}

	bad = append([]byte(nil), frame...)
	bad[4] = 0xFF
	if _, err := DecodeFrame(bytes.NewReader(bad)); err == nil {
		t.Fatal("expected bad version error")
	} else if e := asError(t, err); e.Kind != ErrKindBadVersion || e.Byte != 4 {
		t.Fatalf("got %v", e)
	}
}
