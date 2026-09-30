package fpcodec

import (
	"math"
	"math/rand/v2"
	"testing"
)

func mustConvert(t *testing.T, bits uint64, from, to Width, pol Policy) (uint64, Flag) {
	t.Helper()
	out, flags, err := Convert(bits, from, to, pol)
	if err != nil {
		t.Fatalf("Convert(%x, %d, %d) error: %v", bits, from, to, err)
	}
	return out, flags
}

// TestExhaustive16 checks every one of the 65536 16-bit patterns against
// the big.Int reference for widening to 32 and 64 bits, and verifies the
// narrowing round trip restores the original bits (modulo the documented
// quieting of signaling NaNs).
func TestExhaustive16(t *testing.T) {
	pol := DefaultPolicy()
	const quiet16 = uint64(1) << 9
	for i := 0; i < 1<<16; i++ {
		b := uint64(i)
		isNaN := b&0x7C00 == 0x7C00 && b&0x03FF != 0
		for _, to := range []Width{Width32, Width64} {
			got, gotF := mustConvert(t, b, Width16, to, pol)
			want, wantF := refConvert(b, Width16, to, pol)
			if got != want || gotF != wantF {
				t.Fatalf("16->%d: bits=%#04x got=(%#x,%v) want=(%#x,%v)",
					to, b, got, gotF, want, wantF)
			}
			back, _ := mustConvert(t, got, to, Width16, pol)
			wantBack := b
			if isNaN {
				wantBack |= quiet16 // signaling NaNs are quieted once
			}
			if back != wantBack {
				t.Fatalf("round trip 16->%d->16: bits=%#04x got=%#04x want=%#04x",
					to, b, back, wantBack)
			}
		}
	}
}

// TestMidpointBoundaries32 verifies correct round-to-nearest-ties-to-even
// for 32->16 conversion at every rounding boundary: for each adjacent pair
// of finite float16 values, the exact float32 midpoint must round to the
// even neighbor, and one float32 ulp to either side must round to the
// nearer neighbor. All patterns are derived with integer math only.
func TestMidpointBoundaries32(t *testing.T) {
	pol := DefaultPolicy()
	check := func(lo16, hi16 uint64) {
		// Widen both neighbors exactly to float32.
		lo32, f := mustConvert(t, lo16, Width16, Width32, pol)
		if f != 0 {
			t.Fatalf("widening %#x not exact: %v", lo16, f)
		}
		hi32, _ := mustConvert(t, hi16, Width16, Width32, pol)
		var mid uint64
		if lo16&0x7FFF == 0 {
			// Pair (zero, min subnormal): the midpoint is exactly half of
			// the widened normal, i.e. its exponent field minus one.
			mid = hi32 - 1<<23
		} else {
			// The widened neighbors are linear in their bit patterns
			// (same or adjacent float32 binade), so the exact midpoint is
			// the integer average of the patterns; both are even.
			if (lo32+hi32)&1 != 0 {
				t.Fatalf("neighbors %#x/%#x: odd pattern sum", lo16, hi16)
			}
			mid = (lo32 + hi32) / 2
		}
		even := lo16
		if lo16&1 == 1 {
			even = hi16
		}
		if got, fl := mustConvert(t, mid, Width32, Width16, pol); got != even || !fl.Has(FlagInexact) {
			t.Fatalf("midpoint of %#x/%#x: got=(%#x,%v) want=(%#x,inexact)",
				lo16, hi16, got, fl, even)
		}
		if got, _ := mustConvert(t, mid-1, Width32, Width16, pol); got != lo16 {
			t.Fatalf("mid-1ulp of %#x/%#x: got=%#x want=%#x", lo16, hi16, got, lo16)
		}
		if got, _ := mustConvert(t, mid+1, Width32, Width16, pol); got != hi16 {
			t.Fatalf("mid+1ulp of %#x/%#x: got=%#x want=%#x", lo16, hi16, got, hi16)
		}
	}
	// Positive side: pairs (i, i+1) of adjacent finite patterns.
	for i := uint64(0); i < 0x7BFF; i++ {
		check(i, i+1)
	}
	// Negative side: same magnitudes with the sign bit set.
	for i := uint64(0); i < 0x7BFF; i++ {
		check(0x8000|i, 0x8000|(i+1))
	}
}

// TestRandomCrossCheck compares the optimized engine with the big.Int
// reference on random patterns across all width pairs.
func TestRandomCrossCheck(t *testing.T) {
	pol := DefaultPolicy()
	rng := rand.New(rand.NewPCG(1, 2))
	widths := []Width{Width16, Width32, Width64}
	for _, from := range widths {
		for _, to := range widths {
			for i := 0; i < 20000; i++ {
				b := rng.Uint64()
				if from == Width16 {
					b &= 0xFFFF
				} else if from == Width32 {
					b &= 0xFFFFFFFF
				}
				got, gotF := mustConvert(t, b, from, to, pol)
				want, wantF := refConvert(b, from, to, pol)
				if got != want || gotF != wantF {
					t.Fatalf("%d->%d: bits=%#x got=(%#x,%v) want=(%#x,%v)",
						from, to, b, got, gotF, want, wantF)
				}
			}
		}
	}
}

