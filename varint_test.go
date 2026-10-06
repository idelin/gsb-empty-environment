package varint

import (
	"bytes"
	"math/big"
	"testing"
)

const (
	maxInt64  = int64(1<<63 - 1)
	minInt64  = -maxInt64 - 1
	maxUint64 = ^uint64(0)
)

// bigValueOf independently computes the integer value of an encoding
// with arbitrary-precision arithmetic: v = sum payload_i * 2^(7i).
func bigValueOf(enc []byte) *big.Int {
	v := new(big.Int)
	for i := len(enc) - 1; i >= 0; i-- {
		v.Lsh(v, 7)
		v.Or(v, big.NewInt(int64(enc[i]&0x7f)))
	}
	return v
}

// bigEncodingOf independently derives the minimal encoding of v by
// repeatedly peeling off 7-bit groups with big-integer arithmetic.
func bigEncodingOf(v *big.Int) []byte {
	x := new(big.Int).Set(v)
	mask := big.NewInt(0x7f)
	var enc []byte
	for x.Cmp(mask) > 0 {
		enc = append(enc, byte(new(big.Int).And(x, mask).Int64())|0x80)
		x.Rsh(x, 7)
	}
	return append(enc, byte(x.Int64()))
}

// bigZigZag maps x to its ZigZag image with big-integer arithmetic:
// 2x when x >= 0, -2x-1 otherwise.
func bigZigZag(x int64) *big.Int {
	bx := big.NewInt(x)
	if x >= 0 {
		return bx.Lsh(bx, 1)
	}
	r := bx.Neg(bx)
	r.Lsh(r, 1)
	return r.Sub(r, big.NewInt(1))
}

func mustError(t *testing.T, err error, kind Kind, off int64, consumed int) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T (%v), want *Error", err, err)
	}
	if e.Kind != kind {
		t.Fatalf("error kind = %v, want %v", e.Kind, kind)
	}
	if e.Offset != off {
		t.Fatalf("error offset = %d, want %d", e.Offset, off)
	}
	if e.Consumed != consumed {
		t.Fatalf("error consumed = %d, want %d", e.Consumed, consumed)
	}
	if e.Boundary() != off+int64(consumed) {
		t.Fatalf("error boundary = %d, want %d", e.Boundary(), off+int64(consumed))
	}
	return e
}

// TestExhaustiveShortEncodings enumerates every byte string of length
// 1, 2 and 3 with the continuation bits of a single value, and checks
// each one against big-integer arithmetic: canonical forms must decode
// to the big-int value and re-encode to the identical bytes, while
// non-minimal forms must be rejected with the exact consumed range.
func TestExhaustiveShortEncodings(t *testing.T) {
	for length := 1; length <= 3; length++ {
		total := 1 << uint(7*length)
		for n := 0; n < total; n++ {
			enc := make([]byte, length)
			m := n
			for i := 0; i < length; i++ {
				enc[i] = byte(m & 0x7f)
				m >>= 7
			}
			for i := 0; i < length-1; i++ {
				enc[i] |= 0x80
			}
			v, next, err := Decode(enc)
			if length > 1 && enc[length-1] == 0 {
				mustError(t, err, ErrNonCanonical, 0, length)
				if next != length {
					t.Fatalf("enc %x: next = %d, want %d", enc, next, length)
				}
				continue
			}
			if err != nil {
				t.Fatalf("enc %x: unexpected error: %v", enc, err)
			}
			if next != length {
				t.Fatalf("enc %x: next = %d, want %d", enc, next, length)
			}
			if want := bigValueOf(enc); want.Cmp(new(big.Int).SetUint64(v)) != 0 {
				t.Fatalf("enc %x: decoded %d, big-int value %s", enc, v, want)
			}
			if got := AppendUint64(nil, v); !bytes.Equal(got, enc) {
				t.Fatalf("enc %x: re-encode of %d = %x", enc, v, got)
			}
			if EncodedLen(v) != length {
				t.Fatalf("enc %x: EncodedLen(%d) = %d, want %d", enc, v, EncodedLen(v), length)
			}
		}
	}
}

// TestBlockBoundaryValues checks the extremes straddling every 7-bit
// block edge (2^(7k)-1, 2^(7k), 2^(7k)+1) plus the 64-bit limits,
// deriving the expected encoding independently with big integers.
func TestBlockBoundaryValues(t *testing.T) {
	one := big.NewInt(1)
	var values []*big.Int
	for k := 1; k <= 9; k++ {
		edge := new(big.Int).Lsh(one, uint(7*k))
		for _, d := range []int64{-1, 0, 1} {
			values = append(values, new(big.Int).Add(edge, big.NewInt(d)))
		}
	}
	values = append(values,
		new(big.Int).Lsh(one, 63),
		new(big.Int).Sub(new(big.Int).Lsh(one, 64), one),
		big.NewInt(0),
	)
	for _, vb := range values {
		v := vb.Uint64()
		wantEnc := bigEncodingOf(vb)
		gotEnc := AppendUint64(nil, v)
		if !bytes.Equal(gotEnc, wantEnc) {
			t.Fatalf("value %s: encoding %x, big-int encoding %x", vb, gotEnc, wantEnc)
		}
		decoded, next, err := Decode(gotEnc)
		if err != nil {
			t.Fatalf("value %s: decode error: %v", vb, err)
		}
		if decoded != v || next != len(gotEnc) {
			t.Fatalf("value %s: decoded (%d, %d), want (%d, %d)", vb, decoded, next, v, len(gotEnc))
		}
	}
}

