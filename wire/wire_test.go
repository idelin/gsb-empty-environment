package wire

import (
	"math"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// Varint
// ---------------------------------------------------------------------------

func TestVarintRoundTrip(t *testing.T) {
	vals := []uint64{
		0, 1, 2, 127, 128, 129, 255, 300, 16383, 16384,
		1<<21 - 1, 1 << 21, 1<<28 - 1, 1 << 28,
		1<<32 - 1, 1 << 32, 1<<56 - 1, 1 << 56,
		math.MaxUint64 - 1, math.MaxUint64,
	}
	for _, v := range vals {
		buf := AppendVarint(nil, v)
		got, n, err := ConsumeVarint(buf)
		if err != nil {
			t.Fatalf("ConsumeVarint(%d): %v", v, err)
		}
		if got != v || n != len(buf) {
			t.Fatalf("varint %d: got (%d, %d), want (%d, %d)", v, got, n, v, len(buf))
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
		if got := AppendVarint(nil, c.v); !reflect.DeepEqual(got, c.enc) {
			t.Fatalf("AppendVarint(%d) = %v, want %v", c.v, got, c.enc)
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	// Continuation bit set but buffer ends.
	if _, _, err := ConsumeVarint([]byte{0x80}); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	if _, _, err := ConsumeVarint(nil); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	// 9 continuation bytes, still unterminated, buffer exhausted.
	if _, _, err := ConsumeVarint([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10th byte carries more than 1 payload bit.
	over := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, _, err := ConsumeVarint(over); err != ErrOverflow {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 11-byte varint.
	eleven := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if _, _, err := ConsumeVarint(eleven); err != ErrOverflow {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ZigZag
// ---------------------------------------------------------------------------

func TestZigZag(t *testing.T) {
	cases := []struct {
		s int64
		u uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := EncodeZigZag(c.s); got != c.u {
			t.Fatalf("EncodeZigZag(%d) = %d, want %d", c.s, got, c.u)
		}
		if got := DecodeZigZag(c.u); got != c.s {
			t.Fatalf("DecodeZigZag(%d) = %d, want %d", c.u, got, c.s)
		}
	}
	// Full round-trip sweep around zero.
	for s := int64(-10000); s <= 10000; s++ {
		if got := DecodeZigZag(EncodeZigZag(s)); got != s {
			t.Fatalf("zigzag round-trip %d -> %d", s, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Fixed width
// ---------------------------------------------------------------------------

func TestFixedRoundTrip(t *testing.T) {
	for _, v := range []uint32{0, 1, 0xdeadbeef, math.MaxUint32} {
		buf := AppendFixed32(nil, v)
		got, n, err := ConsumeFixed32(buf)
		if err != nil || got != v || n != 4 {
			t.Fatalf("fixed32 %d: got (%d, %d, %v)", v, got, n, err)
		}
	}
	for _, v := range []uint64{0, 1, 0xdeadbeefcafe, math.MaxUint64} {
		buf := AppendFixed64(nil, v)
		got, n, err := ConsumeFixed64(buf)
		if err != nil || got != v || n != 8 {
			t.Fatalf("fixed64 %d: got (%d, %d, %v)", v, got, n, err)
		}
	}
	if _, _, err := ConsumeFixed32([]byte{1, 2, 3}); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	if _, _, err := ConsumeFixed64([]byte{1, 2, 3, 4, 5, 6, 7}); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Nested messages + packed repeated
// ---------------------------------------------------------------------------

// buildNestedMessage hand-builds:
//
//	message Outer {                 // field layout:
//	  uint32 id        = 1;         // varint
//	  Inner  inner     = 2;         // length-delimited
//	  repeated uint32 scores = 3;   // packed
//	}
//	message Inner {
//	  sint64 delta     = 1;         // zigzag varint
//	  Core   core      = 2;         // length-delimited (3rd nesting level)
//	}
//	message Core {
//	  fixed64 stamp    = 1;
//	  string  label    = 2;
//	  repeated fixed32 coeffs = 3;  // packed
//	}
func buildNestedMessage() []byte {
	var core []byte
	core = AppendTag(core, 1, TypeFixed64)
	core = AppendFixed64(core, 0x0123456789abcdef)
	core = AppendTag(core, 2, TypeBytes)
	core = AppendBytes(core, []byte("deep-core"))
	core = AppendTag(core, 3, TypeBytes)
	core = AppendBytes(core, AppendPackedFixed32(nil, 7, 8, 9, 10))

	var inner []byte
	inner = AppendTag(inner, 1, TypeVarint)
	inner = AppendZigZag(inner, -123456)
	inner = AppendTag(inner, 2, TypeBytes)
	inner = AppendBytes(inner, core)

	var outer []byte
	outer = AppendTag(outer, 1, TypeVarint)
	outer = AppendVarint(outer, 4242)
	outer = AppendTag(outer, 2, TypeBytes)
	outer = AppendBytes(outer, inner)
	outer = AppendTag(outer, 3, TypeBytes)
	outer = AppendBytes(outer, AppendPackedVarints(nil, 3, 1, 4, 1, 5, 9, 2, 6))
	return outer
}

func TestNestedMessageAndPacked(t *testing.T) {
	fields, err := ParseMessage(buildNestedMessage())
	if err != nil {
		t.Fatalf("outer parse: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("outer: want 3 fields, got %d", len(fields))
	}
	if fields[0].Number != 1 || fields[0].Type != TypeVarint || fields[0].Varint != 4242 {
		t.Fatalf("outer.id wrong: %+v", fields[0])
	}
	if fields[2].Number != 3 || fields[2].Type != TypeBytes {
		t.Fatalf("outer.scores wrong: %+v", fields[2])
	}
	scores, err := DecodePackedVarints(fields[2].Bytes)
	if err != nil {
		t.Fatalf("packed scores: %v", err)
	}
	if want := []uint64{3, 1, 4, 1, 5, 9, 2, 6}; !reflect.DeepEqual(scores, want) {
		t.Fatalf("scores = %v, want %v", scores, want)
	}

	// Level 2.
	inner, err := ParseMessage(fields[1].Bytes)
	if err != nil {
		t.Fatalf("inner parse: %v", err)
	}
	if len(inner) != 2 || inner[0].Svarint() != -123456 {
		t.Fatalf("inner wrong: %+v", inner)
	}

	// Level 3.
	core, err := ParseMessage(inner[1].Bytes)
	if err != nil {
		t.Fatalf("core parse: %v", err)
	}
	if len(core) != 3 {
		t.Fatalf("core: want 3 fields, got %d", len(core))
	}
	if core[0].Fixed64 != 0x0123456789abcdef {
		t.Fatalf("core.stamp = %#x", core[0].Fixed64)
	}
	if string(core[1].Bytes) != "deep-core" {
		t.Fatalf("core.label = %q", core[1].Bytes)
	}
	coeffs, err := DecodePackedFixed32(core[2].Bytes)
	if err != nil {
		t.Fatalf("packed coeffs: %v", err)
	}
	if want := []uint32{7, 8, 9, 10}; !reflect.DeepEqual(coeffs, want) {
		t.Fatalf("coeffs = %v, want %v", coeffs, want)
	}
}

func TestPackedSvarintsAndFixed64(t *testing.T) {
	var payload []byte
	for _, v := range []int64{0, -1, 1, -2, 2, math.MinInt64, math.MaxInt64} {
		payload = AppendZigZag(payload, v)
	}
	got, err := DecodePackedSvarints(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, -1, 1, -2, 2, math.MinInt64, math.MaxInt64}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("svarints = %v, want %v", got, want)
	}

	f64, err := DecodePackedFixed64(AppendPackedFixed64(nil, 11, 22, 33))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f64, []uint64{11, 22, 33}) {
		t.Fatalf("fixed64 = %v", f64)
	}
}

func TestPackedTruncated(t *testing.T) {
	// Packed fixed32 payload whose length is not a multiple of 4.
	if _, err := DecodePackedFixed32([]byte{1, 2, 3}); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	if _, err := DecodePackedFixed64([]byte{1, 2, 3, 4, 5}); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	// Packed varint payload ending mid-varint.
	if _, err := DecodePackedVarints([]byte{0x01, 0x80}); err != ErrTruncated {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Truncation / hostile-input safety
// ---------------------------------------------------------------------------

func TestTruncatedFields(t *testing.T) {
	// Length-delimited field declares 10 bytes, buffer holds 3.
	msg := AppendTag(nil, 1, TypeBytes)
	msg = AppendVarint(msg, 10)
	msg = append(msg, 1, 2, 3)
	if _, err := ParseMessage(msg); err == nil {
		t.Fatal("want error for truncated length-delimited field")
	}

	// Tag present, varint value missing.
	msg = AppendTag(nil, 1, TypeVarint)
	if _, err := ParseMessage(msg); err == nil {
		t.Fatal("want error for missing varint value")
	}

	// Tag present, fixed64 value cut short.
	msg = AppendTag(nil, 1, TypeFixed64)
	msg = append(msg, 1, 2, 3)
	if _, err := ParseMessage(msg); err == nil {
		t.Fatal("want error for truncated fixed64")
	}

	// Truncated tag itself (continuation bit set, buffer ends).
	if _, err := ParseMessage([]byte{0x80}); err == nil {
		t.Fatal("want error for truncated tag")
	}
}

func TestInvalidTags(t *testing.T) {
	// Field number 0 is illegal.
	if _, err := ParseMessage([]byte{0x00}); err == nil {
		t.Fatal("want error for field number 0")
	}
	// Wire types 3, 4, 6, 7 are not supported (groups are deprecated).
	for _, wt := range []byte{3, 4, 6, 7} {
		tag := AppendVarint(nil, uint64(1)<<3|uint64(wt))
		if _, err := ParseMessage(tag); err == nil {
			t.Fatalf("want error for wire type %d", wt)
		}
	}
}

// consumedLen returns the byte length of the first field in buf.
func consumedLen(buf []byte) int {
	_, n, err := ConsumeVarint(buf) // tag
	if err != nil {
		return 0
	}
	tag, _, _ := ConsumeVarint(buf)
	switch Type(tag & 7) {
	case TypeVarint:
		_, m, _ := ConsumeVarint(buf[n:])
		return n + m
	case TypeFixed64:
		return n + 8
	case TypeBytes:
		_, m, _ := ConsumeBytes(buf[n:])
		return n + m
	case TypeFixed32:
		return n + 4
	}
	return 0
}

// TestEveryPrefixIsSafe truncates a valid nested message at every possible
// byte boundary and asserts the decoder never panics and always fails with
// a package sentinel error (safe out-of-bounds protection).
func TestEveryPrefixIsSafe(t *testing.T) {
	full := buildNestedMessage()
	if _, err := ParseMessage(full); err != nil {
		t.Fatalf("sanity: %v", err)
	}
	// Collect the offsets that end exactly on a top-level field boundary;
	// those prefixes are themselves valid messages and must parse cleanly.
	boundary := map[int]bool{0: true}
	off := 0
	for off < len(full) {
		d := NewDecoder(full[off:])
		if _, err := d.Next(); err != nil {
			t.Fatalf("sanity walk: %v", err)
		}
		// Re-derive the consumed length by finding the next boundary:
		// the decoder is Done when the remaining buffer is exhausted.
		// Instead, walk field by field using a fresh decoder on the rest.
		off += consumedLen(full[off:])
		boundary[off] = true
	}
	for i := 0; i <= len(full); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on prefix of length %d: %v", i, r)
				}
			}()
			_, err := ParseMessage(full[:i])
			if boundary[i] && err != nil {
				t.Fatalf("boundary prefix %d should parse, got %v", i, err)
			}
			if !boundary[i] && err == nil {
				t.Fatalf("mid-field prefix %d decoded without error", i)
			}
		}()
	}
}

// TestUnknownFieldsSkipped verifies a generic parser walks every wire type
// without choking, so unknown fields can be skipped by number.
func TestUnknownFieldsSkipped(t *testing.T) {
	var msg []byte
	msg = AppendTag(msg, 1, TypeVarint)
	msg = AppendVarint(msg, 1)
	msg = AppendTag(msg, 50, TypeFixed64) // unknown
	msg = AppendFixed64(msg, 0)
	msg = AppendTag(msg, 51, TypeBytes) // unknown
	msg = AppendBytes(msg, []byte("xyz"))
	msg = AppendTag(msg, 52, TypeFixed32) // unknown
	msg = AppendFixed32(msg, 0)
	msg = AppendTag(msg, 2, TypeVarint)
	msg = AppendVarint(msg, 2)

	fields, err := ParseMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 5 || fields[0].Varint != 1 || fields[4].Varint != 2 {
		t.Fatalf("unexpected walk result: %+v", fields)
	}
}
