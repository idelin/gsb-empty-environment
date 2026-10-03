package wirecodec

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	values := []uint64{
		0, 1, 2, 127, 128, 129, 300, 16383, 16384,
		1<<21 - 1, 1 << 21, 1<<28 - 1, 1 << 28,
		1<<35 - 1, 1 << 35, 1<<63 - 1, 1 << 63,
		math.MaxUint64 - 1, math.MaxUint64,
	}
	for _, v := range values {
		enc := AppendVarint(nil, v)
		r := NewReader(enc)
		got, err := r.Varint()
		if err != nil {
			t.Fatalf("Varint(%d) decode error: %v", v, err)
		}
		if got != v {
			t.Fatalf("Varint round trip: got %d, want %d", got, v)
		}
		if !r.Done() {
			t.Fatalf("Varint(%d): %d trailing bytes", v, r.Remaining())
		}
	}
}

func TestVarintCanonicalSizes(t *testing.T) {
	cases := []struct {
		v    uint64
		size int
	}{
		{0, 1}, {127, 1}, {128, 2}, {300, 2},
		{1<<21 - 1, 3}, {1 << 21, 4}, {1<<63 - 1, 9}, {math.MaxUint64, 10},
	}
	for _, c := range cases {
		if got := len(AppendVarint(nil, c.v)); got != c.size {
			t.Errorf("varint(%d) size = %d, want %d", c.v, got, c.size)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: definitely overflows 64 bits.
	eleven := bytes.Repeat([]byte{0x80}, 10)
	eleven = append(eleven, 0x01)
	if _, err := NewReader(eleven).Varint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("11-byte varint: got %v, want ErrOverflow", err)
	}
	// 10th byte > 1: top bits exceed 64.
	ten := append(bytes.Repeat([]byte{0xff}, 9), 0x02)
	if _, err := NewReader(ten).Varint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("10th byte 0x02: got %v, want ErrOverflow", err)
	}
	// 10th byte == 1 is the legal maximum (math.MaxUint64 >> 63 == 1 pattern).
	max := append(bytes.Repeat([]byte{0xff}, 9), 0x01)
	v, err := NewReader(max).Varint()
	if err != nil || v != math.MaxUint64 {
		t.Fatalf("max varint: got %d, %v; want %d, nil", v, err, uint64(math.MaxUint64))
	}
}

