package wire

import (
	"bytes"
	"math"
	"math/rand"
	"testing"
)

func TestUvarintKnownEncodings(t *testing.T) {
	cases := []struct {
		v   uint64
		enc []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{300, []byte{0xac, 0x02}}, // canonical protobuf example
		{math.MaxUint64, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}},
	}
	for _, c := range cases {
		got := AppendUvarint(nil, c.v)
		if !bytes.Equal(got, c.enc) {
			t.Errorf("AppendUvarint(%d) = % x, want % x", c.v, got, c.enc)
		}
		if UvarintSize(c.v) != len(c.enc) {
			t.Errorf("UvarintSize(%d) = %d, want %d", c.v, UvarintSize(c.v), len(c.enc))
		}
		v, n, err := ConsumeUvarint(c.enc, 0)
		if err != nil || v != c.v || n != len(c.enc) {
			t.Errorf("ConsumeUvarint(% x) = %d, %d, %v", c.enc, v, n, err)
		}
	}
}

func TestUvarintRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 10000; i++ {
		v := rng.Uint64() >> uint(rng.Intn(64))
		buf := AppendUvarint(nil, v)
		got, n, err := ConsumeUvarint(buf, 0)
		if err != nil || got != v || n != len(buf) {
			t.Fatalf("round trip %d failed: got %d n=%d err=%v", v, got, n, err)
		}
	}
}

func TestVarintNegativeTakesTenBytes(t *testing.T) {
	buf := AppendVarint(nil, -1)
	if len(buf) != 10 {
		t.Fatalf("varint(-1) len = %d, want 10", len(buf))
	}
	v, _, err := ConsumeVarint(buf, 0)
	if err != nil || v != -1 {
		t.Fatalf("ConsumeVarint = %d, %v", v, err)
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
		if got := EncodeZigZag64(c.signed); got != c.unsigned {
			t.Errorf("EncodeZigZag64(%d) = %d, want %d", c.signed, got, c.unsigned)
		}
		if got := DecodeZigZag64(c.unsigned); got != c.signed {
			t.Errorf("DecodeZigZag64(%d) = %d, want %d", c.unsigned, got, c.signed)
		}
	}
	c32 := []struct {
		signed   int32
		unsigned uint32
	}{{0, 0}, {-1, 1}, {1, 2}, {math.MinInt32, math.MaxUint32}, {math.MaxInt32, math.MaxUint32 - 1}}
	for _, c := range c32 {
		if got := EncodeZigZag32(c.signed); got != c.unsigned {
			t.Errorf("EncodeZigZag32(%d) = %d, want %d", c.signed, got, c.unsigned)
		}
		if got := DecodeZigZag32(c.unsigned); got != c.signed {
			t.Errorf("DecodeZigZag32(%d) = %d, want %d", c.unsigned, got, c.signed)
		}
	}
}

func TestFixedRoundTrip(t *testing.T) {
	b32 := AppendFixed32(nil, 0xdeadbeef)
	if len(b32) != 4 || b32[0] != 0xef || b32[3] != 0xde {
		t.Fatalf("fixed32 little-endian wrong: % x", b32)
	}
	v32, n, err := ConsumeFixed32(b32, 0)
	if err != nil || v32 != 0xdeadbeef || n != 4 {
		t.Fatalf("ConsumeFixed32 = %x, %d, %v", v32, n, err)
	}
	b64 := AppendFixed64(nil, 0x0123456789abcdef)
	v64, n, err := ConsumeFixed64(b64, 0)
	if err != nil || v64 != 0x0123456789abcdef || n != 8 {
		t.Fatalf("ConsumeFixed64 = %x, %d, %v", v64, n, err)
	}
}

