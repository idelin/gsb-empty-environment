package wire

import (
	"errors"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 1<<21 - 1, 1 << 21, 1<<32 - 1, 1 << 32, 1<<63 - 1, 1 << 63, math.MaxUint64}
	for _, want := range cases {
		buf := AppendVarint(nil, want)
		r := AcquireReader(buf)
		got, err := r.ReadVarint()
		r.Release()
		if err != nil {
			t.Fatalf("varint(%d): %v", want, err)
		}
		if got != want {
			t.Fatalf("varint roundtrip: got %d want %d", got, want)
		}
	}
}

func TestVarintCanonicalSizes(t *testing.T) {
	sizes := []struct {
		v    uint64
		want int
	}{
		{0, 1}, {127, 1}, {128, 2}, {1<<14 - 1, 2}, {1 << 14, 3},
		{math.MaxUint64, 10},
	}
	for _, c := range sizes {
		if got := len(AppendVarint(nil, c.v)); got != c.want {
			t.Fatalf("varint(%d) size = %d, want %d", c.v, got, c.want)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: unambiguously too long.
	buf := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}
	r := AcquireReader(buf)
	_, err := r.ReadVarint()
	r.Release()
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("11-byte varint: got %v, want ErrOverflow", err)
	}

	// 10 bytes but the 10th carries more than 1 payload bit.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	r = AcquireReader(buf)
	_, err = r.ReadVarint()
	r.Release()
	if !errors.Is(err, ErrOverflow) {
		t.Fatalf("10th byte >1: got %v, want ErrOverflow", err)
	}
}

func TestVarintTruncated(t *testing.T) {
	full := AppendVarint(nil, math.MaxUint64) // 10 bytes
	for i := 0; i < len(full); i++ {
		r := AcquireReader(full[:i])
		_, err := r.ReadVarint()
		r.Release()
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("prefix %d: got %v, want ErrTruncated", i, err)
		}
	}
}

func TestZigZag(t *testing.T) {
	cases := []struct {
		v   int64
		enc uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases {
		if got := EncodeZigZag64(c.v); got != c.enc {
			t.Fatalf("zigzag64(%d) = %d, want %d", c.v, got, c.enc)
		}
		if got := DecodeZigZag64(c.enc); got != c.v {
			t.Fatalf("unzigzag64(%d) = %d, want %d", c.enc, got, c.v)
		}
	}
	c32 := []struct {
		v   int32
		enc uint32
	}{
		{0, 0}, {-1, 1}, {1, 2}, {math.MaxInt32, math.MaxUint32 - 1}, {math.MinInt32, math.MaxUint32},
	}
	for _, c := range c32 {
		if got := EncodeZigZag32(c.v); got != c.enc {
			t.Fatalf("zigzag32(%d) = %d, want %d", c.v, got, c.enc)
		}
		if got := DecodeZigZag32(c.enc); got != c.v {
			t.Fatalf("unzigzag32(%d) = %d, want %d", c.enc, got, c.v)
		}
	}
}

func TestFixedRoundTripAndTruncation(t *testing.T) {
	buf := AppendFixed64(AppendFixed32(nil, 0xdeadbeef), 0x0102030405060708)
	r := AcquireReader(buf)
	v32, err := r.ReadFixed32()
	if err != nil || v32 != 0xdeadbeef {
		t.Fatalf("fixed32: %x %v", v32, err)
	}
	v64, err := r.ReadFixed64()
	if err != nil || v64 != 0x0102030405060708 {
		t.Fatalf("fixed64: %x %v", v64, err)
	}
	r.Release()

	for i := 0; i < 4; i++ {
		r := AcquireReader(buf[:i])
		if _, err := r.ReadFixed32(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed32 prefix %d: want ErrTruncated", i)
		}
		r.Release()
	}
	for i := 4; i < 12; i++ {
		r := AcquireReader(buf[:i])
		_, _ = r.ReadFixed32()
		if _, err := r.ReadFixed64(); !errors.Is(err, ErrTruncated) {
			t.Fatalf("fixed64 prefix %d: want ErrTruncated", i)
		}
		r.Release()
	}
}

func TestReadTagValidation(t *testing.T) {
	// Field number 0 is illegal.
	r := AcquireReader([]byte{0x00})
	if _, _, err := r.ReadTag(); !errors.Is(err, ErrInvalidTag) {
		t.Fatalf("tag 0: want ErrInvalidTag, got %v", err)
	}
	r.Release()

	// Wire types 3, 4 (groups) and 6, 7 (reserved) are rejected.
	for _, wt := range []int{3, 4, 6, 7} {
		r := AcquireReader(AppendVarint(nil, uint64(1<<3|wt)))
		if _, _, err := r.ReadTag(); !errors.Is(err, ErrInvalidWireType) {
			t.Fatalf("wire type %d: want ErrInvalidWireType, got %v", wt, err)
		}
		r.Release()
	}

	// Valid: field 1, varint.
	r = AcquireReader(AppendVarint(nil, 1<<3|WireVarint))
	num, wt, err := r.ReadTag()
	r.Release()
	if err != nil || num != 1 || wt != WireVarint {
		t.Fatalf("valid tag: num=%d wt=%d err=%v", num, wt, err)
	}
}

func TestLengthDelimitedBounds(t *testing.T) {
	// Declared length (100) far exceeds remaining bytes (2): must fail
	// safely without allocating or panicking.
	buf := append(AppendVarint(nil, 100), 0xAA, 0xBB)
	r := AcquireReader(buf)
	if _, err := r.ReadLengthDelimited(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("oversized length: want ErrTruncated, got %v", err)
	}
	r.Release()

	// Huge length prefix (max uint64) must also fail safely.
	buf = append(AppendVarint(nil, math.MaxUint64), 0xAA)
	r = AcquireReader(buf)
	if _, err := r.ReadLengthDelimited(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("huge length: want ErrTruncated, got %v", err)
	}
	r.Release()

	// Negative read is rejected.
	r = AcquireReader([]byte{1, 2, 3})
	if _, err := r.ReadBytes(-1); !errors.Is(err, ErrTruncated) {
		t.Fatalf("negative read: want ErrTruncated, got %v", err)
	}
	r.Release()
}

func TestSubReaderNested(t *testing.T) {
	// Outer: field 1 varint 42; field 2 embedded {field 1 varint 7}; field 3 fixed32.
	var buf []byte
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 42)
	var inner []byte
	inner = AppendTag(inner, 1, WireVarint)
	inner = AppendVarint(inner, 7)
	buf = AppendTag(buf, 2, WireBytes)
	buf = AppendVarint(buf, uint64(len(inner)))
	buf = append(buf, inner...)
	buf = AppendTag(buf, 3, WireFixed32)
	buf = AppendFixed32(buf, 99)

	r := AcquireReader(buf)
	num, wt, _ := r.ReadTag()
	if num != 1 || wt != WireVarint {
		t.Fatal("field 1 tag")
	}
	if v, _ := r.ReadVarint(); v != 42 {
		t.Fatal("field 1 value")
	}
	num, wt, _ = r.ReadTag()
	if num != 2 || wt != WireBytes {
		t.Fatal("field 2 tag")
	}
	sub, err := r.Sub()
	if err != nil {
		t.Fatal(err)
	}
	sn, swt, _ := sub.ReadTag()
	sv, _ := sub.ReadVarint()
	if sn != 1 || swt != WireVarint || sv != 7 || !sub.Done() {
		t.Fatalf("nested: num=%d wt=%d v=%d done=%v", sn, swt, sv, sub.Done())
	}
	sub.Release()
	num, wt, _ = r.ReadTag()
	if num != 3 || wt != WireFixed32 {
		t.Fatal("field 3 tag")
	}
	if v, _ := r.ReadFixed32(); v != 99 {
		t.Fatal("field 3 value")
	}
	if !r.Done() {
		t.Fatal("stream not exhausted")
	}
	r.Release()
}

