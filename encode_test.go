package gsbdiff

import (
	"errors"
	"testing"
)

func validWire(t *testing.T) []byte {
	t.Helper()
	d, err := Compute(
		map[string]any{"a": float64(1), "list": []any{"x", "y"}},
		map[string]any{"a": float64(2), "list": []any{"x", "z", "w"}, "n": nil},
	)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	wire, err := Encode(d)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return wire
}

func TestDecodeRejectsHugeDeclaredLengths(t *testing.T) {
	wire := validWire(t)

	// Corrupt opCount (offset 4+8+32) to a huge value.
	bad := append([]byte(nil), wire...)
	bad[44], bad[45], bad[46], bad[47] = 0xff, 0xff, 0xff, 0xff
	if _, err := Decode(bad); !errors.Is(err, ErrBadLength) {
		t.Fatalf("huge opCount: want ErrBadLength, got %v", err)
	}

	// A hand-crafted stream: valid header, one OpSet with a string value
	// whose declared length is 0xffffffff.
	d, _ := Compute(map[string]any{"a": float64(1)}, map[string]any{"a": float64(2)})
	w := &writer{}
	w.bytes(magic)
	w.u64(d.BaseLen)
	w.bytes(d.BaseHash[:])
	w.u32(1)          // opCount
	w.u8(byte(OpSet)) // kind
	w.u32(1)          // pathLen
	w.u8(0)           // key segment
	w.u32(1)
	w.bytes([]byte("a"))
	w.u8(4)           // string value tag
	w.u32(0xffffffff) // absurd length
	if _, err := Decode(w.buf); !errors.Is(err, ErrBadLength) {
		t.Fatalf("huge string length: want ErrBadLength, got %v", err)
	}
}

func TestDecodeRejectsTruncation(t *testing.T) {
	wire := validWire(t)
	for _, n := range []int{0, 3, 10, 43, len(wire) / 2, len(wire) - 1} {
		if _, err := Decode(wire[:n]); err == nil {
			t.Fatalf("truncated to %d bytes: want error, got nil", n)
		}
	}
}

func TestDecodeRejectsTrailingBytes(t *testing.T) {
	wire := append(validWire(t), 0x00)
	if _, err := Decode(wire); !errors.Is(err, ErrTrailing) {
		t.Fatalf("want ErrTrailing, got %v", err)
	}
}

func TestDecodeRejectsBadMagic(t *testing.T) {
	wire := validWire(t)
	wire[0] = 'X'
	if _, err := Decode(wire); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}

func TestEncodeDecodeRoundTripDeterministic(t *testing.T) {
	d, err := Compute(baseline(), target())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	w1, err := Encode(d)
	if err != nil {
		t.Fatalf("encode 1: %v", err)
	}
	d2, err := Decode(w1)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	w2, err := Encode(d2)
	if err != nil {
		t.Fatalf("encode 2: %v", err)
	}
	if string(w1) != string(w2) {
		t.Fatal("encode/decode not deterministic")
	}
}
