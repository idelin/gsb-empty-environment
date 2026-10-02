package wire

import (
	"errors"
	"math"
	"testing"
)

func TestVarintRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 127, 128, 300, 16384, 1 << 32, math.MaxInt64, math.MaxUint64}
	for _, want := range cases {
		buf := AppendVarint(nil, want)
		got, n, err := ConsumeVarint(buf)
		if err != nil {
			t.Fatalf("ConsumeVarint(%d): %v", want, err)
		}
		if got != want || n != len(buf) {
			t.Fatalf("roundtrip %d: got %d consumed %d want %d bytes", want, got, n, len(buf))
		}
	}
}

func TestVarintKnownEncodings(t *testing.T) {
	// 300 = 0b100101100 + continuation -> 0xAC 0x02
	buf := AppendVarint(nil, 300)
	if len(buf) != 2 || buf[0] != 0xAC || buf[1] != 0x02 {
		t.Fatalf("300 encoded as %x, want ac02", buf)
	}
	if b := AppendVarint(nil, 1); len(b) != 1 || b[0] != 0x01 {
		t.Fatalf("1 encoded as %x, want 01", b)
	}
	// MaxUint64 must take exactly 10 bytes.
	if b := AppendVarint(nil, math.MaxUint64); len(b) != 10 {
		t.Fatalf("MaxUint64 encoded in %d bytes, want 10", len(b))
	}
}

func TestVarintTruncated(t *testing.T) {
	for _, buf := range [][]byte{
		{0x80},             // continuation bit set, no next byte
		{0xFF, 0xFF, 0xFF}, // 3 bytes all demanding more
	} {
		if _, _, err := ConsumeVarint(buf); !errors.Is(err, ErrTruncated) {
			t.Fatalf("ConsumeVarint(%x) err=%v, want ErrTruncated", buf, err)
		}
	}
}

func TestVarintOverflow(t *testing.T) {
	// 11 continuation bytes: exceeds the 64-bit/10-byte limit.
	over := make([]byte, 11)
	for i := range over {
		over[i] = 0xFF
	}
	if _, _, err := ConsumeVarint(over); !errors.Is(err, ErrOverflow) {
		t.Fatalf("err=%v, want ErrOverflow", err)
	}
	// 10th byte carrying more than the single remaining bit.
	ten := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F}
	if _, _, err := ConsumeVarint(ten); !errors.Is(err, ErrOverflow) {
		t.Fatalf("err=%v, want ErrOverflow", err)
	}
}

func TestZigZagRoundTrip(t *testing.T) {
	cases := []int64{0, -1, 1, -2, 2, 300, -300, math.MaxInt64, math.MinInt64}
	for _, want := range cases {
		if got := DecodeZigZag(EncodeZigZag(want)); got != want {
			t.Fatalf("zigzag roundtrip %d: got %d", want, got)
		}
	}
	// Canonical mappings.
	if EncodeZigZag(-1) != 1 || EncodeZigZag(1) != 2 || EncodeZigZag(-2) != 3 {
		t.Fatal("zigzag canonical mapping broken")
	}
	if EncodeZigZag(math.MinInt64) != math.MaxUint64 {
		t.Fatal("MinInt64 must map to MaxUint64")
	}
}

func TestFixedRoundTrip(t *testing.T) {
	for _, v := range []uint32{0, 1, 0xDEADBEEF, math.MaxUint32} {
		buf := AppendFixed32(nil, v)
		got, n, err := ConsumeFixed32(buf)
		if err != nil || got != v || n != 4 {
			t.Fatalf("fixed32 roundtrip %d: got %d n=%d err=%v", v, got, n, err)
		}
	}
	for _, v := range []uint64{0, 1, 0xDEADBEEFCAFEBABE, math.MaxUint64} {
		buf := AppendFixed64(nil, v)
		got, n, err := ConsumeFixed64(buf)
		if err != nil || got != v || n != 8 {
			t.Fatalf("fixed64 roundtrip %d: got %d n=%d err=%v", v, got, n, err)
		}
	}
}

func TestFixedTruncated(t *testing.T) {
	if _, _, err := ConsumeFixed32([]byte{1, 2, 3}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err=%v, want ErrTruncated", err)
	}
	if _, _, err := ConsumeFixed64([]byte{1, 2, 3, 4, 5, 6, 7}); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err=%v, want ErrTruncated", err)
	}
}

func TestSplitTagValidation(t *testing.T) {
	if _, _, err := SplitTag(0); !errors.Is(err, ErrInvalidFieldNumber) {
		t.Fatalf("tag 0: err=%v, want ErrInvalidFieldNumber", err)
	}
	for _, wt := range []uint64{3, 4, 6, 7} {
		if _, _, err := SplitTag(1<<3 | wt); !errors.Is(err, ErrInvalidWireType) {
			t.Fatalf("wiretype %d: err=%v, want ErrInvalidWireType", wt, err)
		}
	}
	num, wt, err := SplitTag(MakeTag(15, WireFixed32))
	if err != nil || num != 15 || wt != WireFixed32 {
		t.Fatalf("valid tag: num=%d wt=%d err=%v", num, wt, err)
	}
}

func TestConsumeBytesBoundsGuard(t *testing.T) {
	// Declared length 10 but only 2 payload bytes present.
	buf := append([]byte{0x0A}, 0x01, 0x02)
	if _, _, err := ConsumeBytes(buf); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err=%v, want ErrTruncated", err)
	}
	// Empty payload is legal.
	got, n, err := ConsumeBytes([]byte{0x00})
	if err != nil || len(got) != 0 || n != 1 {
		t.Fatalf("empty payload: got=%v n=%d err=%v", got, n, err)
	}
}
