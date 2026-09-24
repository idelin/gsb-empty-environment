package baselinepatch

import (
	"bytes"
	"math"
	"testing"
)

func TestCanonicalRoundTrip(t *testing.T) {
	cases := []any{
		nil,
		true,
		false,
		0.0,
		-0.0,
		3.14159,
		"",
		"hello 世界",
		[]any{},
		[]any{float64(1), float64(2), float64(3)},
		map[string]any{"a": float64(1)},
		map[string]any{
			"nested": map[string]any{"b": []any{nil, true, "x", -2.5}},
			"a":      float64(1),
		},
	}
	for _, v := range cases {
		raw, err := canonicalBytes(v)
		if err != nil {
			t.Fatalf("encode %#v: %v", v, err)
		}
		got, err := decodeValue(&reader{data: raw})
		if err != nil {
			t.Fatalf("decode %#v: %v", v, err)
		}
		eq, err := equalValues(got, v)
		if err != nil {
			t.Fatal(err)
		}
		if !eq {
			t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", got, v)
		}
	}
}

func TestObjectKeyOrderCanonical(t *testing.T) {
	a := map[string]any{"b": float64(2), "a": float64(1), "c": float64(3)}
	b := map[string]any{"a": float64(1), "c": float64(3), "b": float64(2)}
	ab, _ := canonicalBytes(a)
	bb, _ := canonicalBytes(b)
	if !bytes.Equal(ab, bb) {
		t.Fatal("object key order must not affect encoding")
	}
}

func TestNullVsDeleteAndNegativeZero(t *testing.T) {
	// Digest distinguishes null-valued key from absent key.
	withNull := map[string]any{"k": nil}
	withoutK := map[string]any{}
	d1, _ := Digest(withNull)
	d2, _ := Digest(withoutK)
	if d1 == d2 {
		t.Fatal("null field and absent field must have different digests")
	}

	// -0.0 and +0.0 have different bit patterns and therefore different
	// canonical identity; the inverse convention must preserve them exactly.
	dp, _ := Digest(float64(0))
	dn, _ := Digest(math.Copysign(0, -1))
	if dp == dn {
		t.Fatal("-0.0 and +0.0 must differ canonically")
	}
}

func TestUnsupportedValuesRejected(t *testing.T) {
	bad := []any{
		3,                 // plain int
		int64(3),          // int64
		float32(3),        // float32
		map[int]any{1: 2}, // foreign map key
		[]string{"a"},     // foreign slice
		struct{}{},        // struct
		math.NaN(),        // non-finite
		math.Inf(1),       // non-finite
	}
	for i, v := range bad {
		_, err := canonicalBytes(v)
		if err == nil {
			t.Errorf("case %d (%T): expected rejection", i, v)
		}
	}
	if _, err := canonicalBytes("ok"); err != nil {
		t.Error("string should be accepted")
	}

	// Oversized string cap.
	big := string(make([]byte, maxStringBytes+1))
	if _, err := canonicalBytes(big); err == nil {
		t.Error("oversized string must be rejected")
	}
}

func TestDecodeValidatesBeforeAllocate(t *testing.T) {
	// Tag array, count 1 billion, no data: must fail without allocating.
	r := &reader{data: []byte{tagArray, 0x80, 0xCA, 0x9C, 0xEB}}
	if _, err := decodeValue(r); err == nil {
		t.Fatal("huge declared array length must be rejected")
	}
	// Declared count fits the cap but exceeds remaining bytes.
	r = &reader{data: []byte{tagArray, 0x05, tagNull}}
	if _, err := decodeValue(r); err == nil {
		t.Fatal("truncated array must be rejected")
	}
	// String claims more bytes than remain.
	r = &reader{data: []byte{tagString, 0x05, 'h', 'i'}}
	if _, err := decodeValue(r); err == nil {
		t.Fatal("truncated string must be rejected")
	}
	// Unknown tag.
	r = &reader{data: []byte{99}}
	if _, err := decodeValue(r); err == nil {
		t.Fatal("unknown tag must be rejected")
	}
	// Non-canonical object keys (duplicate / unsorted).
	dup := []byte{tagObject, 0x02,
		0x01, 'a', tagNull,
		0x01, 'a', tagBool, 0x01}
	if _, err := decodeValue(&reader{data: dup}); err == nil {
		t.Fatal("duplicate object key must be rejected")
	}
	// Invalid bool byte.
	if _, err := decodeValue(&reader{data: []byte{tagBool, 7}}); err == nil {
		t.Fatal("invalid bool byte must be rejected")
	}
	// Non-finite number embedded in wire bytes.
	inf := []byte{tagNumber, 0x7f, 0xf0, 0, 0, 0, 0, 0, 0}
	if _, err := decodeValue(&reader{data: inf}); err == nil {
		t.Fatal("encoded +Inf must be rejected")
	}
}

func TestUvarintOverflow(t *testing.T) {
	// 10 continuation bytes: malformed/overflow.
	data := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}
	if _, err := (&reader{data: data}).readUvarint(); err == nil {
		t.Fatal("overflow varint must be rejected")
	}
}
