package wire

import (
	"errors"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	vals := []uint64{0, 1, 127, 128, 300, 16384, 1<<21 - 1, 1 << 21, 1<<32 - 1, 1 << 32, 1<<63 - 1, math.MaxUint64}
	for _, v := range vals {
		w := NewWriter()
		w.Varint(v)
		if got, err := NewReader(w.Out()).Varint(); err != nil || got != v {
			t.Fatalf("varint round trip %d: got %d, err %v", v, got, err)
		}
	}
	// Canonical sizes per the protobuf spec.
	sizes := map[uint64]int{0: 1, 127: 1, 128: 2, 300: 2, math.MaxUint64: 10}
	for v, want := range sizes {
		w := NewWriter()
		w.Varint(v)
		if len(w.Out()) != want {
			t.Fatalf("varint(%d) size = %d, want %d", v, len(w.Out()), want)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10 bytes with the 10th byte carrying bits beyond 64.
	over := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, err := NewReader(over).Varint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// 11 continuation bytes: still overflow, not a read past the buffer.
	long := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}
	if _, err := NewReader(long).Varint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	// Exactly 10 bytes, 10th byte == 1: the largest legal encoding.
	max := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if v, err := NewReader(max).Varint(); err != nil || v != math.MaxUint64 {
		t.Fatalf("max varint: got %d, err %v", v, err)
	}
}

func TestVarintTruncated(t *testing.T) {
	for _, b := range [][]byte{{}, {0x80}, {0xff, 0xff}} {
		if _, err := NewReader(b).Varint(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("input %x: want ErrTruncated, got %v", b, err)
		}
	}
}

func TestZigzag(t *testing.T) {
	cases := []struct {
		signed   int64
		unsigned uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := EncodeZigzag(c.signed); got != c.unsigned {
			t.Fatalf("EncodeZigzag(%d) = %d, want %d", c.signed, got, c.unsigned)
		}
		if got := DecodeZigzag(c.unsigned); got != c.signed {
			t.Fatalf("DecodeZigzag(%d) = %d, want %d", c.unsigned, got, c.signed)
		}
		w := NewWriter()
		w.Zigzag(c.signed)
		if got, err := NewReader(w.Out()).Zigzag(); err != nil || got != c.signed {
			t.Fatalf("zigzag round trip %d: got %d, err %v", c.signed, got, err)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	w := NewWriter()
	w.Fixed32(0xdeadbeef)
	w.Fixed64(0x0123456789abcdef)
	r := NewReader(w.Out())
	if v, err := r.Fixed32(); err != nil || v != 0xdeadbeef {
		t.Fatalf("fixed32: got %#x, err %v", v, err)
	}
	if v, err := r.Fixed64(); err != nil || v != 0x0123456789abcdef {
		t.Fatalf("fixed64: got %#x, err %v", v, err)
	}
	if !r.Done() {
		t.Fatal("reader not exhausted")
	}
}

func TestFixedTruncated(t *testing.T) {
	if _, err := NewReader([]byte{1, 2, 3}).Fixed32(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("fixed32: want ErrTruncated, got %v", err)
	}
	if _, err := NewReader([]byte{1, 2, 3, 4, 5, 6, 7}).Fixed64(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("fixed64: want ErrTruncated, got %v", err)
	}
}

func TestBytesZeroCopyAndBounds(t *testing.T) {
	w := NewWriter()
	w.Bytes([]byte("hello"))
	r := NewReader(w.Out())
	got, err := r.Bytes()
	if err != nil || string(got) != "hello" {
		t.Fatalf("bytes: got %q, err %v", got, err)
	}
	// Zero-copy: the returned slice aliases the reader's buffer.
	if &got[0] != &w.Out()[1] {
		t.Fatal("Bytes copied the payload")
	}
	// Declared length exceeds remaining input -> truncation, no allocation.
	bomb := []byte{0xff, 0xff, 0xff, 0xff, 0x0f} // length ~4GiB, no payload
	if _, err := NewReader(bomb).Bytes(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("length bomb: want ErrTruncated, got %v", err)
	}
	// Truncated payload.
	short := append([]byte{5}, []byte("ab")...)
	if _, err := NewReader(short).Bytes(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("short payload: want ErrTruncated, got %v", err)
	}
}

func TestTag(t *testing.T) {
	w := NewWriter()
	w.Tag(1, WireVarint)
	w.Tag(MaxFieldNumber, WireFixed64)
	r := NewReader(w.Out())
	if n, wt, err := r.Tag(); err != nil || n != 1 || wt != WireVarint {
		t.Fatalf("tag 1: got %d/%d, err %v", n, wt, err)
	}
	if n, wt, err := r.Tag(); err != nil || n != MaxFieldNumber || wt != WireFixed64 {
		t.Fatalf("tag max: got %d/%d, err %v", n, wt, err)
	}
	// Field number 0 and out-of-range numbers are rejected.
	if _, _, err := NewReader([]byte{0x00}).Tag(); !errors.Is(err, ErrBadFieldNumber) {
		t.Fatalf("field 0: want ErrBadFieldNumber, got %v", err)
	}
	w2 := NewWriter()
	w2.Varint(uint64(MaxFieldNumber+1) << 3)
	if _, _, err := NewReader(w2.Out()).Tag(); !errors.Is(err, ErrBadFieldNumber) {
		t.Fatalf("field overflow: want ErrBadFieldNumber, got %v", err)
	}
}

func TestSkip(t *testing.T) {
	w := NewWriter()
	w.Varint(300)              // varint
	w.Fixed64(1)               // fixed64
	w.Bytes([]byte("skip me")) // bytes
	w.Fixed32(7)               // fixed32
	w.Varint(0xdead)           // sentinel
	r := NewReader(w.Out())
	for _, wt := range []int{WireVarint, WireFixed64, WireBytes, WireFixed32} {
		if err := r.Skip(wt); err != nil {
			t.Fatalf("skip(%d): %v", wt, err)
		}
	}
	if v, err := r.Varint(); err != nil || v != 0xdead {
		t.Fatalf("sentinel: got %#x, err %v", v, err)
	}
	// Reserved wire types (start/end group, 6, 7) are rejected.
	for _, wt := range []int{3, 4, 6, 7} {
		if err := NewReader([]byte{0}).Skip(wt); !errors.Is(err, ErrBadWireType) {
			t.Fatalf("skip(%d): want ErrBadWireType, got %v", wt, err)
		}
	}
	// Skip over truncated data reports truncation.
	if err := NewReader([]byte{0x80}).Skip(WireVarint); !errors.Is(err, ErrTruncated) {
		t.Fatalf("skip truncated varint: want ErrTruncated, got %v", err)
	}
}

func TestMixedStreamRoundTrip(t *testing.T) {
	w := NewWriter()
	w.Tag(1, WireVarint)
	w.Varint(150)
	w.Tag(2, WireFixed64)
	w.Fixed64(math.MaxUint64)
	w.Tag(3, WireBytes)
	w.Bytes([]byte{0, 1, 2, 3})
	w.Tag(4, WireFixed32)
	w.Fixed32(42)
	w.Tag(5, WireVarint)
	w.Zigzag(-12345)

	r := NewReader(w.Out())
	expect := []struct {
		num, wt int
	}{{1, WireVarint}, {2, WireFixed64}, {3, WireBytes}, {4, WireFixed32}, {5, WireVarint}}
	for _, e := range expect {
		n, wt, err := r.Tag()
		if err != nil || n != e.num || wt != e.wt {
			t.Fatalf("tag: got %d/%d err %v, want %d/%d", n, wt, err, e.num, e.wt)
		}
		if err := r.Skip(wt); err != nil {
			t.Fatalf("skip: %v", err)
		}
	}
	if !r.Done() {
		t.Fatal("trailing bytes after stream")
	}
}
