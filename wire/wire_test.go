package wire

import (
	"errors"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 1<<14 - 1, 1 << 14, 1<<21 - 1, 1 << 21,
		1<<32 - 1, 1 << 32, 1<<63 - 1, 1 << 63, math.MaxUint64}
	for _, want := range cases {
		buf := AppendVarint(nil, want)
		r := NewReader(buf)
		got, err := r.ReadVarint()
		if err != nil {
			t.Fatalf("ReadVarint(%d): %v", want, err)
		}
		if got != want {
			t.Errorf("varint roundtrip: got %d, want %d", got, want)
		}
		if r.Remaining() != 0 {
			t.Errorf("varint %d: %d trailing bytes", want, r.Remaining())
		}
	}
}

func TestVarintCanonicalSizes(t *testing.T) {
	// 7-bit compression: each byte carries 7 payload bits.
	sizes := []struct {
		v    uint64
		size int
	}{
		{0, 1}, {127, 1}, {128, 2}, {1<<14 - 1, 2}, {1 << 14, 3},
		{1<<21 - 1, 3}, {1 << 21, 4}, {math.MaxUint64, 10},
	}
	for _, c := range sizes {
		if n := len(AppendVarint(nil, c.v)); n != c.size {
			t.Errorf("varint(%d) size = %d, want %d", c.v, n, c.size)
		}
	}
}