func TestVarintTruncated(t *testing.T) {
	full := AppendVarint(nil, math.MaxUint64) // 10 bytes
	for i := 0; i < len(full); i++ {
		if _, err := NewReader(full[:i]).Varint(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("prefix len %d: got %v, want ErrTruncated", i, err)
		}
	}
	if _, err := NewReader(nil).Varint(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty input: got %v, want ErrTruncated", err)
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		signed   int64
		unsigned uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt32, 2*math.MaxInt32 + 1 - 1},
		{math.MinInt32, 2*(-math.MinInt32) - 1},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := EncodeZigZag(c.signed); got != c.unsigned {
			t.Errorf("EncodeZigZag(%d) = %d, want %d", c.signed, got, c.unsigned)
		}
		if got := DecodeZigZag(c.unsigned); got != c.signed {
			t.Errorf("DecodeZigZag(%d) = %d, want %d", c.unsigned, got, c.signed)
		}
	}
	// Full round trip over edge values.
	for _, v := range []int64{0, 1, -1, 127, -128, math.MaxInt64, math.MinInt64, math.MaxInt32, math.MinInt32} {
		if got := DecodeZigZag(EncodeZigZag(v)); got != v {
			t.Errorf("zigzag round trip: got %d, want %d", got, v)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	w := NewWriter()
	w.Fixed32(0xdeadbeef)
	w.Fixed64(0x0123456789abcdef)
	w.Fixed32(0)
	w.Fixed64(math.MaxUint64)
	r := NewReader(w.Bytes())
	if v, err := r.Fixed32(); err != nil || v != 0xdeadbeef {
		t.Fatalf("fixed32: got %#x, %v", v, err)
	}
	if v, err := r.Fixed64(); err != nil || v != 0x0123456789abcdef {
		t.Fatalf("fixed64: got %#x, %v", v, err)
	}
	if v, err := r.Fixed32(); err != nil || v != 0 {
		t.Fatalf("fixed32 zero: got %#x, %v", v, err)
	}
	if v, err := r.Fixed64(); err != nil || v != math.MaxUint64 {
		t.Fatalf("fixed64 max: got %#x, %v", v, err)
	}
	if !r.Done() {
		t.Fatal("trailing bytes after fixed reads")
	}
}

func TestFixedTruncated(t *testing.T) {
	for i := 0; i < 4; i++ {
		if _, err := NewReader(make([]byte, i)).Fixed32(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed32 with %d bytes: got %v, want ErrTruncated", i, err)
		}
	}
	for i := 0; i < 8; i++ {
		if _, err := NewReader(make([]byte, i)).Fixed64(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed64 with %d bytes: got %v, want ErrTruncated", i, err)
		}
	}
}

func TestFloatDoubleRoundTrip(t *testing.T) {
	w := NewWriter()
	w.DoubleField(1, math.Pi)
	w.Tag(2, WireFixed32)
	w.Fixed32(math.Float32bits(2.5))
	r := NewReader(w.Bytes())
	f1, wt1, err := r.Tag()
	if err != nil || f1 != 1 || wt1 != WireFixed64 {
		t.Fatalf("tag1: %d %d %v", f1, wt1, err)
	}
	if v, err := r.Double(); err != nil || v != math.Pi {
		t.Fatalf("double: got %v, %v", v, err)
	}
	if _, _, err := r.Tag(); err != nil {
		t.Fatal(err)
	}
	if v, err := r.Float(); err != nil || v != 2.5 {
		t.Fatalf("float: got %v, %v", v, err)
	}
}

func TestBytesHostileLength(t *testing.T) {
	// Length prefix claims 4GB but the buffer holds 3 bytes.
	data := append(AppendVarint(nil, 1<<32), 1, 2, 3)
	if _, err := NewReader(data).Bytes(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("hostile length: got %v, want ErrTruncated", err)
	}
	// Length prefix is itself truncated.
	if _, err := NewReader([]byte{0x80}).Bytes(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated length prefix: got %v, want ErrTruncated", err)
	}
	// Overflowing length prefix.
	if _, err := NewReader(bytes.Repeat([]byte{0xff}, 11)).Bytes(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflowing length prefix: got %v, want ErrOverflow", err)
	}
}

func TestTagValidation(t *testing.T) {
	// Field number 0 is illegal.
	if _, _, err := NewReader([]byte{0x00}).Tag(); !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("field 0: got %v, want ErrInvalidTag", err)
	}
	// Field number above 2^29-1 is illegal.
	big := AppendVarint(nil, uint64(MaxFieldNum+1)<<3)
	if _, _, err := NewReader(big).Tag(); !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("field > max: got %v, want ErrInvalidTag", err)
	}
	// Max legal field number is accepted.
	ok := AppendVarint(nil, uint64(MaxFieldNum)<<3|WireVarint)
	f, wt, err := NewReader(ok).Tag()
	if err != nil || f != MaxFieldNum || wt != WireVarint {
		t.Fatalf("max field: got %d %d %v", f, wt, err)
	}
}

func TestSkipAllWireTypes(t *testing.T) {
	w := NewWriter()
	w.Tag(100, WireVarint)
	w.Varint(12345)
	w.Tag(101, WireFixed64)
	w.Fixed64(0xffffffffffffffff)
	w.Tag(102, WireBytes)
	w.WriteString("skip me")
	w.Tag(103, WireFixed32)
	w.Fixed32(0xcafebabe)
	w.Tag(1, WireVarint) // sentinel field we actually read
	w.Varint(42)

	r := NewReader(w.Bytes())
	for i := 0; i < 4; i++ {
		_, wt, err := r.Tag()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Skip(wt); err != nil {
			t.Fatalf("skip %d: %v", i, err)
		}
	}
	f, _, err := r.Tag()
	if err != nil || f != 1 {
		t.Fatalf("sentinel tag: %d %v", f, err)
	}
	if v, err := r.Varint(); err != nil || v != 42 {
		t.Fatalf("sentinel value: %d %v", v, err)
	}
	// Reserved wire types must be rejected.
	for _, wt := range []int{3, 4, 6, 7} {
		if err := NewReader(nil).Skip(wt); !errors.Is(err, ErrBadWireType) {
			t.Fatalf("skip wire type %d: got %v, want ErrBadWireType", wt, err)
		}
	}
}

func TestBytesZeroCopyAndCap(t *testing.T) {
	w := NewWriter()
	w.WriteString("payload")
	w.Varint(0xff) // trailing byte must not be reachable via append
	buf := w.Bytes()

	r := NewReader(buf)
	got, err := r.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("got %q", got)
	}
	// Zero-copy: returned slice aliases the input buffer.
	if &got[0] != &buf[1] {
		t.Fatal("Bytes() copied; want zero-copy alias of input")
	}
	// Capacity is capped at the payload so appends reallocate.
	if cap(got) != len(got) {
		t.Fatalf("cap = %d, want %d (uncapped slice could corrupt the stream)", cap(got), len(got))
	}
}
