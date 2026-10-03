package wire

import (
	"math"
	"testing"
)

func TestVarintBoundaries(t *testing.T) {
	cases := []struct {
		v   uint64
		enc []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},               // max 1-byte
		{128, []byte{0x80, 0x01}},         // min 2-byte
		{300, []byte{0xac, 0x02}},         // canonical protobuf example
		{16383, []byte{0xff, 0x7f}},       // max 2-byte
		{16384, []byte{0x80, 0x80, 0x01}}, // min 3-byte
		{math.MaxUint64, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}}, // max 10-byte
	}
	for _, c := range cases {
		w := NewWriter()
		w.AppendVarint(c.v)
		if got := w.Bytes(); !eq(got, c.enc) {
			t.Fatalf("encode %d: got %v want %v", c.v, got, c.enc)
		}
		r := NewReader(c.enc)
		got, err := r.ReadVarint()
		if err != nil {
			t.Fatalf("decode %d: %v", c.v, err)
		}
		if got != c.v {
			t.Fatalf("decode: got %d want %d", got, c.v)
		}
		if !r.Done() {
			t.Fatalf("decode %d: %d trailing bytes", c.v, r.Remaining())
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	// Continuation bit set but buffer ends.
	r := NewReader([]byte{0x80})
	if _, err := r.ReadVarint(); err != ErrTruncated {
		t.Fatalf("got %v want ErrTruncated", err)
	}
	// 9 continuation bytes then nothing.
	r = NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	if _, err := r.ReadVarint(); err != ErrTruncated {
		t.Fatalf("got %v want ErrTruncated", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10th byte carries more than 1 payload bit.
	r := NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02})
	if _, err := r.ReadVarint(); err != ErrOverflow {
		t.Fatalf("got %v want ErrOverflow", err)
	}
	// 11 bytes of continuation.
	r = NewReader([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01})
	if _, err := r.ReadVarint(); err != ErrOverflow {
		t.Fatalf("got %v want ErrOverflow", err)
	}
}

func TestZigZagRoundTrip(t *testing.T) {
	// Canonical zigzag table from the protobuf spec.
	pairs := []struct {
		s int64
		u uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, p := range pairs {
		if got := ZigZagEncode64(p.s); got != p.u {
			t.Fatalf("encode64(%d) = %d want %d", p.s, got, p.u)
		}
		if got := ZigZagDecode64(p.u); got != p.s {
			t.Fatalf("decode64(%d) = %d want %d", p.u, got, p.s)
		}
	}
	pairs32 := []struct {
		s int32
		u uint32
	}{
		{0, 0}, {-1, 1}, {1, 2},
		{math.MaxInt32, math.MaxUint32 - 1},
		{math.MinInt32, math.MaxUint32},
	}
	for _, p := range pairs32 {
		if got := ZigZagEncode32(p.s); got != p.u {
			t.Fatalf("encode32(%d) = %d want %d", p.s, got, p.u)
		}
		if got := ZigZagDecode32(p.u); got != p.s {
			t.Fatalf("decode32(%d) = %d want %d", p.u, got, p.s)
		}
	}
	// Fuzz-ish round trip over a dense range.
	for i := int64(-100000); i <= 100000; i++ {
		if ZigZagDecode64(ZigZagEncode64(i)) != i {
			t.Fatalf("round trip failed for %d", i)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	w := NewWriter()
	w.AppendFixed32(0xdeadbeef)
	w.AppendFixed64(0x0123456789abcdef)
	r := NewReader(w.Bytes())
	if v, err := r.ReadFixed32(); err != nil || v != 0xdeadbeef {
		t.Fatalf("fixed32: v=%x err=%v", v, err)
	}
	if v, err := r.ReadFixed64(); err != nil || v != 0x0123456789abcdef {
		t.Fatalf("fixed64: v=%x err=%v", v, err)
	}
}

func TestFixedTruncated(t *testing.T) {
	r := NewReader([]byte{0x01, 0x02, 0x03})
	if _, err := r.ReadFixed32(); err != ErrTruncated {
		t.Fatalf("fixed32: got %v want ErrTruncated", err)
	}
	r = NewReader([]byte{0x01, 0x02, 0x03, 0x04, 0x05})
	if _, err := r.ReadFixed64(); err != ErrTruncated {
		t.Fatalf("fixed64: got %v want ErrTruncated", err)
	}
}

func TestLengthDelimitedTruncationSafety(t *testing.T) {
	// Declared length 1000, only 2 bytes present: must error, never slice OOB.
	w := NewWriter()
	w.AppendVarint(1000)
	w.AppendBytes([]byte{0xaa, 0xbb})
	r := NewReader(w.Bytes())
	_, err := r.ReadLengthDelimited()
	if err == nil {
		t.Fatal("expected truncation error")
	}
	if !IsTruncated(err) {
		t.Fatalf("IsTruncated(%v) = false", err)
	}
	te, ok := err.(*TruncatedError)
	if !ok {
		t.Fatalf("error type %T, want *TruncatedError", err)
	}
	if te.Need != 1000 || te.Have != 2 {
		t.Fatalf("TruncatedError = %+v", te)
	}
	if IsTruncated(nil) {
		t.Fatal("IsTruncated(nil) = true")
	}
}

func TestReadBytesNegative(t *testing.T) {
	r := NewReader([]byte{1, 2, 3})
	if _, err := r.ReadBytes(-1); err != ErrNegativeLength {
		t.Fatalf("got %v want ErrNegativeLength", err)
	}
}

func eq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
