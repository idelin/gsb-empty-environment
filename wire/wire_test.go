package wire

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var e Encoder
	e.Uvarint(300)
	e.Varint(-42)
	e.Bool(true)
	e.String("hello")
	e.Bytes([]byte{1, 2, 3})
	d := NewDecoder(e.Out())
	if v, err := d.Uvarint(); err != nil || v != 300 {
		t.Fatalf("uvarint = %v, %v", v, err)
	}
	if v, err := d.Varint(); err != nil || v != -42 {
		t.Fatalf("varint = %v, %v", v, err)
	}
	if v, err := d.Bool(); err != nil || !v {
		t.Fatalf("bool = %v, %v", v, err)
	}
	if v, err := d.String(); err != nil || v != "hello" {
		t.Fatalf("string = %q, %v", v, err)
	}
	if v, err := d.Bytes(); err != nil || !bytes.Equal(v, []byte{1, 2, 3}) {
		t.Fatalf("bytes = %v, %v", v, err)
	}
	if err := d.Done(); err != nil {
		t.Fatalf("done: %v", err)
	}
}

// TestLengthCheckedBeforeAllocation feeds a huge declared length with a
// tiny buffer: the decoder must reject before allocating anything.
func TestLengthCheckedBeforeAllocation(t *testing.T) {
	var e Encoder
	e.Uvarint(1 << 40) // absurd string length, no payload
	d := NewDecoder(e.Out())
	if _, err := d.String(); err != ErrLength {
		t.Fatalf("expected ErrLength, got %v", err)
	}

	e.Reset()
	e.Uvarint(MaxStringLen) // within the hard limit but no payload behind it
	d = NewDecoder(e.Out())
	if _, err := d.String(); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}

	e.Reset()
	e.Uvarint(uint64(MaxCount) + 1)
	d = NewDecoder(e.Out())
	if _, err := d.Count(MaxCount); err != ErrLength {
		t.Fatalf("expected ErrLength, got %v", err)
	}
}

func TestCountBoundedByRemaining(t *testing.T) {
	// Count claims 100 elements but only 3 bytes remain: each element needs
	// at least one byte, so this is impossible and must be rejected.
	var e Encoder
	e.Uvarint(100)
	e.Bytes([]byte{1, 2, 3})
	d := NewDecoder(e.Out())
	if _, err := d.Count(MaxCount); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
}

func TestTruncatedVarint(t *testing.T) {
	d := NewDecoder([]byte{0x80}) // continuation bit set, no next byte
	if _, err := d.Uvarint(); err != ErrTruncated {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	d := NewDecoder([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f})
	if _, err := d.Uvarint(); err != ErrOverflow {
		t.Fatalf("expected ErrOverflow, got %v", err)
	}
}

func TestTrailingBytesRejected(t *testing.T) {
	var e Encoder
	e.Uvarint(1)
	e.Uvarint(2)
	d := NewDecoder(e.Out())
	if _, err := d.Uvarint(); err != nil {
		t.Fatal(err)
	}
	if err := d.Done(); err != ErrTrailing {
		t.Fatalf("expected ErrTrailing, got %v", err)
	}
}

func TestDepthLimit(t *testing.T) {
	d := NewDecoder(nil)
	for i := 0; i < MaxDepth; i++ {
		if err := d.Enter(); err != nil {
			t.Fatalf("enter %d: %v", i, err)
		}
	}
	if err := d.Enter(); err != ErrDepth {
		t.Fatalf("expected ErrDepth, got %v", err)
	}
}

func TestInvalidBool(t *testing.T) {
	d := NewDecoder([]byte{7})
	if _, err := d.Bool(); err != ErrBool {
		t.Fatalf("expected ErrBool, got %v", err)
	}
}
