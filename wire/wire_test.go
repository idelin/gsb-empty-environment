package wire

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 1<<16 - 1, 1 << 16, 1<<32 - 1, 1 << 32,
		math.MaxInt64, math.MaxUint64 - 1, math.MaxUint64}
	for _, want := range cases {
		b := NewBuffer()
		b.AppendVarint(want)
		if want < 128 && b.Len() != 1 {
			t.Fatalf("%d should encode to 1 byte, got %d", want, b.Len())
		}
		r := NewReader(b.Bytes())
		got, err := r.ReadVarint()
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", want, err)
		}
		if got != want {
			t.Errorf("varint round trip: got %d, want %d", got, want)
		}
		if !r.Done() {
			t.Errorf("reader not drained for %d", want)
		}
	}
}

func TestVarintKnownEncodings(t *testing.T) {
	// 300 = 0b100101100 -> 0xAC 0x02 per the protobuf spec.
	b := NewBuffer()
	b.AppendVarint(300)
	if !bytes.Equal(b.Bytes(), []byte{0xAC, 0x02}) {
		t.Fatalf("300 encoded as % x, want [AC 02]", b.Bytes())
	}
	// MaxUint64 takes exactly 10 bytes, last byte 0x01.
	b.Reset()
	b.AppendVarint(math.MaxUint64)
	if b.Len() != MaxVarintBytes || b.Bytes()[9] != 0x01 {
		t.Fatalf("MaxUint64 encoded as % x", b.Bytes())
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: definitely overflow.
	b := NewBuffer()
	for i := 0; i < 11; i++ {
		b.AppendVarint(0) // placeholder to grow
	}
	over := bytes.Repeat([]byte{0xFF}, 11)
	r := NewReader(over)
	if _, err := r.ReadVarint(); err != ErrOverflow {
		t.Fatalf("11-byte varint: got %v, want ErrOverflow", err)
	}
	// 10th byte carrying more than 1 payload bit.
	bad10 := append(bytes.Repeat([]byte{0x80}, 9), 0x02)
	r = NewReader(bad10)
	if _, err := r.ReadVarint(); err != ErrOverflow {
		t.Fatalf("10th byte 0x02: got %v, want ErrOverflow", err)
	}
	// 10th byte with continuation bit set.
	bad10c := append(bytes.Repeat([]byte{0x80}, 9), 0x81)
	r = NewReader(bad10c)
	if _, err := r.ReadVarint(); err != ErrOverflow {
		t.Fatalf("10th byte 0x81: got %v, want ErrOverflow", err)
	}
}

func TestVarintTruncated(t *testing.T) {
	// A varint promising continuation but cut off.
	for n := 0; n < 3; n++ {
		r := NewReader(bytes.Repeat([]byte{0x80}, n))
		if _, err := r.ReadVarint(); err != ErrTruncated {
			t.Fatalf("%d continuation bytes: got %v, want ErrTruncated", n, err)
		}
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		in  int64
		enc uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := ZigZag64(c.in); got != c.enc {
			t.Errorf("ZigZag64(%d) = %d, want %d", c.in, got, c.enc)
		}
		if got := UnZigZag64(c.enc); got != c.in {
			t.Errorf("UnZigZag64(%d) = %d, want %d", c.enc, got, c.in)
		}
	}
	// 32-bit variants.
	if ZigZag32(-1) != 1 || UnZigZag32(1) != -1 {
		t.Error("zigzag32 broken for -1")
	}
	if ZigZag32(math.MinInt32) != math.MaxUint32 {
		t.Error("zigzag32 broken for MinInt32")
	}
	// Full round trip through the wire.
	for _, v := range []int64{0, -1, 1, -123456, 123456, math.MinInt64, math.MaxInt64} {
		b := NewBuffer()
		b.AppendZigZag64(v)
		got, err := NewReader(b.Bytes()).ReadZigZag64()
		if err != nil || got != v {
			t.Errorf("zigzag wire round trip %d: got %d, err %v", v, got, err)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	b := NewBuffer()
	b.AppendFixed32(0xDEADBEEF)
	b.AppendFixed64(0x0102030405060708)
	r := NewReader(b.Bytes())
	v32, err := r.ReadFixed32()
	if err != nil || v32 != 0xDEADBEEF {
		t.Fatalf("fixed32: got %x err %v", v32, err)
	}
	v64, err := r.ReadFixed64()
	if err != nil || v64 != 0x0102030405060708 {
		t.Fatalf("fixed64: got %x err %v", v64, err)
	}
}

func TestFixedTruncated(t *testing.T) {
	for n := 0; n < 4; n++ {
		if _, err := NewReader(make([]byte, n)).ReadFixed32(); err != ErrTruncated {
			t.Fatalf("fixed32 with %d bytes: got %v, want ErrTruncated", n, err)
		}
	}
	for n := 0; n < 8; n++ {
		if _, err := NewReader(make([]byte, n)).ReadFixed64(); err != ErrTruncated {
			t.Fatalf("fixed64 with %d bytes: got %v, want ErrTruncated", n, err)
		}
	}
}

func TestReadBytesTruncated(t *testing.T) {
	// Declared length 10, only 3 bytes present.
	b := NewBuffer()
	b.AppendVarint(10)
	b.Bytes() // no-op
	raw := append(b.Bytes(), 1, 2, 3)
	if _, err := NewReader(raw).ReadBytes(); err != ErrTruncated {
		t.Fatalf("declared length beyond buffer: got %v, want ErrTruncated", err)
	}
	// Empty buffer: length prefix itself missing.
	if _, err := NewReader(nil).ReadBytes(); err != ErrTruncated {
		t.Fatalf("empty buffer: got %v, want ErrTruncated", err)
	}
}

func TestReadTagValidation(t *testing.T) {
	// Field number 0 is illegal.
	if _, _, err := NewReader([]byte{0x00}).ReadTag(); err != ErrInvalidTag {
		t.Fatalf("field 0: got %v, want ErrInvalidTag", err)
	}
	// Wire type 3 (deprecated group start) is rejected.
	if _, _, err := NewReader([]byte{0x0B}).ReadTag(); err != ErrInvalidWireType {
		t.Fatalf("wire type 3: got %v, want ErrInvalidWireType", err)
	}
	// Legal tag: field 1, varint.
	num, wt, err := NewReader([]byte{0x08}).ReadTag()
	if err != nil || num != 1 || wt != WireVarint {
		t.Fatalf("tag 0x08: got num=%d wt=%d err=%v", num, wt, err)
	}
}

func TestParseMessageNested(t *testing.T) {
	// inner: field 1 varint 150, field 2 string "hi"
	inner := NewBuffer()
	inner.AppendTag(1, WireVarint)
	inner.AppendVarint(150)
	inner.AppendTag(2, WireBytes)
	inner.AppendString("hi")

	// outer: field 1 fixed32, field 2 nested message, field 3 fixed64
	outer := NewBuffer()
	outer.AppendTag(1, WireFixed32)
	outer.AppendFixed32(0xABCDEF01)
	outer.AppendTag(2, WireBytes)
	outer.AppendBytes(inner.Bytes())
	outer.AppendTag(3, WireFixed64)
	outer.AppendFixed64(0x1122334455667788)

	fields, err := ParseMessage(outer.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("got %d fields, want 3", len(fields))
	}
	if fields[0].Fixed32 != 0xABCDEF01 {
		t.Errorf("field 1 fixed32 = %x", fields[0].Fixed32)
	}
	if fields[2].Fixed64 != 0x1122334455667788 {
		t.Errorf("field 3 fixed64 = %x", fields[2].Fixed64)
	}
	// Recurse into the nested message.
	sub, err := ParseMessage(fields[1].Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 2 || sub[0].Varint != 150 || string(sub[1].Bytes) != "hi" {
		t.Errorf("nested fields wrong: %+v", sub)
	}
}

func TestParseMessageTruncationSafety(t *testing.T) {
	inner := NewBuffer()
	inner.AppendTag(1, WireVarint)
	inner.AppendVarint(150)
	outer := NewBuffer()
	outer.AppendTag(1, WireBytes)
	outer.AppendBytes(inner.Bytes())
	outer.AppendTag(2, WireFixed64)
	outer.AppendFixed64(0xFFFFFFFFFFFFFFFF)
	full := outer.Bytes()

	// Every strict prefix must be handled without panic. Cuts landing on
	// a field boundary are valid shorter messages; cuts inside a field
	// must error.
	for cut := 0; cut < len(full); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("cut=%d: panic %v", cut, r)
				}
			}()
			_, _ = ParseMessage(full[:cut])
		}()
	}
	// Cutting inside the trailing fixed64 value must report truncation.
	if _, err := ParseMessage(full[:len(full)-1]); err == nil {
		t.Error("cut inside fixed64 value: expected error, got nil")
	}
	// Cutting inside the nested bytes payload must report truncation.
	// Layout: tag(1) len(1) payload... so cut at offset 3 is mid-payload.
	if _, err := ParseMessage(full[:3]); err == nil {
		t.Error("cut inside nested payload: expected error, got nil")
	}
	// And the full message parses.
	if _, err := ParseMessage(full); err != nil {
		t.Fatalf("full message: %v", err)
	}
}

