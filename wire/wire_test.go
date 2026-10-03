package wire

import (
	"math"
	"testing"
)

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
		{16384, []byte{0x80, 0x80, 0x01}},
		{math.MaxUint64, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
	}
	for _, c := range cases {
		got := AppendVarint(nil, c.v)
		if string(got) != string(c.enc) {
			t.Errorf("AppendVarint(%d) = % x, want % x", c.v, got, c.enc)
		}
		v, n, err := ConsumeVarint(c.enc)
		if err != nil || v != c.v || n != len(c.enc) {
			t.Errorf("ConsumeVarint(% x) = %d, %d, %v; want %d, %d, nil", c.enc, v, n, err, c.v, len(c.enc))
		}
		if SizeVarint(c.v) != len(c.enc) {
			t.Errorf("SizeVarint(%d) = %d, want %d", c.v, SizeVarint(c.v), len(c.enc))
		}
	}
}

func TestVarintRoundTripBoundaries(t *testing.T) {
	vals := []uint64{0, 1, 2, 127, 128, 255, 300, 1<<14 - 1, 1 << 14, 1<<21 - 1,
		1 << 21, 1<<28 - 1, 1 << 28, 1<<32 - 1, 1 << 32, 1<<63 - 1, 1 << 63, math.MaxUint64}
	for _, v := range vals {
		enc := AppendVarint(nil, v)
		got, n, err := ConsumeVarint(enc)
		if err != nil || got != v || n != len(enc) {
			t.Errorf("round trip %d failed: got=%d n=%d err=%v", v, got, n, err)
		}
	}
}

func TestVarintTruncation(t *testing.T) {
	// A varint with the continuation bit set but no following byte.
	for _, in := range [][]byte{
		{0x80},
		{0xac},
		{0xff, 0xff, 0xff},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}, // 9 continuation bytes
	} {
		if _, _, err := ConsumeVarint(in); err == nil {
			t.Errorf("ConsumeVarint(% x) succeeded, want truncation error", in)
		} else if pe, ok := err.(*ParseError); !ok || pe.Err != ErrTruncated {
			t.Errorf("ConsumeVarint(% x) error = %v, want ErrTruncated", in, err)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10th byte contributing more than 1 bit.
	over := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, _, err := ConsumeVarint(over); err == nil {
		t.Fatal("expected overflow error")
	} else if pe, ok := err.(*ParseError); !ok || pe.Err != ErrOverflow {
		t.Fatalf("error = %v, want ErrOverflow", err)
	}
	// 11 continuation bytes: exceeds MaxVarintLen entirely.
	over11 := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if _, _, err := ConsumeVarint(over11); err == nil {
		t.Fatal("expected overflow error for 11-byte varint")
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
			t.Errorf("EncodeZigZag64(%d) = %d, want %d", c.signed, got, c.unsigned)
		}
		if got := DecodeZigZag64(c.unsigned); got != c.signed {
			t.Errorf("DecodeZigZag64(%d) = %d, want %d", c.unsigned, got, c.signed)
		}
	}
	for _, v := range []int32{0, -1, 1, math.MinInt32, math.MaxInt32, -123456} {
		if got := DecodeZigZag32(EncodeZigZag32(v)); got != v {
			t.Errorf("zigzag32 round trip %d failed: got %d", v, got)
		}
	}
}

func TestFixedRoundTripAndTruncation(t *testing.T) {
	v32 := uint32(0xdeadbeef)
	enc32 := AppendFixed32(nil, v32)
	if len(enc32) != 4 {
		t.Fatalf("fixed32 encoding length = %d", len(enc32))
	}
	got32, n, err := ConsumeFixed32(enc32)
	if err != nil || got32 != v32 || n != 4 {
		t.Errorf("fixed32 round trip failed: %x %d %v", got32, n, err)
	}
	for i := 0; i < 4; i++ {
		if _, _, err := ConsumeFixed32(enc32[:i]); err == nil {
			t.Errorf("ConsumeFixed32(%d bytes) succeeded, want truncation", i)
		}
	}

	v64 := uint64(0x0123456789abcdef)
	enc64 := AppendFixed64(nil, v64)
	got64, n, err := ConsumeFixed64(enc64)
	if err != nil || got64 != v64 || n != 8 {
		t.Errorf("fixed64 round trip failed: %x %d %v", got64, n, err)
	}
	for i := 0; i < 8; i++ {
		if _, _, err := ConsumeFixed64(enc64[:i]); err == nil {
			t.Errorf("ConsumeFixed64(%d bytes) succeeded, want truncation", i)
		}
	}
}

func TestTagValidation(t *testing.T) {
	// Invalid wire types 3, 4, 6, 7.
	for _, wt := range []byte{3, 4, 6, 7} {
		tag := AppendVarint(nil, uint64(1)<<3|uint64(wt))
		if _, _, _, err := ConsumeTag(tag); err == nil {
			t.Errorf("wire type %d accepted", wt)
		} else if pe, ok := err.(*ParseError); !ok || pe.Err != ErrInvalidWire {
			t.Errorf("wire type %d: error = %v, want ErrInvalidWire", wt, err)
		}
	}
	// Field number 0 is illegal.
	if _, _, _, err := ConsumeTag([]byte{0x00}); err == nil {
		t.Error("field number 0 accepted")
	} else if pe, ok := err.(*ParseError); !ok || pe.Err != ErrBadTag {
		t.Errorf("error = %v, want ErrBadTag", err)
	}
}

