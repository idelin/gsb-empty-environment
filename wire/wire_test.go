package wire

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{
		0, 1, 2, 127, 128, 129, 300, 16383, 16384,
		1<<21 - 1, 1 << 21, 1<<28 - 1, 1 << 28,
		1<<35 - 1, 1 << 35, 1<<42 - 1, 1 << 42,
		1<<49 - 1, 1 << 49, 1<<56 - 1, 1 << 56,
		1<<63 - 1, 1 << 63, math.MaxUint64 - 1, math.MaxUint64,
	}
	for _, want := range cases {
		buf := AppendVarint(nil, want)
		d := NewDecoder(buf)
		got, err := d.Varint()
		if err != nil {
			t.Fatalf("Varint(%d): %v", want, err)
		}
		if got != want {
			t.Fatalf("Varint(%d) = %d", want, got)
		}
		if !d.EOF() {
			t.Fatalf("Varint(%d): %d trailing byte(s)", want, d.Remaining())
		}
	}
}

func TestVarintCanonicalEncodings(t *testing.T) {
	// Known-good encodings from the protobuf spec.
	cases := []struct {
		v    uint64
		want []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{300, []byte{0xac, 0x02}},
		{math.MaxUint64, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
	}
	for _, c := range cases {
		if got := AppendVarint(nil, c.v); !bytes.Equal(got, c.want) {
			t.Fatalf("AppendVarint(%d) = % x, want % x", c.v, got, c.want)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10th byte with payload > 1 bit.
	over := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, err := NewDecoder(over).Varint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 10th byte with continuation bit set (11-byte varint).
	over2 := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if _, err := NewDecoder(over2).Varint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
}

func TestVarintTruncated(t *testing.T) {
	full := AppendVarint(nil, math.MaxUint64) // 10 bytes
	for i := 0; i < len(full); i++ {
		_, err := NewDecoder(full[:i]).Varint()
		if !IsTruncated(err) {
			t.Fatalf("prefix len %d: want TruncatedError, got %v", i, err)
		}
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		signed   int64
		unsigned uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := EncodeZigZag64(c.signed); got != c.unsigned {
			t.Fatalf("EncodeZigZag64(%d) = %d, want %d", c.signed, got, c.unsigned)
		}
		if got := DecodeZigZag64(c.unsigned); got != c.signed {
			t.Fatalf("DecodeZigZag64(%d) = %d, want %d", c.unsigned, got, c.signed)
		}
	}
	// 32-bit variant.
	if EncodeZigZag32(-1) != 1 || DecodeZigZag32(1) != -1 {
		t.Fatal("zigzag32 broken")
	}
	if EncodeZigZag32(math.MinInt32) != math.MaxUint32 {
		t.Fatal("zigzag32 min broken")
	}
	if DecodeZigZag32(math.MaxUint32) != math.MinInt32 {
		t.Fatal("zigzag32 min decode broken")
	}
}

func TestFixedRoundTrip(t *testing.T) {
	for _, v := range []uint32{0, 1, 0xdeadbeef, math.MaxUint32} {
		d := NewDecoder(AppendFixed32(nil, v))
		got, err := d.Fixed32()
		if err != nil || got != v {
			t.Fatalf("Fixed32(%d) = %d, %v", v, got, err)
		}
	}
	for _, v := range []uint64{0, 1, 0xdeadbeefcafe, math.MaxUint64} {
		d := NewDecoder(AppendFixed64(nil, v))
		got, err := d.Fixed64()
		if err != nil || got != v {
			t.Fatalf("Fixed64(%d) = %d, %v", v, got, err)
		}
	}
}

func TestFixedTruncated(t *testing.T) {
	for i := 0; i < 4; i++ {
		if _, err := NewDecoder(make([]byte, i)).Fixed32(); !IsTruncated(err) {
			t.Fatalf("fixed32 prefix %d: want truncation, got %v", i, err)
		}
	}
	for i := 0; i < 8; i++ {
		if _, err := NewDecoder(make([]byte, i)).Fixed64(); !IsTruncated(err) {
			t.Fatalf("fixed64 prefix %d: want truncation, got %v", i, err)
		}
	}
}

func TestBytesTruncated(t *testing.T) {
	full := AppendBytes(nil, []byte("hello world"))
	// Cut inside the payload: length prefix says 11, fewer remain.
	for i := 1; i < len(full); i++ {
		if _, err := NewDecoder(full[:i]).Bytes(); !IsTruncated(err) {
			t.Fatalf("bytes prefix %d: want truncation, got %v", i, err)
		}
	}
	// Empty payload is legal.
	p, err := NewDecoder(AppendBytes(nil, nil)).Bytes()
	if err != nil || len(p) != 0 {
		t.Fatalf("empty bytes: %v %v", p, err)
	}
}

func TestTagAndSkip(t *testing.T) {
	e := NewEncoder()
	e.Varint(1, 150).Fixed64(2, 0x1122334455667788).String(3, "skip-me").Fixed32(4, 42)
	d := NewDecoder(e.Bytes())
	for i, want := range []Type{Varint, Fixed64, Bytes, Fixed32} {
		field, wt, err := d.Tag()
		if err != nil {
			t.Fatal(err)
		}
		if field != i+1 || wt != want {
			t.Fatalf("tag %d: got (%d,%s), want (%d,%s)", i, field, wt, i+1, want)
		}
		if err := d.Skip(wt); err != nil {
			t.Fatal(err)
		}
	}
	if !d.EOF() {
		t.Fatal("decoder not exhausted after skipping all fields")
	}
}

func TestBadWireType(t *testing.T) {
	// Wire types 3, 4 (groups, deprecated) and 6, 7 are rejected.
	for _, wt := range []Type{3, 4, 6, 7} {
		buf := AppendTag(nil, 1, wt)
		if _, _, err := NewDecoder(buf).Tag(); !errors.Is(err, ErrBadWireType) {
			t.Fatalf("wire type %d: want ErrBadWireType, got %v", wt, err)
		}
	}
	// Field number 0 is illegal.
	if _, _, err := NewDecoder([]byte{0x00}).Tag(); err == nil {
		t.Fatal("field 0: want error")
	}
}

func TestNestedMessageManual(t *testing.T) {
	// Hand-build a 3-level nested message without the reflection engine.
	inner := NewEncoder().Varint(1, 7).String(2, "deep").Bytes()
	mid := NewEncoder().ZigZag64(1, -42).Message(2, inner).Bytes()
	outer := NewEncoder().Fixed64(1, 0xdeadbeef).Message(2, mid).Bytes()

	d := NewDecoder(outer)
	f, wt, err := d.Tag()
	if err != nil || f != 1 || wt != Fixed64 {
		t.Fatalf("outer tag: %d %s %v", f, wt, err)
	}
	if v, _ := d.Fixed64(); v != 0xdeadbeef {
		t.Fatalf("outer fixed64 = %#x", v)
	}
	if _, _, err := d.Tag(); err != nil {
		t.Fatal(err)
	}
	midBytes, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !d.EOF() {
		t.Fatal("outer not exhausted")
	}

	dm := NewDecoder(midBytes)
	if _, _, err := dm.Tag(); err != nil {
		t.Fatal(err)
	}
	if v, _ := dm.ZigZag64(); v != -42 {
		t.Fatalf("mid zigzag = %d", v)
	}
	if _, _, err := dm.Tag(); err != nil {
		t.Fatal(err)
	}
	innerBytes, err := dm.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	di := NewDecoder(innerBytes)
	if _, _, err := di.Tag(); err != nil {
		t.Fatal(err)
	}
	if v, _ := di.Varint(); v != 7 {
		t.Fatalf("inner varint = %d", v)
	}
	if _, _, err := di.Tag(); err != nil {
		t.Fatal(err)
	}
	if s, _ := di.String(); s != "deep" {
		t.Fatalf("inner string = %q", s)
	}
	if !di.EOF() {
		t.Fatal("inner not exhausted")
	}
}

func TestPackedVarints(t *testing.T) {
	want := []uint64{0, 1, 127, 128, 300, math.MaxUint64}
	e := NewEncoder().PackedVarints(5, want)
	d := NewDecoder(e.Bytes())
	f, wt, err := d.Tag()
	if err != nil || f != 5 || wt != Bytes {
		t.Fatalf("packed tag: %d %s %v", f, wt, err)
	}
	got, err := d.PackedVarints()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("packed len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("packed[%d] = %d, want %d", i, got[i], want[i])
		}
	}
	// Truncated packed payload (inner varint cut short) must error.
	raw := e.Bytes()
	cut := raw[:len(raw)-1]
	cd := NewDecoder(cut)
	if _, _, err := cd.Tag(); err != nil {
		t.Fatal(err)
	}
	if _, err := cd.PackedVarints(); !IsTruncated(err) {
		t.Fatalf("packed truncation: got %v", err)
	}
}

func TestPackedZigZag(t *testing.T) {
	want := []int64{0, -1, 1, -2, 2, math.MinInt64, math.MaxInt64}
	e := NewEncoder().PackedZigZag64(1, want)
	d := NewDecoder(e.Bytes())
	if _, _, err := d.Tag(); err != nil {
		t.Fatal(err)
	}
	got, err := d.PackedZigZag64()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}
