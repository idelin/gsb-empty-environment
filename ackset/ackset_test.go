package ackset

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/rand"
	"slices"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sameSet(a, b []uint32) bool {
	return slices.Equal(normalize(a), normalize(b))
}

func TestGolden(t *testing.T) {
	spread := []uint32{0, 2}
	for v := uint32(3); v <= 130; v++ {
		spread = append(spread, v)
	}
	cases := []struct {
		name string
		set  []uint32
		hex  string
	}{
		{"empty", nil, "00"},
		{"single", []uint32{7}, "0101000000000701"},
		{"contiguous", []uint32{1, 2, 3}, "0101000000000103"},
		{"small bitmap", []uint32{1, 2, 3, 5}, "010101000000010517"},
		{"wrap range", []uint32{0xFFFFFFFF, 0, 1}, "010100FFFFFFFF03"},
		{"wrap range 2", []uint32{0xFFFFFFFE, 0xFFFFFFFF, 0, 1, 2}, "010100FFFFFFFE05"},
		{"bitmap over hole", []uint32{10, 11, 20, 21, 22}, "0101010000000A0D031C"},
		{"two ranges", []uint32{0, 1000}, "010200000000000100000003E801"},
		{"bitmap+range", append([]uint32{0, 2}, seq(4, 104)...), "010201000000000305000000000465"},
		// Bitmap directly followed by a range (adjacent, different
		// types): shrinking the range to 127 elements saves a varint
		// byte, so the adjacent layout is strictly minimal.
		{"adjacent bitmap+range", spread, "01020100000000040D00000000047F"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := mustHex(t, tc.hex)
			got, err := Encode(tc.set)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Encode = %x, want %x", got, want)
			}
			dec, err := Decode(want)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !sameSet(dec, tc.set) {
				t.Fatalf("Decode = %v, want set %v", dec, tc.set)
			}
		})
	}
}

func seq(lo, hi uint32) []uint32 {
	var out []uint32
	for v := lo; v <= hi; v++ {
		out = append(out, v)
	}
	return out
}

func TestDecodeOrder(t *testing.T) {
	// Decoded elements come out in modular order from the anchor.
	dec, err := Decode(mustHex(t, "010100FFFFFFFF03"))
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{0xFFFFFFFF, 0, 1}
	if !slices.Equal(dec, want) {
		t.Fatalf("got %v, want %v", dec, want)
	}
}

func TestEncodeErrors(t *testing.T) {
	if _, err := Encode([]uint32{0, 1 << 31}); !errors.Is(err, ErrTooSparse) {
		t.Fatalf("span 2^31: got %v, want ErrTooSparse", err)
	}
	if _, err := Encode([]uint32{0, (1 << 31) - 1}); err != nil {
		t.Fatalf("span 2^31-1 must be encodable: %v", err)
	}
	big := make([]uint32, MaxElements+1)
	for i := range big {
		big[i] = uint32(i)
	}
	if _, err := Encode(big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestReject(t *testing.T) {
	tooMany := append([]byte{modeSegments, 1, segRange, 0, 0, 0, 0},
		binary.AppendUvarint(nil, MaxElements+1)...)
	cases := []struct {
		name string
		hex  string
		raw  []byte
		want error
	}{
		{"empty input", "", nil, ErrTruncated},
		{"empty with trailing", "0000", nil, ErrTrailing},
		{"bad mode", "07", nil, ErrMalformed},
		{"zero segments", "0100", nil, ErrNotCanonical},
		{"bad segment type", "0101020000000001", nil, ErrMalformed},
		{"truncated start", "010100", nil, ErrTruncated},
		{"truncated count", "01", nil, ErrTruncated},
		{"empty range", "0101000000000A00", nil, ErrNotCanonical},
		// ranges [10..12] and [11..12]
		{"overlap", "0102000000000A03000000000B02", nil, ErrOverlap},
		// ranges [20..20] then [10..10]: backwards
		{"out of order", "0102000000001401000000000A01", nil, ErrOverlap},
		// ranges [10..12] and [13..14] must be merged
		{"mergeable ranges", "0102000000000A03000000000D02", nil, ErrNotCanonical},
		{"bitmap 1 bit", "0101010000000A0101", nil, ErrNotCanonical},
		{"bitmap leading zero", "0101010000000A0306", nil, ErrNotCanonical},
		{"bitmap trailing zero", "0101010000000A0407", nil, ErrNotCanonical},
		{"bitmap padding bits", "0101010000000A030F", nil, ErrNotCanonical},
		{"bitmap all ones", "0101010000000A0307", nil, ErrNotCanonical},
		{"bitmap too long", "010101000000008140", nil, ErrNotCanonical},
		// {0..19, 100} as one bitmap; two ranges are smaller
		{"suboptimal segmentation", "0101010000000065" +
			"FFFF0F0000000000000000000010", nil, ErrNotCanonical},
		// overlong varint for range count (0x81 0x00 == 1)
		{"overlong varint", "010100000000058100", nil, ErrNotCanonical},
		{"varint overflow", "01FFFFFFFFFFFFFFFFFFFF01", nil, ErrMalformed},
		// [0..1] and [2^31..2^31+1]: span reaches half the space
		{"span too large", "0102000000000002008000000002", nil, ErrTooSparse},
		// [0..1] then start 2^31+1: modular distance >= 2^31
		{"ambiguous order", "0102000000000002008000000102", nil, ErrOverlap},
		{"trailing after segments", "0101000000000701" + "00", nil, ErrTrailing},
		{"too many elements", "", tooMany, ErrTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.raw
			if data == nil {
				data = mustHex(t, tc.hex)
			}
			if _, err := Decode(data); !errors.Is(err, tc.want) {
				t.Fatalf("Decode = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestRoundtrip(t *testing.T) {
	// Two bitmap segments: even numbers over 16000 offsets exceed one
	// MaxBitmapBits window.
	var wide []uint32
	for v := uint32(0); v < 16000; v += 2 {
		wide = append(wide, v)
	}
	sets := [][]uint32{
		nil,
		{0},
		{0xFFFFFFFF},
		{5, 5, 3, 3}, // duplicates
		seq(100, 200),
		{0xFFFFFFFE, 0xFFFFFFFF, 0, 1, 2},
		{0x7FFFFFFF, 0x80000000, 0x80000001},
		{0, 2, 4, 8, 16, 32, 64},
		wide,
	}
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		base := rng.Uint32()
		n := rng.Intn(64)
		s := make([]uint32, 0, n)
		for j := 0; j < n; j++ {
			s = append(s, base+uint32(rng.Intn(512)))
		}
		sets = append(sets, s)
	}
	for i, s := range sets {
		enc, err := Encode(s)
		if err != nil {
			t.Fatalf("set %d: Encode: %v", i, err)
		}
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("set %d: Decode(%x): %v", i, enc, err)
		}
		if !sameSet(dec, s) {
			t.Fatalf("set %d: got %v, want %v", i, dec, s)
		}
		// Decoding is deterministic: re-encoding is a fixed point.
		enc2, err := Encode(dec)
		if err != nil {
			t.Fatalf("set %d: re-Encode: %v", i, err)
		}
		if !bytes.Equal(enc, enc2) {
			t.Fatalf("set %d: re-encode %x != %x", i, enc2, enc)
		}
	}
}