func TestVarintTruncation(t *testing.T) {
	full := AppendVarint(nil, math.MaxUint64) // 10 bytes
	for cut := 0; cut < len(full); cut++ {
		r := NewReader(full[:cut])
		if _, err := r.ReadVarint(); !errors.Is(err, ErrTruncated) {
			t.Errorf("prefix len %d: err = %v, want ErrTruncated", cut, err)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: exceeds 64-bit range.
	buf := make([]byte, 11)
	for i := range buf {
		buf[i] = 0x80
	}
	if _, err := NewReader(buf).ReadVarint(); !errors.Is(err, ErrOverflow) {
		t.Errorf("11-byte varint: err = %v, want ErrOverflow", err)
	}
	// 10th byte carrying more than 1 payload bit.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}
	if _, err := NewReader(buf).ReadVarint(); !errors.Is(err, ErrOverflow) {
		t.Errorf("10th byte >1: err = %v, want ErrOverflow", err)
	}
	// Valid max: 10th byte == 1.
	buf = []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	v, err := NewReader(buf).ReadVarint()
	if err != nil || v != math.MaxUint64 {
		t.Errorf("max varint: v=%d err=%v", v, err)
	}
}

func TestZigZagRoundTrip(t *testing.T) {
	cases := []int64{0, -1, 1, -2, 2, math.MinInt32, math.MaxInt32,
		math.MinInt64, math.MaxInt64, -1234567890123}
	for _, want := range cases {
		buf := AppendZigZag(nil, want)
		got, err := NewReader(buf).ReadZigZag64()
		if err != nil || got != want {
			t.Errorf("zigzag(%d) = %d, %v", want, got, err)
		}
	}
	// ZigZag keeps small magnitudes small: -1..64 fit in 1-2 bytes.
	if n := len(AppendZigZag(nil, -1)); n != 1 {
		t.Errorf("zigzag(-1) size = %d, want 1", n)
	}
	if n := len(AppendZigZag(nil, 63)); n != 1 {
		t.Errorf("zigzag(63) size = %d, want 1", n)
	}
}

func TestZigZag32(t *testing.T) {
	for _, want := range []int32{0, -1, 1, math.MinInt32, math.MaxInt32} {
		buf := AppendZigZag(nil, int64(want))
		got, err := NewReader(buf).ReadZigZag32()
		if err != nil || got != want {
			t.Errorf("zigzag32(%d) = %d, %v", want, got, err)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	buf := AppendFixed32(nil, 0xdeadbeef)
	buf = AppendFixed64(buf, 0x0123456789abcdef)
	r := NewReader(buf)
	v32, err := r.ReadFixed32()
	if err != nil || v32 != 0xdeadbeef {
		t.Errorf("fixed32 = %#x, %v", v32, err)
	}
	v64, err := r.ReadFixed64()
	if err != nil || v64 != 0x0123456789abcdef {
		t.Errorf("fixed64 = %#x, %v", v64, err)
	}
}

func TestFixedTruncation(t *testing.T) {
	for n := 0; n < 4; n++ {
		if _, err := NewReader(make([]byte, n)).ReadFixed32(); !errors.Is(err, ErrTruncated) {
			t.Errorf("fixed32 with %d bytes: err = %v", n, err)
		}
	}
	for n := 0; n < 8; n++ {
		if _, err := NewReader(make([]byte, n)).ReadFixed64(); !errors.Is(err, ErrTruncated) {
			t.Errorf("fixed64 with %d bytes: err = %v", n, err)
		}
	}
}

func TestReadBytesBounds(t *testing.T) {
	// Declared length exceeds remaining input -> safe error, no panic.
	buf := AppendVarint(nil, 100)
	buf = append(buf, 1, 2, 3)
	if _, err := NewReader(buf).ReadBytes(); !errors.Is(err, ErrTruncated) {
		t.Errorf("oversized length: err = %v, want ErrTruncated", err)
	}
	// Empty buffer.
	if _, err := NewReader(nil).ReadBytes(); !errors.Is(err, ErrTruncated) {
		t.Errorf("empty: err = %v, want ErrTruncated", err)
	}
	// Valid payload.
	buf = AppendBytes(nil, []byte("hello"))
	b, err := NewReader(buf).ReadBytes()
	if err != nil || string(b) != "hello" {
		t.Errorf("read bytes = %q, %v", b, err)
	}
}

func TestInvalidTags(t *testing.T) {
	// Field number 0 is illegal.
	if _, _, err := NewReader([]byte{0x00}).ReadTag(); !errors.Is(err, ErrInvalidFieldNumber) {
		t.Errorf("field 0: err = %v", err)
	}
	// Wire types 3 and 4 (groups) are rejected.
	for _, tag := range []byte{0x0b, 0x0c} {
		if _, _, err := NewReader([]byte{tag}).ReadTag(); !errors.Is(err, ErrInvalidWireType) {
			t.Errorf("tag %#x: err = %v, want ErrInvalidWireType", tag, err)
		}
	}
}

// buildNestedMessage hand-crafts a 3-level nested message:
//
//	Envelope{1: id(varint), 2: Middle(bytes)}
//	Middle{1: delta(zigzag varint), 2: Inner(bytes), 3: packed []varint}
//	Inner{1: name(string), 2: packed []fixed32, 3: packed []fixed64, 4: score(fixed64)}
func buildNestedMessage() []byte {
	var inner []byte
	inner = AppendTag(inner, 1, Bytes)
	inner = AppendBytes(inner, []byte("deep-node"))
	var pf32 []byte
	for _, v := range []uint32{7, 8, 9} {
		pf32 = AppendFixed32(pf32, v)
	}
	inner = AppendTag(inner, 2, Bytes)
	inner = AppendBytes(inner, pf32)
	var pf64 []byte
	for _, v := range []uint64{1000, 2000} {
		pf64 = AppendFixed64(pf64, v)
	}
	inner = AppendTag(inner, 3, Bytes)
	inner = AppendBytes(inner, pf64)
	inner = AppendTag(inner, 4, Fixed64)
	inner = AppendFixed64(inner, math.Float64bits(3.5))

	var mid []byte
	mid = AppendTag(mid, 1, Varint)
	mid = AppendZigZag(mid, -42)
	mid = AppendTag(mid, 2, Bytes)
	mid = AppendBytes(mid, inner)
	var packed []byte
	for _, v := range []uint64{1, 150, 3000, 65536} {
		packed = AppendVarint(packed, v)
	}
	mid = AppendTag(mid, 3, Bytes)
	mid = AppendBytes(mid, packed)

	var env []byte
	env = AppendTag(env, 1, Varint)
	env = AppendVarint(env, 9001)
	env = AppendTag(env, 2, Bytes)
	env = AppendBytes(env, mid)
	return env
}

func TestNestedMessageParsing(t *testing.T) {
	data := buildNestedMessage()
	r := NewReader(data)

	// Envelope level.
	f, ok, err := r.Next()
	if !ok || err != nil || f.Number != 1 || f.Type != Varint || f.Varint != 9001 {
		t.Fatalf("envelope.id = %+v ok=%v err=%v", f, ok, err)
	}
	f, ok, err = r.Next()
	if !ok || err != nil || f.Number != 2 || f.Type != Bytes {
		t.Fatalf("envelope.mid = %+v ok=%v err=%v", f, ok, err)
	}
	if _, ok, err = r.Next(); ok || err != nil {
		t.Fatalf("envelope should be exhausted: ok=%v err=%v", ok, err)
	}

	// Middle level.
	mr := NewReader(f.Data)
	f, _, err = mr.Next()
	if err != nil || f.Number != 1 || f.ZigZag() != -42 {
		t.Fatalf("mid.delta = %+v err=%v", f, err)
	}
	f, _, err = mr.Next()
	if err != nil || f.Number != 2 || f.Type != Bytes {
		t.Fatalf("mid.inner = %+v err=%v", f, err)
	}
	innerData := f.Data
	f, _, err = mr.Next()
	if err != nil || f.Number != 3 || f.Type != Bytes {
		t.Fatalf("mid.packed = %+v err=%v", f, err)
	}
	vals, err := PackedVarints(f.Data, nil)
	if err != nil {
		t.Fatalf("packed varints: %v", err)
	}
	want := []uint64{1, 150, 3000, 65536}
	if len(vals) != len(want) {
		t.Fatalf("packed varints len = %d", len(vals))
	}
	for i := range want {
		if vals[i] != want[i] {
			t.Errorf("packed[%d] = %d, want %d", i, vals[i], want[i])
		}
	}

	// Inner level.
	ir := NewReader(innerData)
	f, _, err = ir.Next()
	if err != nil || f.Number != 1 || string(f.Data) != "deep-node" {
		t.Fatalf("inner.name = %+v err=%v", f, err)
	}
	f, _, _ = ir.Next()
	f32s, err := PackedFixed32(f.Data, nil)
	if err != nil || len(f32s) != 3 || f32s[0] != 7 || f32s[2] != 9 {
		t.Fatalf("inner.packed32 = %v err=%v", f32s, err)
	}
	f, _, _ = ir.Next()
	f64s, err := PackedFixed64(f.Data, nil)
	if err != nil || len(f64s) != 2 || f64s[0] != 1000 || f64s[1] != 2000 {
		t.Fatalf("inner.packed64 = %v err=%v", f64s, err)
	}
	f, _, _ = ir.Next()
	if f.Number != 4 || f.Type != Fixed64 || math.Float64frombits(f.Fixed64) != 3.5 {
		t.Fatalf("inner.score = %+v", f)
	}
	if _, ok, _ := ir.Next(); ok {
		t.Fatal("inner should be exhausted")
	}
}

func TestTruncationAtEveryOffset(t *testing.T) {
	// Cutting the stream at ANY offset must yield a clean error, never a panic.
	full := buildNestedMessage()
	for cut := 0; cut < len(full); cut++ {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Fatalf("panic at cut=%d: %v", cut, rec)
				}
			}()
			r := NewReader(full[:cut])
			for {
				f, ok, err := r.Next()
				if err != nil {
					if !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrOverflow) {
						t.Fatalf("cut=%d: unexpected err %v", cut, err)
					}
					return
				}
				if !ok {
					// A cut landing exactly on a field boundary is
					// indistinguishable from a complete stream; that is fine.
					return
				}
				_ = f
			}
		}()
	}
}