// TestExtremes pins the exact byte form of the 64-bit limits and the
// ZigZag images of the signed limits, cross-checked with big integers.
func TestExtremes(t *testing.T) {
	uintCases := []struct {
		v   uint64
		enc []byte
	}{
		{0, []byte{0x00}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{maxUint64, append(bytes.Repeat([]byte{0xff}, 9), 0x01)},
		{1 << 63, append(bytes.Repeat([]byte{0x80}, 9), 0x01)},
		{1<<63 - 1, append(bytes.Repeat([]byte{0xff}, 8), 0x7f)},
	}
	for _, c := range uintCases {
		if got := AppendUint64(nil, c.v); !bytes.Equal(got, c.enc) {
			t.Fatalf("AppendUint64(%d) = %x, want %x", c.v, got, c.enc)
		}
		v, next, err := Decode(c.enc)
		if err != nil || v != c.v || next != len(c.enc) {
			t.Fatalf("Decode(%x) = (%d, %d, %v), want (%d, %d, nil)", c.enc, v, next, err, c.v, len(c.enc))
		}
	}
	intCases := []struct {
		x int64
		u uint64
	}{
		{0, 0},
		{-1, 1},
		{1, 2},
		{minInt64, maxUint64},
		{maxInt64, maxUint64 - 1},
	}
	for _, c := range intCases {
		if u := ZigZagEncode(c.x); u != c.u {
			t.Fatalf("ZigZagEncode(%d) = %d, want %d", c.x, u, c.u)
		}
		if want := bigZigZag(c.x); want.Cmp(new(big.Int).SetUint64(c.u)) != 0 {
			t.Fatalf("big-int ZigZag(%d) = %s, want %d", c.x, want, c.u)
		}
		if x := ZigZagDecode(c.u); x != c.x {
			t.Fatalf("ZigZagDecode(%d) = %d, want %d", c.u, x, c.x)
		}
		enc := AppendInt64(nil, c.x)
		x, _, err := DecodeInt64(enc)
		if err != nil || x != c.x {
			t.Fatalf("DecodeInt64(%x) = (%d, %v), want (%d, nil)", enc, x, err, c.x)
		}
	}
}

// TestRejectionsResume checks that every malformed value is rejected
// with the exact consumed range, and that the following value decodes
// correctly when resumed at the explicit frame boundary.
func TestRejectionsResume(t *testing.T) {
	const follower = uint64(300)
	good := AppendUint64(nil, follower)
	t.Run("empty input", func(t *testing.T) {
		_, next, err := Decode(nil)
		mustError(t, err, ErrTruncated, 0, 0)
		if next != 0 {
			t.Fatalf("empty input: next = %d, want 0", next)
		}
	})
	cases := []struct {
		name     string
		bad      []byte
		kind     Kind
		consumed int
	}{
		{"non-minimal zero", []byte{0x80, 0x00}, ErrNonCanonical, 2},
		{"non-minimal three bytes", []byte{0xff, 0xff, 0x00}, ErrNonCanonical, 3},
		{"non-minimal tenth byte", append(bytes.Repeat([]byte{0x80}, 9), 0x00), ErrNonCanonical, 10},
		{"overlong zeros", bytes.Repeat([]byte{0x80}, 10), ErrOverlong, 10},
		{"overlong ones", bytes.Repeat([]byte{0xff}, 10), ErrOverlong, 10},
		{"overflow by one bit", append(bytes.Repeat([]byte{0xff}, 9), 0x02), ErrOverflow, 10},
		{"overflow full payload", append(bytes.Repeat([]byte{0xff}, 9), 0x7f), ErrOverflow, 10},
	}
	for _, c := range cases {
		buf := append(append([]byte{}, c.bad...), good...)
		_, next, err := Decode(buf)
		mustError(t, err, c.kind, 0, c.consumed)
		if next != c.consumed {
			t.Fatalf("%s: next = %d, want %d", c.name, next, c.consumed)
		}
		v, next2, err := Decode(buf[next:])
		if err != nil || v != follower || next2 != len(good) {
			t.Fatalf("%s: resume at boundary: (%d, %d, %v), want (%d, %d, nil)",
				c.name, v, next2, err, follower, len(good))
		}
	}
}

// TestTruncationEveryPrefix feeds every proper prefix of a long
// encoding and expects ErrTruncated covering exactly the prefix.
func TestTruncationEveryPrefix(t *testing.T) {
	enc := AppendUint64(nil, 0xf1f2f3f4f5f6f7f8)
	if len(enc) != MaxLen {
		t.Fatalf("encoding length = %d, want %d", len(enc), MaxLen)
	}
	for i := 0; i < len(enc); i++ {
		_, next, err := Decode(enc[:i])
		mustError(t, err, ErrTruncated, 0, i)
		if next != i {
			t.Fatalf("prefix %d: next = %d, want %d", i, next, i)
		}
	}
}

// TestDecodeAtOffsets decodes a concatenated stream of values purely
// through explicit frame offsets.
func TestDecodeAtOffsets(t *testing.T) {
	values := []uint64{0, 1, 127, 128, 1 << 21, 1<<63 - 1, 1 << 63, maxUint64}
	var buf []byte
	for _, v := range values {
		buf = AppendUint64(buf, v)
	}
	off := 0
	for i, want := range values {
		got, next, err := DecodeAt(buf, off)
		if err != nil || got != want {
			t.Fatalf("value %d: DecodeAt = (%d, %v), want (%d, nil)", i, got, err, want)
		}
		if next != off+EncodedLen(want) {
			t.Fatalf("value %d: next = %d, want %d", i, next, off+EncodedLen(want))
		}
		off = next
	}
	if off != len(buf) {
		t.Fatalf("final offset = %d, want %d", off, len(buf))
	}
}