func TestTruncationGuards(t *testing.T) {
	// Varint cut at every prefix of a 3-byte encoding.
	full := AppendUvarint(nil, 1<<20)
	for i := 0; i < len(full); i++ {
		if _, _, err := ConsumeUvarint(full[:i], 0); err != ErrTruncated {
			t.Errorf("ConsumeUvarint(prefix %d) err = %v, want ErrTruncated", i, err)
		}
	}
	// Fixed32/64 short buffers.
	for i := 0; i < 4; i++ {
		if _, _, err := ConsumeFixed32(make([]byte, i), 0); err != ErrTruncated {
			t.Errorf("ConsumeFixed32(%d bytes) err = %v", i, err)
		}
	}
	for i := 0; i < 8; i++ {
		if _, _, err := ConsumeFixed64(make([]byte, i), 0); err != ErrTruncated {
			t.Errorf("ConsumeFixed64(%d bytes) err = %v", i, err)
		}
	}
	// Length-delimited: declared length beyond buffer end.
	buf := AppendUvarint(nil, 100)
	buf = append(buf, 1, 2, 3)
	if _, _, err := ConsumeBytes(buf, 0); err != ErrTruncated {
		t.Errorf("ConsumeBytes oversized length err = %v, want ErrTruncated", err)
	}
	// Out-of-range positions must not panic.
	if _, _, err := ConsumeUvarint([]byte{1}, 5); err != ErrTruncated {
		t.Errorf("ConsumeUvarint pos>len err = %v", err)
	}
	if _, _, err := ConsumeFixed32([]byte{1, 2, 3, 4}, -1); err != ErrTruncated {
		t.Errorf("ConsumeFixed32 negative pos err = %v", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes.
	bad := bytes.Repeat([]byte{0x80}, 11)
	if _, _, err := ConsumeUvarint(bad, 0); err != ErrOverflow {
		t.Errorf("11-byte varint err = %v, want ErrOverflow", err)
	}
	// 10th byte carrying bits beyond 64.
	bad = append(bytes.Repeat([]byte{0xff}, 9), 0x7f)
	if _, _, err := ConsumeUvarint(bad, 0); err != ErrOverflow {
		t.Errorf("10th byte > 1 err = %v, want ErrOverflow", err)
	}
}

func TestKeyValidation(t *testing.T) {
	buf := AppendKey(nil, 15, WireLengthPref)
	num, wt, n, err := ConsumeKey(buf, 0)
	if err != nil || num != 15 || wt != WireLengthPref || n != len(buf) {
		t.Fatalf("ConsumeKey = %d, %d, %d, %v", num, wt, n, err)
	}
	for _, wt := range []int{3, 4, 6, 7} {
		b := AppendUvarint(nil, uint64(1<<3|wt))
		if _, _, _, err := ConsumeKey(b, 0); err != ErrInvalidWireType {
			t.Errorf("wire type %d err = %v, want ErrInvalidWireType", wt, err)
		}
	}
	if _, _, _, err := ConsumeKey([]byte{0x08 - 0x08}, 0); err != ErrBadFieldNumber {
		t.Errorf("field number 0 err = %v, want ErrBadFieldNumber", err)
	}
}

func TestNextFieldWalk(t *testing.T) {
	var buf []byte
	buf = AppendKey(buf, 1, WireVarint)
	buf = AppendUvarint(buf, 150)
	buf = AppendKey(buf, 2, WireFixed64)
	buf = AppendFixed64(buf, 0xfeedfacecafef00d)
	buf = AppendKey(buf, 3, WireLengthPref)
	buf = AppendString(buf, "hello")
	buf = AppendKey(buf, 4, WireFixed32)
	buf = AppendFixed32(buf, 42)

	var got []Field
	pos := 0
	for {
		f, next, done, err := NextField(buf, pos)
		if err != nil {
			t.Fatalf("NextField at %d: %v", pos, err)
		}
		if done {
			break
		}
		got = append(got, f)
		pos = next
	}
	if len(got) != 4 {
		t.Fatalf("got %d fields, want 4", len(got))
	}
	if got[0].Number != 1 || got[0].Varint != 150 {
		t.Errorf("field 1 = %+v", got[0])
	}
	if got[1].Number != 2 || got[1].Fixed64 != 0xfeedfacecafef00d {
		t.Errorf("field 2 = %+v", got[1])
	}
	if got[2].Number != 3 || string(got[2].Bytes) != "hello" {
		t.Errorf("field 3 = %+v", got[2])
	}
	if got[3].Number != 4 || got[3].Fixed32 != 42 {
		t.Errorf("field 4 = %+v", got[3])
	}
}

func TestNextFieldTruncationAtEveryByte(t *testing.T) {
	var buf []byte
	buf = AppendKey(buf, 1, WireVarint)
	buf = AppendUvarint(buf, 1<<21)
	buf = AppendKey(buf, 2, WireLengthPref)
	buf = AppendString(buf, "payload")
	buf = AppendKey(buf, 3, WireFixed64)
	buf = AppendFixed64(buf, 99)

	// Every strict prefix must fail cleanly with an error, never panic.
	for i := 0; i < len(buf); i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("prefix %d panicked: %v", i, r)
				}
			}()
			pos := 0
			for pos < i {
				_, next, done, err := NextField(buf[:i], pos)
				if err != nil {
					return // truncation detected safely
				}
				if done {
					return
				}
				pos = next
			}
		}()
	}
}

func TestPackedHelpers(t *testing.T) {
	vals := []uint64{0, 1, 300, math.MaxUint64}
	body := AppendPackedUvarints(nil, vals)
	// Strip the length prefix ConsumePackedUvarints doesn't expect:
	_, n, err := ConsumeUvarint(body, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ConsumePackedUvarints(body[n:])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(vals) {
		t.Fatalf("got %d values, want %d", len(got), len(vals))
	}
	for i := range vals {
		if got[i] != vals[i] {
			t.Errorf("value %d = %d, want %d", i, got[i], vals[i])
		}
	}
	// Truncated packed body must error.
	if _, err := ConsumePackedUvarints([]byte{0x80}); err == nil {
		t.Error("truncated packed varint body: want error")
	}
	if _, err := ConsumePackedFixed32([]byte{1, 2, 3}); err == nil {
		t.Error("packed fixed32 len 3: want error")
	}
	if _, err := ConsumePackedFixed64([]byte{1, 2, 3, 4, 5}); err == nil {
		t.Error("packed fixed64 len 5: want error")
	}
}

func TestRandomGarbageNeverPanics(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 2000; trial++ {
		buf := make([]byte, rng.Intn(64))
		rng.Read(buf)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("trial %d panicked on % x: %v", trial, buf, r)
				}
			}()
			pos := 0
			for pos < len(buf) {
				_, next, done, err := NextField(buf, pos)
				if err != nil || done {
					return
				}
				if next <= pos {
					t.Fatalf("trial %d: walker stuck at %d", trial, pos)
				}
				pos = next
			}
		}()
	}
}

func TestSkipValue(t *testing.T) {
	var buf []byte
	buf = AppendUvarint(buf, 12345)
	if n, err := SkipValue(buf, 0, WireVarint); err != nil || n != len(buf) {
		t.Errorf("skip varint: n=%d err=%v", n, err)
	}
	if _, err := SkipValue(buf, 0, 7); err != ErrInvalidWireType {
		t.Errorf("skip bad wire type err = %v", err)
	}
	if _, err := SkipValue([]byte{0x80}, 0, WireVarint); err != ErrTruncated {
		t.Errorf("skip truncated varint err = %v", err)
	}
}