func TestSkip(t *testing.T) {
	var buf []byte
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 12345)
	buf = AppendTag(buf, 2, WireFixed64)
	buf = AppendFixed64(buf, math.MaxUint64)
	buf = AppendTag(buf, 3, WireBytes)
	buf = AppendVarint(buf, 3)
	buf = append(buf, "abc"...)
	buf = AppendTag(buf, 4, WireFixed32)
	buf = AppendFixed32(buf, 7)
	buf = AppendTag(buf, 5, WireVarint)
	buf = AppendVarint(buf, 1)

	r := AcquireReader(buf)
	for i := 0; i < 4; i++ {
		_, wt, err := r.ReadTag()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Skip(wt); err != nil {
			t.Fatalf("skip field %d: %v", i+1, err)
		}
	}
	num, _, err := r.ReadTag()
	if err != nil || num != 5 {
		t.Fatalf("after skips: num=%d err=%v", num, err)
	}
	r.Release()

	// Skipping a truncated field fails safely.
	r = AcquireReader([]byte{0x08, 0x80}) // tag then truncated varint
	_, wt, _ := r.ReadTag()
	if err := r.Skip(wt); !errors.Is(err, ErrTruncated) {
		t.Fatalf("skip truncated: want ErrTruncated, got %v", err)
	}
	r.Release()
}

func TestWriterReaderInterOp(t *testing.T) {
	w := AcquireWriter()
	w.WriteTag(1, WireVarint)
	w.WriteVarint(300)
	w.WriteTag(2, WireVarint)
	w.WriteZigZag64(-123456789)
	w.WriteTag(3, WireFixed64)
	w.WriteFixed64(0xCAFEBABEDEADBEEF)
	w.WriteTag(4, WireBytes)
	w.WriteLengthDelimited([]byte("hello"))
	data := append([]byte(nil), w.Bytes()...)
	w.Release()

	r := AcquireReader(data)
	if _, _, err := r.ReadTag(); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.ReadVarint(); v != 300 {
		t.Fatalf("varint: %d", v)
	}
	if _, _, err := r.ReadTag(); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.ReadZigZag64(); v != -123456789 {
		t.Fatalf("zigzag: %d", v)
	}
	if _, _, err := r.ReadTag(); err != nil {
		t.Fatal(err)
	}
	if v, _ := r.ReadFixed64(); v != 0xCAFEBABEDEADBEEF {
		t.Fatalf("fixed64: %x", v)
	}
	if _, _, err := r.ReadTag(); err != nil {
		t.Fatal(err)
	}
	if b, _ := r.ReadLengthDelimited(); string(b) != "hello" {
		t.Fatalf("bytes: %q", b)
	}
	if !r.Done() {
		t.Fatal("trailing bytes")
	}
	r.Release()
}
