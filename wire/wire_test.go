package wire

import (
	"bytes"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	vals := []uint64{0, 1, 127, 128, 300, 1 << 20, 1 << 32, 1<<63 + 1, ^uint64(0)}
	for _, v := range vals {
		buf := AppendVarint(nil, v)
		got, n, err := ConsumeVarint(buf)
		if err != nil {
			t.Fatalf("ConsumeVarint(%d) error: %v", v, err)
		}
		if got != v || n != len(buf) {
			t.Fatalf("roundtrip %d: got %d, n=%d, len=%d", v, got, n, len(buf))
		}
	}
	// 300 -> 0xAC 0x02 (canonical protobuf example).
	if got := AppendVarint(nil, 300); !bytes.Equal(got, []byte{0xAC, 0x02}) {
		t.Fatalf("varint(300) = %x, want ac02", got)
	}
}

func TestVarintTruncated(t *testing.T) {
	full := AppendVarint(nil, 1<<63+7)
	for i := 0; i < len(full); i++ {
		if _, _, err := ConsumeVarint(full[:i]); !IsTruncated(err) {
			t.Fatalf("prefix %d: want truncation, got %v", i, err)
		}
	}
	if _, _, err := ConsumeVarint(nil); !IsTruncated(err) {
		t.Fatalf("empty input: want truncation, got %v", err)
	}
}

func TestVarintOverflow(t *testing.T) {
	over := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	if _, _, err := ConsumeVarint(over); err != ErrOverflow {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	ten := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
	if _, _, err := ConsumeVarint(ten); err != ErrOverflow {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	max := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	v, n, err := ConsumeVarint(max)
	if err != nil || v != ^uint64(0) || n != 10 {
		t.Fatalf("max varint: v=%d n=%d err=%v", v, n, err)
	}
}

func TestZigzag(t *testing.T) {
	cases := []struct {
		signed   int64
		unsigned uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4},
		{1<<63 - 1, ^uint64(0) - 1},
		{-1 << 63, ^uint64(0)},
	}
	for _, c := range cases {
		if got := ZigzagEncode(c.signed); got != c.unsigned {
			t.Fatalf("ZigzagEncode(%d) = %d, want %d", c.signed, got, c.unsigned)
		}
		if got := ZigzagDecode(c.unsigned); got != c.signed {
			t.Fatalf("ZigzagDecode(%d) = %d, want %d", c.unsigned, got, c.signed)
		}
	}
}

func TestFixedRoundTripAndTruncation(t *testing.T) {
	b32 := AppendFixed32(nil, 0xdeadbeef)
	if v, n, err := ConsumeFixed32(b32); err != nil || v != 0xdeadbeef || n != 4 {
		t.Fatalf("fixed32 roundtrip: v=%x n=%d err=%v", v, n, err)
	}
	for i := 0; i < 4; i++ {
		if _, _, err := ConsumeFixed32(b32[:i]); !IsTruncated(err) {
			t.Fatalf("fixed32 prefix %d: want truncation, got %v", i, err)
		}
	}
	b64 := AppendFixed64(nil, 0x0123456789abcdef)
	if v, n, err := ConsumeFixed64(b64); err != nil || v != 0x0123456789abcdef || n != 8 {
		t.Fatalf("fixed64 roundtrip: v=%x n=%d err=%v", v, n, err)
	}
	for i := 0; i < 8; i++ {
		if _, _, err := ConsumeFixed64(b64[:i]); !IsTruncated(err) {
			t.Fatalf("fixed64 prefix %d: want truncation, got %v", i, err)
		}
	}
}

func TestTag(t *testing.T) {
	buf := AppendTag(nil, 15, Bytes)
	num, wt, n, err := ConsumeTag(buf)
	if err != nil || num != 15 || wt != Bytes || n != len(buf) {
		t.Fatalf("tag roundtrip: num=%d wt=%d n=%d err=%v", num, wt, n, err)
	}
	if _, _, _, err := ConsumeTag([]byte{0x00}); err != ErrBadTag {
		t.Fatalf("field 0: want ErrBadTag, got %v", err)
	}
	if _, _, _, err := ConsumeTag([]byte{0x0e}); err != ErrBadWireType {
		t.Fatalf("wire type 6: want ErrBadWireType, got %v", err)
	}
	if _, _, _, err := ConsumeTag([]byte{0x0b}); err != ErrBadWireType {
		t.Fatalf("group: want ErrBadWireType, got %v", err)
	}
}
