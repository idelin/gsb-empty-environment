package wire

import (
	"errors"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	values := []uint64{0, 1, 127, 128, 300, 16383, 16384, 1 << 21, 1 << 32, 1 << 49, math.MaxUint64}
	for _, v := range values {
		buf := AppendVarint(nil, v)
		got, n, err := DecodeVarint(buf)
		if err != nil {
			t.Fatalf("DecodeVarint(%d): %v", v, err)
		}
		if got != v || n != len(buf) {
			t.Fatalf("round trip %d: got %d, consumed %d of %d", v, got, n, len(buf))
		}
	}
}

func TestVarintSizes(t *testing.T) {
	cases := map[uint64]int{0: 1, 127: 1, 128: 2, 300: 2, 1<<21 - 1: 3, 1 << 32: 5, math.MaxUint64: 10}
	for v, want := range cases {
		if n := len(AppendVarint(nil, v)); n != want {
			t.Errorf("size(%d) = %d, want %d", v, n, want)
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	full := AppendVarint(nil, 300) // 0xAC 0x02
	if _, _, err := DecodeVarint(full[:1]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	if _, _, err := DecodeVarint(nil); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty input: want ErrTruncated, got %v", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10 continuation bytes with no terminator.
	ten := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
	if _, _, err := DecodeVarint(ten); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 10th byte carries bits beyond 64.
	over := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, _, err := DecodeVarint(over); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 10th byte == 1 is the legal maximum.
	max := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	v, n, err := DecodeVarint(max)
	if err != nil || v != math.MaxUint64 || n != 10 {
		t.Fatalf("max varint: v=%d n=%d err=%v", v, n, err)
	}
}

func TestZigZag(t *testing.T) {
	cases := map[int64]uint64{0: 0, -1: 1, 1: 2, -2: 3, 2: 4,
		math.MaxInt64: math.MaxUint64 - 1, math.MinInt64: math.MaxUint64}
	for v, want := range cases {
		if got := ZigZagEncode(v); got != want {
			t.Errorf("ZigZagEncode(%d) = %d, want %d", v, got, want)
		}
		if got := ZigZagDecode(want); got != v {
			t.Errorf("ZigZagDecode(%d) = %d, want %d", want, got, v)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	b32 := AppendFixed32(nil, 0xDEADBEEF)
	if v, err := DecodeFixed32(b32); err != nil || v != 0xDEADBEEF {
		t.Fatalf("fixed32: %x %v", v, err)
	}
	b64 := AppendFixed64(nil, 0x0123456789ABCDEF)
	if v, err := DecodeFixed64(b64); err != nil || v != 0x0123456789ABCDEF {
		t.Fatalf("fixed64: %x %v", v, err)
	}
	for i := 0; i < 4; i++ {
		if _, err := DecodeFixed32(b32[:i]); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed32[:%d]: want ErrTruncated, got %v", i, err)
		}
	}
	for i := 0; i < 8; i++ {
		if _, err := DecodeFixed64(b64[:i]); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed64[:%d]: want ErrTruncated, got %v", i, err)
		}
	}
}

// buildSample hand-encodes a 3-level nested message with a packed array:
//
//	outer{ id:1 varint, inner:2 msg{ name:1 string, scores:2 packed varint,
//	         leaf:3 msg{ flag:1 varint } } }
func buildSample() []byte {
	var leaf []byte
	leaf = AppendTag(leaf, 1, Varint)
	leaf = AppendVarint(leaf, 1)

	var packed []byte
	for _, v := range []uint64{7, 8, 9} {
		packed = AppendVarint(packed, v)
	}

	var inner []byte
	inner = AppendTag(inner, 1, LengthDelimited)
	inner = AppendBytes(inner, []byte("node"))
	inner = AppendTag(inner, 2, LengthDelimited)
	inner = AppendBytes(inner, packed)
	inner = AppendTag(inner, 3, LengthDelimited)
	inner = AppendBytes(inner, leaf)

	var outer []byte
	outer = AppendTag(outer, 1, Varint)
	outer = AppendVarint(outer, 42)
	outer = AppendTag(outer, 2, LengthDelimited)
	outer = AppendBytes(outer, inner)
	return outer
}

func TestParseNestedMessage(t *testing.T) {
	fields, err := Parse(buildSample())
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields[0].Number != 1 || fields[0].Value != 42 {
		t.Fatalf("outer: %+v", fields)
	}
	inner, err := Parse(fields[1].Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(inner) != 3 || string(inner[0].Bytes) != "node" {
		t.Fatalf("inner: %+v", inner)
	}
	scores, err := DecodePackedVarint(inner[1].Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 3 || scores[0] != 7 || scores[2] != 9 {
		t.Fatalf("packed: %v", scores)
	}
	leaf, err := Parse(inner[2].Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf) != 1 || leaf[0].Value != 1 {
		t.Fatalf("leaf: %+v", leaf)
	}
}

func TestParseTruncationGuards(t *testing.T) {
	full := buildSample()
	// Every strict prefix must be handled cleanly: never panic, never
	// read out of bounds. (A prefix may itself be a valid shorter
	// message, so only panics are failures here.)
	for cut := 1; cut < len(full); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix %d panicked: %v", cut, r)
				}
			}()
			_, _ = Parse(full[:cut])
		}()
	}
	// Prefixes that cut inside a field must report an error.
	for _, cut := range []int{1, 3, 4, len(full) - 1} {
		if _, err := Parse(full[:cut]); err == nil {
			t.Fatalf("prefix %d cuts inside a field but parsed without error", cut)
		}
	}
	// The error must identify the truncated field.
	_, err := Parse(full[:len(full)-1])
	var fe *FieldError
	if !errors.As(err, &fe) || !errors.Is(err, ErrTruncated) {
		t.Fatalf("want *FieldError wrapping ErrTruncated, got %v", err)
	}
}

func TestParseTruncatedPerWireType(t *testing.T) {
	cases := map[string][]byte{
		"tag only":            {0x08},       // field 1 varint, payload missing
		"varint continuation": {0x08, 0x80}, // unterminated varint
		"fixed64 short":       {0x09, 1, 2, 3},
		"length missing":      {0x12},            // field 2 LEN, no length
		"length overruns":     {0x12, 0x05, 'a'}, // declares 5, has 1
		"fixed32 short":       {0x15, 1, 2},      // field 2 fixed32, 2 of 4
		"zero field number":   {0x00, 0x01},
		"bad wire type 3":     {0x0b, 0x01},
		"bad wire type 7":     {0x0f, 0x01},
	}
	for name, buf := range cases {
		if _, err := Parse(buf); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestPackedFixedAlignment(t *testing.T) {
	if _, err := DecodePackedFixed32([]byte{1, 2, 3}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("misaligned fixed32: %v", err)
	}
	if _, err := DecodePackedFixed64([]byte{1, 2, 3, 4, 5}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("misaligned fixed64: %v", err)
	}
	b := AppendFixed32(AppendFixed32(nil, 11), 22)
	got, err := DecodePackedFixed32(b)
	if err != nil || len(got) != 2 || got[0] != 11 || got[1] != 22 {
		t.Fatalf("packed fixed32: %v %v", got, err)
	}
	if _, err := DecodePackedVarint([]byte{0x80}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("packed varint truncated: %v", err)
	}
}