func TestKnownValues(t *testing.T) {
	pol := DefaultPolicy()
	cases := []struct {
		bits      uint64
		from, to  Width
		want      uint64
		wantFlags Flag
	}{
		{0x3C00, Width16, Width32, 0x3F800000, 0}, // 1.0
		{0x7BFF, Width16, Width32, 0x477FE000, 0}, // 65504 (max f16)
		{0x0001, Width16, Width32, 0x33800000, 0}, // min subnormal f16
		{0x3F800000, Width32, Width16, 0x3C00, 0}, // 1.0 exact
		{uint64(math.Float32bits(65504)), Width32, Width16, 0x7BFF, 0},
		// 65520 is the exact tie between 65504 and 2^16: ties-to-even
		// rounds away from the odd max-finite mantissa, overflowing.
		{uint64(math.Float32bits(65520)), Width32, Width16, 0x7C00, FlagOverflow | FlagInexact},
		{uint64(math.Float32bits(65519)), Width32, Width16, 0x7BFF, FlagInexact},
		{uint64(math.Float32bits(1e30)), Width32, Width16, 0x7C00, FlagOverflow | FlagInexact},
		{uint64(math.Float32bits(1e-8)), Width32, Width16, 0x0000, FlagUnderflow | FlagInexact},
		{uint64(math.Float32bits(5.9604645e-8)), Width32, Width16, 0x0001, 0}, // min f16 subnormal, exact
		{0x8000, Width16, Width32, 0x80000000, 0},                             // -0 keeps its sign
		{0x80000000, Width32, Width16, 0x8000, 0},
		{0x7C00, Width16, Width64, 0x7FF0000000000000, 0}, // +Inf
		{0xFC00, Width16, Width64, 0xFFF0000000000000, 0}, // -Inf
	}
	for _, c := range cases {
		got, flags := mustConvert(t, c.bits, c.from, c.to, pol)
		if got != c.want || flags != c.wantFlags {
			t.Errorf("Convert(%#x, %d, %d) = (%#x, %v), want (%#x, %v)",
				c.bits, c.from, c.to, got, flags, c.want, c.wantFlags)
		}
	}
}

func TestNaNPayloadPolicies(t *testing.T) {
	pol := DefaultPolicy()
	// Quiet NaN 64 -> 16: payload keeps its most significant bits.
	q64 := uint64(0x7FF80000) << 32 // quiet bit + zero payload
	got, flags := mustConvert(t, q64|0x0000ABCD, Width64, Width16, pol)
	wantPayload := (uint64(1)<<51 | 0xABCD) >> 42 // top 10 bits of 52-bit payload
	if got != 0x7C00|wantPayload || flags != 0 {
		t.Errorf("qNaN 64->16: got=(%#x,%v) want=(%#x,none)", got, flags, 0x7C00|wantPayload)
	}
	// Signaling NaN 32 -> 16: quieted, invalid raised, payload truncated.
	s32 := uint64(0x7F800001) // signaling, payload=1
	got, flags = mustConvert(t, s32, Width32, Width16, pol)
	if got != 0x7C00|0x200 || !flags.Has(FlagInvalid) {
		t.Errorf("sNaN 32->16: got=(%#x,%v), want quiet NaN with invalid", got, flags)
	}
	// Signaling NaN whose payload truncates to zero must stay NaN.
	got, _ = mustConvert(t, uint64(0x7F800001), Width32, Width16, pol)
	if got&0x7C00 != 0x7C00 || got&0x3FF == 0 {
		t.Errorf("truncated sNaN degenerated: %#x", got)
	}
	// Negative quiet NaN keeps its sign and MSB-aligned payload.
	payload64 := uint64(0x8000000012345) // quiet bit set
	neg := uint64(1)<<63 | 0x7FF<<52 | payload64
	got, flags = mustConvert(t, neg, Width64, Width32, pol)
	want := uint64(0xFFC00000) | (payload64 >> 29)
	if got != want || flags != 0 {
		t.Errorf("negative qNaN 64->32: got=(%#x,%v) want=(%#x,none)", got, flags, want)
	}
}

func TestSubnormalFlushToZero(t *testing.T) {
	flush := Policy{NaNPayload: NaNTruncateMSB, Signaling: SignalingQuiet, Subnormal: SubnormalFlushToZero}
	// Subnormal input flushed silently to signed zero.
	got, flags := mustConvert(t, 0x0001, Width16, Width32, flush)
	if got != 0 || flags != 0 {
		t.Errorf("flush subnormal input: got=(%#x,%v), want (0,none)", got, flags)
	}
	// Negative subnormal input keeps its sign.
	got, _ = mustConvert(t, 0x8001, Width16, Width32, flush)
	if got != 0x80000000 {
		t.Errorf("flush negative subnormal: got=%#x, want 0x80000000", got)
	}
	// Would-be subnormal result flushed with underflow|inexact.
	got, flags = mustConvert(t, uint64(math.Float32bits(1e-6)), Width32, Width16, flush)
	if got != 0 || flags != FlagUnderflow|FlagInexact {
		t.Errorf("flush subnormal result: got=(%#x,%v), want (0,underflow|inexact)", got, flags)
	}
	// Preserve policy keeps the subnormal.
	got, _ = mustConvert(t, uint64(math.Float32bits(5.9604645e-8)), Width32, Width16, DefaultPolicy())
	if got != 0x0001 {
		t.Errorf("preserve subnormal: got=%#x, want 0x0001", got)
	}
}

func TestUnsupportedWidthRejected(t *testing.T) {
	for _, w := range []Width{0, 8, 80, 128} {
		if _, _, err := Convert(0, w, Width16, DefaultPolicy()); err == nil {
			t.Errorf("Convert from width %d: want error", w)
		}
		if _, _, err := Convert(0, Width16, w, DefaultPolicy()); err == nil {
			t.Errorf("Convert to width %d: want error", w)
		}
		if _, err := Marshal(w, Little, nil); err == nil {
			t.Errorf("Marshal width %d: want error", w)
		}
	}
}
