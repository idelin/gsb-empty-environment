package wire

import (
	"bytes"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{
		0, 1, 2, 127, 128, 129, 300, 16383, 16384,
		1<<21 - 1, 1 << 21, 1<<28 - 1, 1 << 28,
		1<<35 - 1, 1 << 35, 1<<49 - 1, 1 << 49,
		1<<56 - 1, 1 << 56, 1<<63 - 1, 1 << 63,
		math.MaxUint64 - 1, math.MaxUint64,
	}
	for _, want := range cases {
		buf := EncodeVarint(nil, want)
		if got := SizeVarint(want); got != len(buf) {
			t.Fatalf("SizeVarint(%d)=%d, encoded len=%d", want, got, len(buf))
		}
		got, n, err := DecodeVarint(buf)
		if err != nil {
			t.Fatalf("DecodeVarint(%d) error: %v", want, err)
		}
		if got != want || n != len(buf) {
			t.Fatalf("DecodeVarint roundtrip: got (%d,%d), want (%d,%d)", got, n, want, len(buf))
		}
	}
}

// Canonical encodings from the protobuf docs.
func TestVarintKnownVectors(t *testing.T) {
	if buf := EncodeVarint(nil, 1); !bytes.Equal(buf, []byte{0x01}) {
		t.Fatalf("encode(1)=%x", buf)
	}
	if buf := EncodeVarint(nil, 300); !bytes.Equal(buf, []byte{0xac, 0x02}) {
		t.Fatalf("encode(300)=%x", buf)
	}
	if buf := EncodeVarint(nil, 150); !bytes.Equal(buf, []byte{0x96, 0x01}) {
		t.Fatalf("encode(150)=%x", buf)
	}
}

