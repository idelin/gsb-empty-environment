package wire

import (
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
		var w Writer
		w.AppendVarint(want)
		r := NewReader(w.Bytes())
		got, err := r.Varint()
		if err != nil {
			t.Fatalf("Varint(%d): %v", want, err)
		}
		if got != want {
			t.Fatalf("Varint round trip: got %d, want %d", got, want)
		}
		if !r.Done() {
			t.Fatalf("Varint(%d): %d trailing bytes", want, r.Remaining())
		}
	}
}

func TestVarintCanonicalSizes(t *testing.T) {
	cases := []struct {
		v    uint64
		size int
	}{
		{0, 1}, {127, 1}, {128, 2}, {300, 2},
		{1<<14 - 1, 2}, {1 << 14, 3},
		{1<<63 - 1, 9}, {1 << 63, 10}, {math.MaxUint64, 10},
	}
	for _, c := range cases {
		var w Writer
		w.AppendVarint(c.v)
		if w.Len() != c.size {
			t.Errorf("varint(%d): size %d, want %d", c.v, w.Len(), c.size)
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	// 0x80 has the continuation bit set but no follow-up byte.
	r := NewReader([]byte{0x80})
	if _, err := r.Varint(); !IsTruncated(err) {
		t.Fatalf("want TruncatedError, got %v", err)
	}
	// 9 continuation bytes then end of buffer.
	r = NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	if _, err := r.Varint(); !IsTruncated(err) {
		t.Fatalf("want TruncatedError, got %v", err)
	}
}

func TestVarintEmpty(t *testing.T) {
	r := NewReader(nil)
	if _, err := r.Varint(); !IsTruncated(err) {
		t.Fatalf("want TruncatedError, got %v", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 bytes: always overflow.
	buf := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if _, err := NewReader(buf).Varint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 10th byte carrying more than 1 bit.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, err := NewReader(buf).Varint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 10th byte == 0x01 is exactly MaxUint64's top bit: legal.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	v, err := NewReader(buf).Varint()
	if err != nil || v != math.MaxUint64 {
		t.Fatalf("max varint: v=%d err=%v", v, err)
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
		if got := EncodeZigZag(c.signed); got != c.unsigned {
			t.Errorf("EncodeZigZag(%d) = %d, want %d", c.signed, got, c.unsigned)
		}
		if got := DecodeZigZag(c.unsigned); got != c.signed {
			t.Errorf("DecodeZigZag(%d) = %d, want %d", c.unsigned, got, c.signed)
		}
	}
	// Full round trip through the wire.
	for _, v := range []int64{0, 1, -1, 63, -64, 300, -300, math.MaxInt64, math.MinInt64} {
		var w Writer
		w.AppendZigZag(v)
		got, err := NewReader(w.Bytes()).ZigZag()
		if err != nil || got != v {
			t.Fatalf("zigzag round trip %d: got %d, err %v", v, got, err)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	var w Writer
	w.AppendFixed32(0xdeadbeef)
	w.AppendFixed64(0x0123456789abcdef)
	r := NewReader(w.Bytes())
	v32, err := r.Fixed32()
	if err != nil || v32 != 0xdeadbeef {
		t.Fatalf("fixed32: %x %v", v32, err)
	}
	v64, err := r.Fixed64()
	if err != nil || v64 != 0x0123456789abcdef {
		t.Fatalf("fixed64: %x %v", v64, err)
	}
	if !r.Done() {
		t.Fatal("trailing bytes")
	}
}

func TestFixedTruncated(t *testing.T) {
	for n := 0; n < 4; n++ {
		if _, err := NewReader(make([]byte, n)).Fixed32(); !IsTruncated(err) {
			t.Fatalf("fixed32 with %d bytes: want TruncatedError, got %v", n, err)
		}
	}
	for n := 0; n < 8; n++ {
		if _, err := NewReader(make([]byte, n)).Fixed64(); !IsTruncated(err) {
			t.Fatalf("fixed64 with %d bytes: want TruncatedError, got %v", n, err)
		}
	}
}

func TestBytesRoundTripAndTruncation(t *testing.T) {
	var w Writer
	w.AppendBytes([]byte("hello protobuf"))
	r := NewReader(w.Bytes())
	b, err := r.Bytes()
	if err != nil || string(b) != "hello protobuf" {
		t.Fatalf("bytes: %q %v", b, err)
	}

	// Declared length far beyond the buffer: must error, never panic.
	var w2 Writer
	w2.AppendVarint(1 << 40)
	w2.AppendBytes([]byte("x")) // only 1 payload byte present
	if _, err := NewReader(w2.Bytes()).Bytes(); !IsTruncated(err) {
		t.Fatalf("oversized length prefix: want TruncatedError, got %v", err)
	}

	// Zero-length payload.
	var w3 Writer
	w3.AppendBytes(nil)
	b, err = NewReader(w3.Bytes()).Bytes()
	if err != nil || len(b) != 0 {
		t.Fatalf("empty bytes: %q %v", b, err)
	}
}

func TestTag(t *testing.T) {
	var w Writer
	w.AppendTag(15, wireFixed32ForTest)
	num, typ, err := NewReader(w.Bytes()).Tag()
	if err != nil || num != 15 || typ != Fixed32 {
		t.Fatalf("tag: num=%d typ=%v err=%v", num, typ, err)
	}
}

const wireFixed32ForTest = Fixed32

func TestTagRejected(t *testing.T) {
	mk := func(key uint64) []byte {
		var w Writer
		w.AppendVarint(key)
		return w.Bytes()
	}
	// Field number 0.
	if _, _, err := NewReader(mk(0)).Tag(); err == nil {
		t.Fatal("field number 0 accepted")
	}
	// Deprecated groups.
	if _, _, err := NewReader(mk(1<<3 | uint64(StartGroup))).Tag(); err == nil {
		t.Fatal("start-group accepted")
	}
	if _, _, err := NewReader(mk(1<<3 | uint64(EndGroup))).Tag(); err == nil {
		t.Fatal("end-group accepted")
	}
	// Unknown wire types 6 and 7.
	if _, _, err := NewReader(mk(1<<3 | 6)).Tag(); err == nil {
		t.Fatal("wire type 6 accepted")
	}
	if _, _, err := NewReader(mk(1<<3 | 7)).Tag(); err == nil {
		t.Fatal("wire type 7 accepted")
	}
	// Truncated tag (continuation bit, no payload).
	if _, _, err := NewReader([]byte{0x80}).Tag(); !IsTruncated(err) {
		t.Fatalf("truncated tag: want TruncatedError, got %v", err)
	}
}

func TestTruncatedErrorFields(t *testing.T) {
	buf := []byte{0x08, 0x01, 0x11} // tag(1,varint)=1, then fixed64 tag with no payload
	r := NewReader(buf)
	if _, _, err := r.Tag(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Varint(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Tag(); err != nil {
		t.Fatal(err)
	}
	_, err := r.Fixed64()
	var te *TruncatedError
	if !errors.As(err, &te) {
		t.Fatalf("want TruncatedError, got %v", err)
	}
	if te.Offset != 3 || te.Need != 8 || te.Have != 0 {
		t.Fatalf("bad truncation metadata: %+v", te)
	}
}
