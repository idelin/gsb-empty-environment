package wire

import (
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 1<<21 - 1, 1 << 21, 1<<35 - 1, 1 << 35,
		math.MaxUint32, math.MaxUint64 - 1, math.MaxUint64}
	for _, want := range cases {
		buf := AppendVarint(nil, want)
		got, n, err := ConsumeVarint(buf)
		if err != nil {
			t.Fatalf("ConsumeVarint(%d) error: %v", want, err)
		}
		if got != want || n != len(buf) {
			t.Fatalf("varint round trip: got (%d,%d), want (%d,%d)", got, n, want, len(buf))
		}
	}
}

func TestVarintCanonicalSizes(t *testing.T) {
	cases := map[uint64]int{0: 1, 127: 1, 128: 2, 300: 2, math.MaxUint32: 5, math.MaxUint64: 10}
	for v, wantLen := range cases {
		if got := len(AppendVarint(nil, v)); got != wantLen {
			t.Fatalf("len(AppendVarint(%d)) = %d, want %d", v, got, wantLen)
		}
	}
}

func TestVarintTruncationEveryPrefix(t *testing.T) {
	full := AppendVarint(nil, math.MaxUint64) // 10 bytes
	for cut := 0; cut < len(full); cut++ {
		_, _, err := ConsumeVarint(full[:cut])
		if !Is(err, ErrTruncated) {
			t.Fatalf("prefix %d: got %v, want ErrTruncated", cut, err)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10th byte > 1 overflows 64 bits.
	buf := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, _, err := ConsumeVarint(buf); !Is(err, ErrOverflow) {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
	// 11-byte encoding with continuation past the 10th byte.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x82, 0x01}
	if _, _, err := ConsumeVarint(buf); !Is(err, ErrOverflow) {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
}

func TestZigZag(t *testing.T) {
	cases64 := []struct {
		v int64
		u uint64
	}{{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, 0xfffffffffffffffe}, {math.MinInt64, 0xffffffffffffffff}}
	for _, c := range cases64 {
		if got := EncodeZigZag64(c.v); got != c.u {
			t.Fatalf("EncodeZigZag64(%d) = %d, want %d", c.v, got, c.u)
		}
		if got := DecodeZigZag64(c.u); got != c.v {
			t.Fatalf("DecodeZigZag64(%d) = %d, want %d", c.u, got, c.v)
		}
	}
	cases32 := []struct {
		v int32
		u uint64
	}{{0, 0}, {-1, 1}, {1, 2}, {math.MaxInt32, 0xfffffffe}, {math.MinInt32, 0xffffffff}}
	for _, c := range cases32 {
		if got := EncodeZigZag32(c.v); got != c.u {
			t.Fatalf("EncodeZigZag32(%d) = %d, want %d", c.v, got, c.u)
		}
		if got := DecodeZigZag32(c.u); got != c.v {
			t.Fatalf("DecodeZigZag32(%d) = %d, want %d", c.u, got, c.v)
		}
	}
}

func TestFixedRoundTripAndTruncation(t *testing.T) {
	b32 := AppendFixed32(nil, 0xdeadbeef)
	if v, n, err := ConsumeFixed32(b32); err != nil || v != 0xdeadbeef || n != 4 {
		t.Fatalf("fixed32 round trip failed: %x %d %v", v, n, err)
	}
	for cut := 0; cut < 4; cut++ {
		if _, _, err := ConsumeFixed32(b32[:cut]); !Is(err, ErrTruncated) {
			t.Fatalf("fixed32 prefix %d: got %v, want ErrTruncated", cut, err)
		}
	}
	b64 := AppendFixed64(nil, 0x0123456789abcdef)
	if v, n, err := ConsumeFixed64(b64); err != nil || v != 0x0123456789abcdef || n != 8 {
		t.Fatalf("fixed64 round trip failed: %x %d %v", v, n, err)
	}
	for cut := 0; cut < 8; cut++ {
		if _, _, err := ConsumeFixed64(b64[:cut]); !Is(err, ErrTruncated) {
			t.Fatalf("fixed64 prefix %d: got %v, want ErrTruncated", cut, err)
		}
	}
}

// buildSampleMessage hand-encodes one field of each wire type.
func buildSampleMessage() []byte {
	var buf []byte
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 300)
	buf = AppendTag(buf, 2, WireFixed64)
	buf = AppendFixed64(buf, 0xfeedfacecafef00d)
	buf = AppendTag(buf, 3, WireBytes)
	buf = AppendBytes(buf, []byte("hello"))
	buf = AppendTag(buf, 4, WireFixed32)
	buf = AppendFixed32(buf, 0x0badf00d)
	return buf
}

func TestParseMessageAllWireTypes(t *testing.T) {
	fields, err := ParseMessage(buildSampleMessage())
	if err != nil {
		t.Fatalf("ParseMessage: %v", err)
	}
	if len(fields) != 4 {
		t.Fatalf("got %d fields, want 4", len(fields))
	}
	if fields[0].Number != 1 || fields[0].Type != WireVarint || fields[0].Varint != 300 {
		t.Fatalf("field 1 mismatch: %+v", fields[0])
	}
	if fields[1].Number != 2 || fields[1].Type != WireFixed64 || fields[1].Fixed64 != 0xfeedfacecafef00d {
		t.Fatalf("field 2 mismatch: %+v", fields[1])
	}
	if fields[2].Number != 3 || fields[2].Type != WireBytes || string(fields[2].Bytes) != "hello" {
		t.Fatalf("field 3 mismatch: %+v", fields[2])
	}
	if fields[3].Number != 4 || fields[3].Type != WireFixed32 || fields[3].Fixed32 != 0x0badf00d {
		t.Fatalf("field 4 mismatch: %+v", fields[3])
	}
}

// TestParseMessageTruncationSafety cuts the stream at every byte offset and
// requires a clean ErrTruncated (or other decode error) instead of a panic.
func TestParseMessageTruncationSafety(t *testing.T) {
	full := buildSampleMessage()
	for cut := 0; cut < len(full); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on truncated input at offset %d: %v", cut, r)
				}
			}()
			// cut==0 is the empty (valid) message; cuts landing exactly on
			// field boundaries are valid shorter messages. Any error that
			// does occur must be a truncation, never a panic.
			if _, err := ParseMessage(full[:cut]); err != nil && !Is(err, ErrTruncated) {
				t.Fatalf("offset %d: got %v, want wrapped ErrTruncated", cut, err)
			}
		}()
	}
	// A guaranteed mid-field cut (inside the trailing fixed32) must fail.
	if _, err := ParseMessage(full[:len(full)-1]); !Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestParseMessageLengthPrefixOverrun(t *testing.T) {
	// Length prefix claims 100 bytes, buffer holds 2.
	var buf []byte
	buf = AppendTag(buf, 1, WireBytes)
	buf = AppendVarint(buf, 100)
	buf = append(buf, 0x41, 0x42)
	_, err := ParseMessage(buf)
	if !Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}