func TestVarintTruncation(t *testing.T) {
	full := EncodeVarint(nil, math.MaxUint64) // 10 bytes
	for i := 0; i < len(full); i++ {
		if _, _, err := DecodeVarint(full[:i]); err != ErrTruncated {
			t.Fatalf("prefix %d: want ErrTruncated, got %v", i, err)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: definitely overflow.
	buf := bytes.Repeat([]byte{0x80}, 11)
	if _, _, err := DecodeVarint(buf); err != ErrOverflow {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 10 bytes where the 10th carries more than 1 bit.
	buf = append(bytes.Repeat([]byte{0xff}, 9), 0x7f)
	if _, _, err := DecodeVarint(buf); err != ErrOverflow {
		t.Fatalf("10th byte >1: want ErrOverflow, got %v", err)
	}
	// Exactly 10 bytes, 10th byte == 1: legal (top bit set).
	buf = append(bytes.Repeat([]byte{0xff}, 9), 0x01)
	v, n, err := DecodeVarint(buf)
	if err != nil || n != 10 || v != math.MaxUint64 {
		t.Fatalf("max varint: v=%d n=%d err=%v", v, n, err)
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
		{2147483647, 4294967294}, {-2147483648, 4294967295},
	}
	for _, c := range cases {
		if got := ZigZagEncode64(c.signed); got != c.unsigned {
			t.Fatalf("ZigZagEncode64(%d)=%d, want %d", c.signed, got, c.unsigned)
		}
		if got := ZigZagDecode64(c.unsigned); got != c.signed {
			t.Fatalf("ZigZagDecode64(%d)=%d, want %d", c.unsigned, got, c.signed)
		}
	}
	for _, v := range []int32{0, -1, 1, math.MinInt32, math.MaxInt32} {
		if got := ZigZagDecode32(ZigZagEncode32(v)); got != v {
			t.Fatalf("zigzag32 roundtrip %d -> %d", v, got)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	for _, v := range []uint32{0, 1, 0xdeadbeef, math.MaxUint32} {
		got, n, err := DecodeFixed32(EncodeFixed32(nil, v))
		if err != nil || got != v || n != 4 {
			t.Fatalf("fixed32 roundtrip %x: got %x n=%d err=%v", v, got, n, err)
		}
	}
	for _, v := range []uint64{0, 1, 0x0123456789abcdef, math.MaxUint64} {
		got, n, err := DecodeFixed64(EncodeFixed64(nil, v))
		if err != nil || got != v || n != 8 {
			t.Fatalf("fixed64 roundtrip %x: got %x n=%d err=%v", v, got, n, err)
		}
	}
	// Little-endian spot check.
	if b := EncodeFixed32(nil, 0x01020304); !bytes.Equal(b, []byte{4, 3, 2, 1}) {
		t.Fatalf("fixed32 LE: %x", b)
	}
}

func TestFixedTruncation(t *testing.T) {
	if _, _, err := DecodeFixed32([]byte{1, 2, 3}); err != ErrTruncated {
		t.Fatalf("fixed32 short: %v", err)
	}
	if _, _, err := DecodeFixed64([]byte{1, 2, 3, 4, 5, 6, 7}); err != ErrTruncated {
		t.Fatalf("fixed64 short: %v", err)
	}
}

func TestTagDecode(t *testing.T) {
	buf := EncodeTag(nil, 15, WireFixed32)
	num, wt, n, err := DecodeTag(buf)
	if err != nil || num != 15 || wt != WireFixed32 || n != len(buf) {
		t.Fatalf("tag: num=%d wt=%d n=%d err=%v", num, wt, n, err)
	}
	// Field number 0 is illegal.
	if _, _, _, err := DecodeTag([]byte{0x00}); err != ErrInvalidFieldNumber {
		t.Fatalf("field 0: %v", err)
	}
	// Wire types 3,4 (groups) and 6,7 are rejected.
	for _, wt := range []byte{3, 4, 6, 7} {
		if _, _, _, err := DecodeTag([]byte{1<<3 | wt}); err != ErrInvalidWireType {
			t.Fatalf("wiretype %d: %v", wt, err)
		}
	}
}

func TestReaderBoundsSafety(t *testing.T) {
	// A message: field1 varint=150, field2 len-delimited "abc".
	var buf []byte
	buf = EncodeTag(buf, 1, WireVarint)
	buf = EncodeVarint(buf, 150)
	buf = EncodeTag(buf, 2, WireLengthDelimited)
	buf = EncodeVarint(buf, 3)
	buf = append(buf, "abc"...)

	msg, err := ParseMessage(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(msg) != 2 || msg[0].Varint != 150 || string(msg[1].Bytes) != "abc" {
		t.Fatalf("parsed: %+v", msg)
	}

	// Prefixes that cut inside a field must fail with a truncation-class
	// error, never panic. Prefixes landing exactly on a field boundary
	// (0, 3, 8 here) are valid shorter messages and must succeed.
	boundary := map[int]bool{0: true, 3: true, 8: true}
	for i := 0; i <= len(buf); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix %d panicked: %v", i, r)
				}
			}()
			_, err := ParseMessage(buf[:i])
			if boundary[i] {
				if err != nil {
					t.Fatalf("prefix %d (field boundary) should parse: %v", i, err)
				}
				return
			}
			te, ok := err.(*TruncatedError)
			if !ok || !te.Is(ErrTruncated) {
				t.Fatalf("prefix %d: want *TruncatedError, got %T: %v", i, err, err)
			}
		}()
	}
}

func TestReaderLengthPrefixOvershoot(t *testing.T) {
	// Length prefix claims 100 bytes but only 2 follow.
	var buf []byte
	buf = EncodeTag(buf, 1, WireLengthDelimited)
	buf = EncodeVarint(buf, 100)
	buf = append(buf, 0xAA, 0xBB)
	_, err := ParseMessage(buf)
	te, ok := err.(*TruncatedError)
	if !ok {
		t.Fatalf("want *TruncatedError, got %T: %v", err, err)
	}
	if !te.Is(ErrTruncated) {
		t.Fatalf("Is(ErrTruncated) false")
	}
}

func TestPackedDecoders(t *testing.T) {
	var payload []byte
	for _, v := range []uint64{1, 300, 1 << 40} {
		payload = EncodeVarint(payload, v)
	}
	got, err := DecodePackedVarints(payload)
	if err != nil || len(got) != 3 || got[0] != 1 || got[1] != 300 || got[2] != 1<<40 {
		t.Fatalf("packed varints: %v %v", got, err)
	}

	sgot, err := DecodePackedSvarints(payload)
	if err != nil || sgot[0] != -1 || sgot[1] != 150 {
		t.Fatalf("packed svarints: %v %v", sgot, err)
	}

	var f32 []byte
	f32 = EncodeFixed32(f32, 7)
	f32 = EncodeFixed32(f32, 8)
	g32, err := DecodePackedFixed32(f32)
	if err != nil || len(g32) != 2 || g32[1] != 8 {
		t.Fatalf("packed fixed32: %v %v", g32, err)
	}
	if _, err := DecodePackedFixed32(f32[:5]); err == nil {
		t.Fatalf("misaligned packed fixed32 must fail")
	}

	var f64 []byte
	f64 = EncodeFixed64(f64, 9)
	if _, err := DecodePackedFixed64(f64[:7]); err == nil {
		t.Fatalf("misaligned packed fixed64 must fail")
	}

	// Truncated varint inside packed payload.
	if _, err := DecodePackedVarints([]byte{0x80}); err == nil {
		t.Fatalf("truncated packed varint must fail")
	}
}
