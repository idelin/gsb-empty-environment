package wirebuf

import (
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	values := []uint64{0, 1, 127, 128, 300, 16383, 16384,
		1<<21 - 1, 1 << 21, 1<<32 - 1, 1 << 32,
		1<<63 - 1, 1 << 63, math.MaxUint64}
	for _, v := range values {
		buf := AppendVarint(nil, v)
		r := NewReader(buf)
		got, err := r.ReadVarint()
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("ReadVarint: got %d, want %d", got, v)
		}
		if r.Remaining() != 0 {
			t.Fatalf("ReadVarint(%d): %d bytes left over", v, r.Remaining())
		}
	}
}

func TestVarintCanonicalSizes(t *testing.T) {
	cases := []struct {
		v    uint64
		size int
	}{
		{0, 1}, {127, 1}, {128, 2}, {300, 2},
		{1<<14 - 1, 2}, {1 << 14, 3}, {math.MaxUint64, 10},
	}
	for _, c := range cases {
		if n := len(AppendVarint(nil, c.v)); n != c.size {
			t.Fatalf("varint(%d) encoded to %d bytes, want %d", c.v, n, c.size)
		}
	}
}

func TestVarintTruncated(t *testing.T) {
	// Continuation bit set but buffer ends.
	for _, buf := range [][]byte{
		{0x80},
		{0xFF, 0xFF},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80},
	} {
		r := NewReader(buf)
		if _, err := r.ReadVarint(); err != ErrTruncated {
			t.Fatalf("buf %x: got %v, want ErrTruncated", buf, err)
		}
	}
	// Empty buffer.
	if _, err := NewReader(nil).ReadVarint(); err != ErrTruncated {
		t.Fatalf("empty: got %v, want ErrTruncated", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 10th byte carrying more than 1 payload bit.
	ten := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x02}
	if _, err := NewReader(ten).ReadVarint(); err != ErrOverflow {
		t.Fatalf("10-byte overflow: got %v, want ErrOverflow", err)
	}
	// 11 continuation bytes.
	eleven := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01}
	if _, err := NewReader(eleven).ReadVarint(); err != ErrOverflow {
		t.Fatalf("11-byte: got %v, want ErrOverflow", err)
	}
	// Largest legal 10-byte varint: 10th byte == 0x01 -> MaxUint64.
	max := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}
	v, err := NewReader(max).ReadVarint()
	if err != nil || v != math.MaxUint64 {
		t.Fatalf("max varint: got %d, %v", v, err)
	}
}

