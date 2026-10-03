package wire

import (
	"math"
	"math/rand"
	"testing"
)

func TestVarintRoundTripBoundaries(t *testing.T) {
	vals := []uint64{
		0, 1, 2, 127, 128, 129, 300, 16383, 16384,
		1<<21 - 1, 1 << 21, 1<<28 - 1, 1 << 28,
		1<<32 - 1, 1 << 32, 1<<63 - 1, 1 << 63, math.MaxUint64,
	}
	for _, v := range vals {
		buf := AppendVarint(nil, v)
		if got := SizeVarint(v); got != len(buf) {
			t.Fatalf("SizeVarint(%d)=%d, encoded len=%d", v, got, len(buf))
		}
		out, n, err := ConsumeVarint(buf)
		if err != nil {
			t.Fatalf("ConsumeVarint(%d): %v", v, err)
		}
		if out != v || n != len(buf) {
			t.Fatalf("roundtrip %d: got (%d,%d)", v, out, n)
		}
	}
}

func TestVarintKnownEncodings(t *testing.T) {
	cases := []struct {
		v   uint64
		enc []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{300, []byte{0xac, 0x02}},
		{math.MaxUint64, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
	}
	for _, c := range cases {
		got := AppendVarint(nil, c.v)
		if len(got) != len(c.enc) {
			t.Fatalf("varint(%d) len=%d want %d", c.v, len(got), len(c.enc))
		}
		for i := range got {
			if got[i] != c.enc[i] {
				t.Fatalf("varint(%d)=% x want % x", c.v, got, c.enc)
			}
		}
	}
}

func TestVarintTruncationEveryPrefix(t *testing.T) {
	full := AppendVarint(nil, math.MaxUint64) // 10 bytes
	for cut := 0; cut < len(full); cut++ {
		if _, _, err := ConsumeVarint(full[:cut]); err != ErrTruncated {
			t.Fatalf("prefix %d: got %v, want ErrTruncated", cut, err)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10th byte carrying bits beyond bit 63.
	over := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, _, err := ConsumeVarint(over); err != ErrOverflow {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
	// 11-byte varint: continuation bit still set on the 10th byte.
	eleven := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if _, _, err := ConsumeVarint(eleven); err != ErrOverflow {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
}

func TestZigZagRoundTrip(t *testing.T) {
	vals := []int64{0, -1, 1, -2, 2, 63, -64, 64, math.MaxInt32, math.MinInt32, math.MaxInt64, math.MinInt64}
	for _, v := range vals {
		if got := DecodeZigZag64(EncodeZigZag64(v)); got != v {
			t.Fatalf("zigzag64 roundtrip %d: got %d", v, got)
		}
	}
	vals32 := []int32{0, -1, 1, -2, 2, math.MaxInt32, math.MinInt32}
	for _, v := range vals32 {
		if got := DecodeZigZag32(EncodeZigZag32(v)); got != v {
			t.Fatalf("zigzag32 roundtrip %d: got %d", v, got)
		}
	}
	// Known mappings: 0->0, -1->1, 1->2, -2->3, 2->4.
	if EncodeZigZag64(-1) != 1 || EncodeZigZag64(1) != 2 || EncodeZigZag64(-2) != 3 {
		t.Fatal("zigzag known mapping broken")
	}
}

func TestFixedRoundTripAndTruncation(t *testing.T) {
	v32 := uint32(0xDEADBEEF)
	buf32 := AppendFixed32(nil, v32)
	if got, n, err := ConsumeFixed32(buf32); err != nil || got != v32 || n != 4 {
		t.Fatalf("fixed32 roundtrip: %x %d %v", got, n, err)
	}
	for cut := 0; cut < 4; cut++ {
		if _, _, err := ConsumeFixed32(buf32[:cut]); err != ErrTruncated {
			t.Fatalf("fixed32 prefix %d: got %v", cut, err)
		}
	}
	v64 := uint64(0x0123456789ABCDEF)
	buf64 := AppendFixed64(nil, v64)
	if got, n, err := ConsumeFixed64(buf64); err != nil || got != v64 || n != 8 {
		t.Fatalf("fixed64 roundtrip: %x %d %v", got, n, err)
	}
	for cut := 0; cut < 8; cut++ {
		if _, _, err := ConsumeFixed64(buf64[:cut]); err != ErrTruncated {
			t.Fatalf("fixed64 prefix %d: got %v", cut, err)
		}
	}
}

func TestTagValidation(t *testing.T) {
	buf := AppendTag(nil, 15, Fixed64Type)
	num, wt, n, err := ConsumeTag(buf)
	if err != nil || num != 15 || wt != Fixed64Type || n != 1 {
		t.Fatalf("tag roundtrip: %d %d %d %v", num, wt, n, err)
	}
	// Field number 0 is illegal.
	if _, _, _, err := ConsumeTag([]byte{0x00}); err != ErrInvalidFieldNumber {
		t.Fatalf("field 0: got %v", err)
	}
	// Wire types 3,4,6,7 are unsupported (groups deprecated / invalid).
	for _, bad := range []byte{0x0b, 0x0c, 0x0e, 0x0f} {
		if _, _, _, err := ConsumeTag([]byte{bad}); err != ErrInvalidWireType {
			t.Fatalf("wiretype byte %#x: got %v", bad, err)
		}
	}
	// Field number above 2^29-1 is illegal.
	big := AppendVarint(nil, uint64(MaxFieldNumber+1)<<3)
	if _, _, _, err := ConsumeTag(big); err != ErrInvalidFieldNumber {
		t.Fatalf("oversized field: got %v", err)
	}
}

func TestConsumeBytesGuards(t *testing.T) {
	payload := []byte("hello")
	buf := AppendBytes(nil, payload)
	got, n, err := ConsumeBytes(buf)
	if err != nil || string(got) != "hello" || n != len(buf) {
		t.Fatalf("bytes roundtrip: %q %d %v", got, n, err)
	}
	// Length prefix larger than remaining buffer.
	evil := AppendVarint(nil, 1000)
	evil = append(evil, payload...)
	if _, _, err := ConsumeBytes(evil); err != ErrNegativeLength {
		t.Fatalf("oversized length: got %v", err)
	}
	// Truncated length prefix.
	if _, _, err := ConsumeBytes([]byte{0x80}); err != ErrTruncated {
		t.Fatalf("truncated length: got %v", err)
	}
}

func TestParseMessageNestedAndPacked(t *testing.T) {
	// inner: field1 varint=150
	var inner []byte
	inner = AppendTag(inner, 1, VarintType)
	inner = AppendVarint(inner, 150)
	// packed: field2 packed varints {1,2,300}
	var packed []byte
	for _, v := range []uint64{1, 2, 300} {
		packed = AppendVarint(packed, v)
	}
	// outer: field1 fixed32, field2 inner message, field3 packed
	var outer []byte
	outer = AppendTag(outer, 1, Fixed32Type)
	outer = AppendFixed32(outer, 77)
	outer = AppendTag(outer, 2, BytesType)
	outer = AppendBytes(outer, inner)
	outer = AppendTag(outer, 3, BytesType)
	outer = AppendBytes(outer, packed)

	fields, err := ParseMessage(outer)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("want 3 fields, got %d", len(fields))
	}
	if fields[0].Type != Fixed32Type || fields[0].Fixed32 != 77 {
		t.Fatalf("field1: %+v", fields[0])
	}
	innerFields, err := ParseMessage(fields[1].Bytes)
	if err != nil || len(innerFields) != 1 || innerFields[0].Varint != 150 {
		t.Fatalf("nested: %+v %v", innerFields, err)
	}
	vals, err := ConsumePackedVarints(fields[2].Bytes)
	if err != nil || len(vals) != 3 || vals[2] != 300 {
		t.Fatalf("packed: %v %v", vals, err)
	}
}

func TestParseMessageTruncationEveryPrefix(t *testing.T) {
	var msg []byte
	boundary := map[int]bool{0: true}
	msg = AppendTag(msg, 1, VarintType)
	msg = AppendVarint(msg, 300)
	boundary[len(msg)] = true
	msg = AppendTag(msg, 2, Fixed64Type)
	msg = AppendFixed64(msg, 0x1122334455667788)
	boundary[len(msg)] = true
	msg = AppendTag(msg, 3, BytesType)
	msg = AppendBytes(msg, []byte("payload"))
	boundary[len(msg)] = true
	msg = AppendTag(msg, 4, Fixed32Type)
	msg = AppendFixed32(msg, 42)
	boundary[len(msg)] = true

	if _, err := ParseMessage(msg); err != nil {
		t.Fatalf("full message should parse: %v", err)
	}
	guarded := 0
	for cut := 1; cut < len(msg); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix %d panicked: %v", cut, r)
				}
			}()
			_, err := ParseMessage(msg[:cut])
			if boundary[cut] {
				// A prefix ending exactly at a field boundary is itself a
				// valid (shorter) message: it must parse cleanly.
				if err != nil {
					t.Fatalf("prefix %d at field boundary: got %v", cut, err)
				}
				return
			}
			// A cut inside a field encoding must trip the bounds guard.
			if err == nil {
				t.Fatalf("prefix %d inside a field: expected truncation error", cut)
			}
			guarded++
		}()
	}
	if guarded == 0 {
		t.Fatal("bounds guard never triggered")
	}
}

func TestParseMessageRandomGarbageNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		buf := make([]byte, rng.Intn(48))
		rng.Read(buf)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on % x: %v", buf, r)
				}
			}()
			ParseMessage(buf)
		}()
	}
}

func TestPackedTruncation(t *testing.T) {
	// Packed varint payload cut mid-value.
	if _, err := ConsumePackedVarints([]byte{0x01, 0x80}); err != ErrTruncated {
		t.Fatalf("packed varint truncation: got %v", err)
	}
	// Packed fixed32 payload not a multiple of 4.
	if _, err := ConsumePackedFixed32([]byte{1, 2, 3}); err != ErrTruncated {
		t.Fatalf("packed fixed32 truncation: got %v", err)
	}
	// Packed fixed64 payload not a multiple of 8.
	if _, err := ConsumePackedFixed64([]byte{1, 2, 3, 4, 5}); err != ErrTruncated {
		t.Fatalf("packed fixed64 truncation: got %v", err)
	}
}