func TestParseMessageBadWireTypes(t *testing.T) {
	for _, wt := range []WireType{3, 4, 6, 7} {
		buf := AppendTag(nil, 1, wt)
		if _, err := ParseMessage(buf); !Is(err, ErrBadWireType) {
			t.Fatalf("wire type %d: got %v, want ErrBadWireType", wt, err)
		}
	}
}

func TestParseMessageBadFieldNumber(t *testing.T) {
	buf := AppendTag(nil, 0, WireVarint) // field 0 is illegal
	buf = AppendVarint(buf, 1)
	if _, err := ParseMessage(buf); !Is(err, ErrBadFieldNumber) {
		t.Fatalf("got %v, want ErrBadFieldNumber", err)
	}
	buf = AppendTag(nil, MaxFieldNumber+1, WireVarint)
	buf = AppendVarint(buf, 1)
	if _, err := ParseMessage(buf); !Is(err, ErrBadFieldNumber) {
		t.Fatalf("got %v, want ErrBadFieldNumber", err)
	}
	// MaxFieldNumber itself is legal.
	buf = AppendTag(nil, MaxFieldNumber, WireVarint)
	buf = AppendVarint(buf, 1)
	fields, err := ParseMessage(buf)
	if err != nil || len(fields) != 1 || fields[0].Number != MaxFieldNumber {
		t.Fatalf("max field number rejected: %v %+v", err, fields)
	}
}

func TestParseMessageTruncatedTag(t *testing.T) {
	// Tag varint with continuation bit but no successor.
	buf := []byte{0x80}
	if _, err := ParseMessage(buf); !Is(err, ErrTruncated) {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}