func TestPackedRoundTrip(t *testing.T) {
	varints := []uint64{0, 1, 300, math.MaxUint64}
	payload := EncodePackedVarints(varints)
	got, err := DecodePackedVarints(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(varints) {
		t.Fatalf("got %d elements, want %d", len(got), len(varints))
	}
	for i := range varints {
		if got[i] != varints[i] {
			t.Errorf("element %d: got %d want %d", i, got[i], varints[i])
		}
	}

	b := NewBuffer()
	for _, v := range []uint32{7, 0xFFFFFFFF} {
		b.AppendFixed32(v)
	}
	got32, err := DecodePackedFixed32(b.Bytes())
	if err != nil || len(got32) != 2 || got32[1] != 0xFFFFFFFF {
		t.Errorf("packed fixed32: %v err %v", got32, err)
	}

	b.Reset()
	b.AppendFixed64(0x0102030405060708)
	got64, err := DecodePackedFixed64(b.Bytes())
	if err != nil || len(got64) != 1 || got64[0] != 0x0102030405060708 {
		t.Errorf("packed fixed64: %v err %v", got64, err)
	}
}

func TestPackedTruncation(t *testing.T) {
	// Packed varint payload ending mid-varint.
	if _, err := DecodePackedVarints([]byte{0xAC}); err == nil {
		t.Error("truncated packed varint accepted")
	}
	// Packed fixed32 with a length that is not a multiple of 4.
	if _, err := DecodePackedFixed32([]byte{1, 2, 3}); err != ErrTruncated {
		t.Errorf("packed fixed32 len 3: got %v, want ErrTruncated", err)
	}
	// Packed fixed64 with a length that is not a multiple of 8.
	if _, err := DecodePackedFixed64([]byte{1, 2, 3, 4, 5}); err != ErrTruncated {
		t.Errorf("packed fixed64 len 5: got %v, want ErrTruncated", err)
	}
}

func TestErrorMessagesAreDescriptive(t *testing.T) {
	// A bytes field whose length overruns the buffer should name the field.
	b := NewBuffer()
	b.AppendTag(7, WireBytes)
	b.AppendVarint(100)
	b.AppendBytes([]byte("short"))
	_, err := ParseMessage(b.Bytes())
	if err == nil || !strings.Contains(err.Error(), "field 7") {
		t.Fatalf("error should name field 7, got: %v", err)
	}
}
