package wire

import (
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{
		0, 1, 2, 127, 128, 129, 300, 16383, 16384,
		1<<21 - 1, 1 << 21, 1<<28 - 1, 1 << 28,
		1<<35 - 1, 1 << 35, 1<<63 - 1, 1 << 63,
		math.MaxUint64 - 1, math.MaxUint64,
	}
	for _, want := range cases {
		enc := NewEncoder()
		enc.AppendVarint(want)
		r := NewReader(enc.Bytes())
		got, err := r.ReadVarint()
		if err != nil {
			t.Fatalf("ReadVarint(%d) error: %v", want, err)
		}
		if got != want {
			t.Fatalf("ReadVarint = %d, want %d", got, want)
		}
		if !r.Done() {
			t.Fatalf("varint %d left %d unread bytes", want, r.Remaining())
		}
	}
}

func TestVarintKnownEncodings(t *testing.T) {
	// Canonical protobuf examples.
	cases := []struct {
		in   []byte
		want uint64
	}{
		{[]byte{0x00}, 0},
		{[]byte{0x01}, 1},
		{[]byte{0x7f}, 127},
		{[]byte{0x80, 0x01}, 128},
		{[]byte{0xac, 0x02}, 300},
		{[]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}, math.MaxUint64},
	}
	for _, c := range cases {
		got, err := NewReader(c.in).ReadVarint()
		if err != nil {
			t.Fatalf("ReadVarint(%x) error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ReadVarint(%x) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	// Continuation bit set but buffer ends: must report truncation, not panic.
	inputs := [][]byte{
		{},
		{0x80},
		{0xff, 0xff, 0xff},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80},
	}
	for _, in := range inputs {
		_, err := NewReader(in).ReadVarint()
		if err == nil {
			t.Fatalf("ReadVarint(%x) succeeded, want truncation error", in)
		}
		if !IsTruncated(err) {
			t.Fatalf("ReadVarint(%x) error %v is not a BoundsError", in, err)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: exceeds the 10-byte / 64-bit limit.
	enc := NewEncoder()
	enc.AppendRaw([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01})
	if _, err := NewReader(enc.Bytes()).ReadVarint(); err != ErrOverflow {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
	// 10th byte > 1 also overflows 64 bits.
	enc2 := NewEncoder()
	enc2.AppendRaw([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02})
	if _, err := NewReader(enc2.Bytes()).ReadVarint(); err != ErrOverflow {
		t.Fatalf("got %v, want ErrOverflow", err)
	}
}

func TestZigZag(t *testing.T) {
	cases64 := []struct {
		v int64
		u uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt32, 2*math.MaxInt32 - 1 + 1},
		{math.MinInt32, 2*math.MaxInt32 + 1},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, c := range cases64 {
		if got := ZigZagEncode64(c.v); got != c.u {
			t.Fatalf("ZigZagEncode64(%d) = %d, want %d", c.v, got, c.u)
		}
		if got := ZigZagDecode64(c.u); got != c.v {
			t.Fatalf("ZigZagDecode64(%d) = %d, want %d", c.u, got, c.v)
		}
	}
	cases32 := []struct {
		v int32
		u uint32
	}{
		{0, 0}, {-1, 1}, {1, 2}, {math.MaxInt32, math.MaxUint32 - 1}, {math.MinInt32, math.MaxUint32},
	}
	for _, c := range cases32 {
		if got := ZigZagEncode32(c.v); got != c.u {
			t.Fatalf("ZigZagEncode32(%d) = %d, want %d", c.v, got, c.u)
		}
		if got := ZigZagDecode32(c.u); got != c.v {
			t.Fatalf("ZigZagDecode32(%d) = %d, want %d", c.u, got, c.v)
		}
	}
}

func TestZigZagReaderRoundTrip(t *testing.T) {
	vals := []int64{0, -1, 1, -123456, 123456, math.MinInt64, math.MaxInt64}
	enc := NewEncoder()
	for _, v := range vals {
		enc.AppendZigZag64(v)
	}
	r := NewReader(enc.Bytes())
	for _, want := range vals {
		got, err := r.ReadZigZag64()
		if err != nil {
			t.Fatalf("ReadZigZag64 error: %v", err)
		}
		if got != want {
			t.Fatalf("ReadZigZag64 = %d, want %d", got, want)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	enc := NewEncoder()
	enc.AppendFixed32(0xdeadbeef)
	enc.AppendFixed64(0x0123456789abcdef)
	r := NewReader(enc.Bytes())
	v32, err := r.ReadFixed32()
	if err != nil || v32 != 0xdeadbeef {
		t.Fatalf("ReadFixed32 = %x, %v", v32, err)
	}
	v64, err := r.ReadFixed64()
	if err != nil || v64 != 0x0123456789abcdef {
		t.Fatalf("ReadFixed64 = %x, %v", v64, err)
	}
	if !r.Done() {
		t.Fatal("reader not exhausted")
	}
}

func TestFixedTruncated(t *testing.T) {
	for n := 0; n < 4; n++ {
		_, err := NewReader(make([]byte, n)).ReadFixed32()
		if !IsTruncated(err) {
			t.Fatalf("ReadFixed32 with %d bytes: got %v, want BoundsError", n, err)
		}
	}
	for n := 0; n < 8; n++ {
		_, err := NewReader(make([]byte, n)).ReadFixed64()
		if !IsTruncated(err) {
			t.Fatalf("ReadFixed64 with %d bytes: got %v, want BoundsError", n, err)
		}
	}
}

func TestReadTag(t *testing.T) {
	enc := NewEncoder()
	enc.AppendTag(1, WireVarint)
	enc.AppendTag(15, WireFixed32)
	enc.AppendTag(2047, WireBytes) // multi-byte tag
	r := NewReader(enc.Bytes())
	want := []struct {
		num int
		wt  WireType
	}{{1, WireVarint}, {15, WireFixed32}, {2047, WireBytes}}
	for _, w := range want {
		num, wt, err := r.ReadTag()
		if err != nil {
			t.Fatalf("ReadTag error: %v", err)
		}
		if num != w.num || wt != w.wt {
			t.Fatalf("ReadTag = (%d,%d), want (%d,%d)", num, wt, w.num, w.wt)
		}
	}
}

func TestReadTagInvalid(t *testing.T) {
	// Reserved wire types 3, 4, 6, 7 must be rejected.
	for _, wt := range []uint64{3, 4, 6, 7} {
		enc := NewEncoder()
		enc.AppendVarint(1<<3 | wt)
		if _, _, err := NewReader(enc.Bytes()).ReadTag(); err != ErrInvalidWireType {
			t.Fatalf("wire type %d: got %v, want ErrInvalidWireType", wt, err)
		}
	}
	// Field number 0 is illegal.
	enc := NewEncoder()
	enc.AppendVarint(0)
	if _, _, err := NewReader(enc.Bytes()).ReadTag(); err != ErrInvalidFieldNumber {
		t.Fatalf("field 0: got %v, want ErrInvalidFieldNumber", err)
	}
}

func TestReadBytesBoundsGuard(t *testing.T) {
	// Length prefix claims 100 bytes but only 2 remain: safe rejection.
	enc := NewEncoder()
	enc.AppendVarint(100)
	enc.AppendRaw([]byte{0x01, 0x02})
	_, err := NewReader(enc.Bytes()).ReadBytes()
	if err == nil {
		t.Fatal("ReadBytes overran buffer without error")
	}
	be, ok := err.(*BoundsError)
	if !ok {
		t.Fatalf("error type %T, want *BoundsError", err)
	}
	if be.Need != 100 || be.Have != 2 {
		t.Fatalf("BoundsError = need %d have %d, want need 100 have 2", be.Need, be.Have)
	}
}

func TestReadBytesEmptyAndExact(t *testing.T) {
	enc := NewEncoder()
	enc.AppendBytes(nil)
	enc.AppendBytes([]byte("abc"))
	r := NewReader(enc.Bytes())
	b, err := r.ReadBytes()
	if err != nil || len(b) != 0 {
		t.Fatalf("empty bytes: %v %v", b, err)
	}
	b, err = r.ReadBytes()
	if err != nil || string(b) != "abc" {
		t.Fatalf("bytes: %q %v", b, err)
	}
	if !r.Done() {
		t.Fatal("reader not exhausted")
	}
}

func TestSkip(t *testing.T) {
	enc := NewEncoder()
	enc.AppendTag(1, WireVarint)
	enc.AppendVarint(150)
	enc.AppendTag(2, WireFixed64)
	enc.AppendFixed64(42)
	enc.AppendTag(3, WireBytes)
	enc.AppendString("skipme")
	enc.AppendTag(4, WireFixed32)
	enc.AppendFixed32(7)
	enc.AppendTag(5, WireVarint)
	enc.AppendVarint(99)

	r := NewReader(enc.Bytes())
	for i := 0; i < 4; i++ {
		_, wt, err := r.ReadTag()
		if err != nil {
			t.Fatalf("ReadTag: %v", err)
		}
		if err := r.Skip(wt); err != nil {
			t.Fatalf("Skip: %v", err)
		}
	}
	num, wt, err := r.ReadTag()
	if err != nil || num != 5 || wt != WireVarint {
		t.Fatalf("after skips: (%d,%d,%v)", num, wt, err)
	}
	v, err := r.ReadVarint()
	if err != nil || v != 99 {
		t.Fatalf("final varint = %d, %v", v, err)
	}
}

func TestNestedMessageDepthLimit(t *testing.T) {
	// Build MaxDepth+2 nested length-delimited shells around an empty core.
	payload := []byte{}
	for i := 0; i < MaxDepth+2; i++ {
		enc := NewEncoder()
		enc.AppendTag(1, WireBytes)
		enc.AppendBytes(payload)
		payload = enc.Bytes()
	}
	r := NewReader(payload)
	depth := 0
	var err error
	for {
		var sub *Reader
		if _, _, terr := r.ReadTag(); terr != nil {
			err = terr
			break
		}
		sub, err = r.ReadMessage()
		if err != nil {
			break
		}
		depth++
		r = sub
		if sub.Done() {
			break
		}
	}
	if err != ErrMaxDepth {
		t.Fatalf("depth walk: got %v after %d levels, want ErrMaxDepth", err, depth)
	}
}

func TestBoundsErrorMessage(t *testing.T) {
	be := &BoundsError{Op: "bytes", Offset: 3, Need: 10, Have: 2}
	want := "wire: truncated bytes at offset 3: need 10 byte(s), have 2"
	if be.Error() != want {
		t.Fatalf("got %q, want %q", be.Error(), want)
	}
}