func TestZigZagRoundTrip(t *testing.T) {
	values := []int64{0, -1, 1, -2, 2, -3, 3,
		math.MaxInt32, math.MinInt32,
		math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, math.MinInt64 + 1}
	for _, v := range values {
		buf := AppendZigZag64(nil, v)
		got, err := NewReader(buf).ReadZigZag64()
		if err != nil {
			t.Fatalf("zigzag(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("zigzag: got %d, want %d", got, v)
		}
	}
}

func TestZigZagKnownEncoding(t *testing.T) {
	// ZigZag maps 0,-1,1,-2,2 -> 0,1,2,3,4.
	cases := []struct {
		v int64
		u uint64
	}{{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{math.MaxInt64, math.MaxUint64 - 1}, {math.MinInt64, math.MaxUint64}}
	for _, c := range cases {
		buf := AppendZigZag64(nil, c.v)
		u, err := NewReader(buf).ReadVarint()
		if err != nil || u != c.u {
			t.Fatalf("zigzag(%d) encoded as %d, want %d", c.v, u, c.u)
		}
	}
}

func TestZigZag32(t *testing.T) {
	for _, v := range []int32{0, -1, 1, math.MaxInt32, math.MinInt32} {
		buf := AppendZigZag32(nil, v)
		got, err := NewReader(buf).ReadZigZag32()
		if err != nil || got != v {
			t.Fatalf("zigzag32: got %d, %v; want %d", got, err, v)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	buf := AppendFixed32(nil, 0xDEADBEEF)
	buf = AppendFixed64(buf, 0x0123456789ABCDEF)
	r := NewReader(buf)
	v32, err := r.ReadFixed32()
	if err != nil || v32 != 0xDEADBEEF {
		t.Fatalf("fixed32: got %#x, %v", v32, err)
	}
	v64, err := r.ReadFixed64()
	if err != nil || v64 != 0x0123456789ABCDEF {
		t.Fatalf("fixed64: got %#x, %v", v64, err)
	}
}

func TestFixedTruncated(t *testing.T) {
	for n := 0; n < 4; n++ {
		if _, err := NewReader(make([]byte, n)).ReadFixed32(); err != ErrTruncated {
			t.Fatalf("fixed32 with %d bytes: got %v, want ErrTruncated", n, err)
		}
	}
	for n := 0; n < 8; n++ {
		if _, err := NewReader(make([]byte, n)).ReadFixed64(); err != ErrTruncated {
			t.Fatalf("fixed64 with %d bytes: got %v, want ErrTruncated", n, err)
		}
	}
}

func TestReadBytesBounds(t *testing.T) {
	buf := AppendBytes(nil, []byte("hello"))
	r := NewReader(buf)
	b, err := r.ReadBytes()
	if err != nil || string(b) != "hello" {
		t.Fatalf("got %q, %v", b, err)
	}
	// Declared length exceeds remaining buffer -> safe truncation error.
	evil := AppendVarint(nil, 1000)
	evil = append(evil, 1, 2, 3)
	if _, err := NewReader(evil).ReadBytes(); err != ErrTruncated {
		t.Fatalf("oversized length: got %v, want ErrTruncated", err)
	}
	// Length prefix itself truncated.
	if _, err := NewReader([]byte{0x80}).ReadBytes(); err != ErrTruncated {
		t.Fatalf("truncated length: got %v, want ErrTruncated", err)
	}
}

func TestReadTagValidation(t *testing.T) {
	// Reserved wire types 3, 4, 6, 7 are rejected.
	for _, wt := range []WireType{3, 4, 6, 7} {
		buf := AppendVarint(nil, uint64(1)<<3|uint64(wt))
		if _, _, err := NewReader(buf).ReadTag(); err != ErrInvalidWireType {
			t.Fatalf("wire type %d: got %v, want ErrInvalidWireType", wt, err)
		}
	}
	// Field number 0 is illegal.
	buf := AppendVarint(nil, 0)
	if _, _, err := NewReader(buf).ReadTag(); err != ErrInvalidFieldNumber {
		t.Fatalf("field 0: got %v, want ErrInvalidFieldNumber", err)
	}
	// Field number above the spec maximum is illegal.
	buf = AppendVarint(nil, uint64(MaxFieldNumber+1)<<3)
	if _, _, err := NewReader(buf).ReadTag(); err != ErrInvalidFieldNumber {
		t.Fatalf("field too large: got %v, want ErrInvalidFieldNumber", err)
	}
	// Boundary: max legal field number is accepted.
	buf = AppendVarint(nil, uint64(MaxFieldNumber)<<3|uint64(WireVarint))
	num, wt, err := NewReader(buf).ReadTag()
	if err != nil || num != MaxFieldNumber || wt != WireVarint {
		t.Fatalf("max field: got %d/%d, %v", num, wt, err)
	}
}

func TestSkipField(t *testing.T) {
	var buf []byte
	buf = AppendTag(buf, 1, WireVarint)
	buf = AppendVarint(buf, 42)
	buf = AppendTag(buf, 2, WireFixed64)
	buf = AppendFixed64(buf, 0xFF)
	buf = AppendTag(buf, 3, WireBytes)
	buf = AppendBytes(buf, []byte("skip me"))
	buf = AppendTag(buf, 4, WireFixed32)
	buf = AppendFixed32(buf, 7)
	buf = AppendTag(buf, 5, WireVarint)
	buf = AppendVarint(buf, 99)

	r := NewReader(buf)
	for i := 0; i < 4; i++ {
		_, wt, err := r.ReadTag()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.SkipField(wt); err != nil {
			t.Fatal(err)
		}
	}
	num, wt, err := r.ReadTag()
	if err != nil || num != 5 || wt != WireVarint {
		t.Fatalf("after skips: got %d/%d, %v", num, wt, err)
	}
	v, _ := r.ReadVarint()
	if v != 99 {
		t.Fatalf("final value: got %d, want 99", v)
	}
	if err := r.SkipField(WireType(6)); err != ErrInvalidWireType {
		t.Fatalf("skip invalid: got %v, want ErrInvalidWireType", err)
	}
}

func TestPackedVarints(t *testing.T) {
	want := []uint64{0, 1, 300, math.MaxUint64}
	var payload []byte
	for _, v := range want {
		payload = AppendVarint(payload, v)
	}
	got, err := ReadPackedVarints(payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d values, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %d, want %d", i, got[i], want[i])
		}
	}
	// Payload ending mid-varint is a truncation error.
	if _, err := ReadPackedVarints([]byte{0x01, 0x80}, nil); err != ErrTruncated {
		t.Fatalf("truncated packed: got %v, want ErrTruncated", err)
	}
	// Empty payload decodes to zero values.
	out, err := ReadPackedVarints(nil, nil)
	if err != nil || len(out) != 0 {
		t.Fatalf("empty packed: got %v, %v", out, err)
	}
}

func TestReaderReuse(t *testing.T) {
	r := AcquireReader(AppendVarint(nil, 7))
	if v, _ := r.ReadVarint(); v != 7 {
		t.Fatalf("got %d, want 7", v)
	}
	r.Release()
	r2 := AcquireReader(AppendVarint(nil, 8))
	if v, _ := r2.ReadVarint(); v != 8 {
		t.Fatalf("reused reader: got %d, want 8", v)
	}
	r2.Release()
	if !Balanced() {
		t.Fatal("pool imbalance after reader reuse")
	}
}