func TestSkip(t *testing.T) {
	var buf []byte
	buf = AppendTag(buf, 1, Varint)
	buf = AppendVarint(buf, 12345)
	buf = AppendTag(buf, 2, Fixed64)
	buf = AppendFixed64(buf, 99)
	buf = AppendTag(buf, 3, Bytes)
	buf = AppendBytes(buf, []byte("skip-me"))
	buf = AppendTag(buf, 4, Fixed32)
	buf = AppendFixed32(buf, 7)
	buf = AppendTag(buf, 5, Varint)
	buf = AppendVarint(buf, 42)

	r := NewReader(buf)
	for i := 0; i < 4; i++ {
		num, typ, err := r.ReadTag()
		if err != nil {
			t.Fatalf("tag %d: %v", i, err)
		}
		if err := r.Skip(typ); err != nil {
			t.Fatalf("skip field %d: %v", num, err)
		}
	}
	num, typ, err := r.ReadTag()
	if err != nil || num != 5 || typ != Varint {
		t.Fatalf("final tag = %d/%d, %v", num, typ, err)
	}
	v, _ := r.ReadVarint()
	if v != 42 {
		t.Errorf("final value = %d, want 42", v)
	}
}

func TestPackedTruncation(t *testing.T) {
	if _, err := PackedFixed32([]byte{1, 2, 3}, nil); !errors.Is(err, ErrTruncated) {
		t.Errorf("packed32 odd length: err = %v", err)
	}
	if _, err := PackedFixed64([]byte{1, 2, 3, 4, 5}, nil); !errors.Is(err, ErrTruncated) {
		t.Errorf("packed64 odd length: err = %v", err)
	}
	if _, err := PackedVarints([]byte{0x80}, nil); !errors.Is(err, ErrTruncated) {
		t.Errorf("packed varint truncated: err = %v", err)
	}
}

func TestFloatRoundTrip(t *testing.T) {
	var buf []byte
	buf = AppendFixed32(buf, math.Float32bits(-1.5))
	buf = AppendFixed64(buf, math.Float64bits(2.718281828))
	r := NewReader(buf)
	f32, err := r.ReadFloat32()
	if err != nil || f32 != -1.5 {
		t.Errorf("float32 = %v, %v", f32, err)
	}
	f64, err := r.ReadFloat64()
	if err != nil || f64 != 2.718281828 {
		t.Errorf("float64 = %v, %v", f64, err)
	}
}