func TestConsumeBytesTruncation(t *testing.T) {
	// Declared length 5, only 2 payload bytes present.
	in := append(AppendVarint(nil, 5), 0xaa, 0xbb)
	if _, _, err := ConsumeBytes(in); err == nil {
		t.Fatal("expected truncation error")
	} else if pe, ok := err.(*ParseError); !ok || pe.Err != ErrTruncated {
		t.Fatalf("error = %v, want ErrTruncated", err)
	}
	// Length prefix itself truncated.
	if _, _, err := ConsumeBytes([]byte{0x80}); err == nil {
		t.Fatal("expected truncation error for truncated length prefix")
	}
}

func TestFieldsMultiLevelNesting(t *testing.T) {
	// Level 3 (innermost): field 1 varint = 42
	l3 := AppendTag(nil, 1, WireVarint)
	l3 = AppendVarint(l3, 42)
	// Level 2: field 1 message(l3), field 2 fixed32
	l2 := AppendTag(nil, 1, WireBytes)
	l2 = AppendBytes(l2, l3)
	l2 = AppendTag(l2, 2, WireFixed32)
	l2 = AppendFixed32(l2, 0xcafe)
	// Level 1: field 1 message(l2), field 2 zigzag varint, field 3 string
	l1 := AppendTag(nil, 1, WireBytes)
	l1 = AppendBytes(l1, l2)
	l1 = AppendTag(l1, 2, WireVarint)
	l1 = AppendVarint(l1, EncodeZigZag64(-77))
	l1 = AppendTag(l1, 3, WireBytes)
	l1 = AppendBytes(l1, []byte("hello"))

	top, err := Fields(l1)
	if err != nil {
		t.Fatalf("parse level 1: %v", err)
	}
	if len(top) != 3 {
		t.Fatalf("level 1 field count = %d, want 3", len(top))
	}
	if top[1].Number != 2 || DecodeZigZag64(top[1].Varint) != -77 {
		t.Errorf("zigzag field = %+v", top[1])
	}
	if string(top[2].Bytes) != "hello" {
		t.Errorf("string field = %q", top[2].Bytes)
	}
	mid, err := Fields(top[0].Bytes)
	if err != nil {
		t.Fatalf("parse level 2: %v", err)
	}
	if len(mid) != 2 || mid[1].Fixed32 != 0xcafe {
		t.Fatalf("level 2 fields = %+v", mid)
	}
	inner, err := Fields(mid[0].Bytes)
	if err != nil {
		t.Fatalf("parse level 3: %v", err)
	}
	if len(inner) != 1 || inner[0].Varint != 42 {
		t.Fatalf("level 3 fields = %+v", inner)
	}
}

func TestPackedRepeated(t *testing.T) {
	// Packed varints.
	var payload []byte
	for _, v := range []uint64{1, 300, 2, math.MaxUint64} {
		payload = AppendVarint(payload, v)
	}
	msg := AppendTag(nil, 4, WireBytes)
	msg = AppendBytes(msg, payload)
	fs, err := Fields(msg)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	vs, err := ConsumePackedVarints(fs[0].Bytes)
	if err != nil {
		t.Fatalf("packed varints: %v", err)
	}
	want := []uint64{1, 300, 2, math.MaxUint64}
	if len(vs) != len(want) {
		t.Fatalf("packed varints = %v, want %v", vs, want)
	}
	for i := range want {
		if vs[i] != want[i] {
			t.Fatalf("packed varints = %v, want %v", vs, want)
		}
	}

	// Packed fixed32 with a truncated tail must fail.
	bad := AppendFixed32(nil, 1)
	bad = append(bad, 0x00, 0x00) // 6 bytes: not a multiple of 4
	if _, err := ConsumePackedFixed32(bad); err == nil {
		t.Error("truncated packed fixed32 accepted")
	}
	// Packed varint with truncated final element must fail.
	if _, err := ConsumePackedVarints([]byte{0x01, 0x80}); err == nil {
		t.Error("truncated packed varint accepted")
	}
	// Packed fixed64 wrong length must fail.
	if _, err := ConsumePackedFixed64(make([]byte, 9)); err == nil {
		t.Error("misaligned packed fixed64 accepted")
	}
}

// TestTruncationSweep proves bounds safety: every possible truncation of a
// valid multi-level message must produce an error (or, for length 0, empty
// output) and must never panic.
func TestTruncationSweep(t *testing.T) {
	inner := AppendTag(nil, 1, WireVarint)
	inner = AppendVarint(inner, 7)
	mid := AppendTag(nil, 1, WireBytes)
	mid = AppendBytes(mid, inner)
	mid = AppendTag(mid, 2, WireFixed64)
	mid = AppendFixed64(mid, 0x0102030405060708)
	full := AppendTag(nil, 1, WireBytes)
	full = AppendBytes(full, mid)
	full = AppendTag(full, 2, WireVarint)
	full = AppendVarint(full, 999)
	full = AppendTag(full, 3, WireBytes)
	full = AppendBytes(full, []byte{0xaa, 0xbb, 0xcc})

	if _, err := Fields(full); err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}
	// Compute the offsets that fall exactly on top-level field boundaries;
	// truncating there yields a valid shorter message. Every other cut
	// lands inside a field and must be reported as a bounds violation.
	boundaries := map[int]bool{0: true}
	rest := full
	off := 0
	for len(rest) > 0 {
		_, n, err := NextField(rest)
		if err != nil {
			t.Fatalf("boundary scan: %v", err)
		}
		off += n
		boundaries[off] = true
		rest = rest[n:]
	}
	for i := 0; i <= len(full); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on truncation to %d bytes: %v", i, r)
				}
			}()
			_, err := Fields(full[:i])
			if boundaries[i] && err != nil {
				t.Fatalf("boundary truncation to %d bytes errored: %v", i, err)
			}
			if !boundaries[i] && err == nil {
				t.Fatalf("mid-field truncation to %d bytes parsed without error", i)
			}
		}()
	}
}
