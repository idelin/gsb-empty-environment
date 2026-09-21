package floatconv_test

import (
	"math/rand"
	"testing"

	"floatconv"
	"floatconv/ref"
)

var modes = []floatconv.RoundingMode{
	floatconv.RoundNearestEven,
	floatconv.RoundTowardZero,
	floatconv.RoundTowardPositive,
	floatconv.RoundTowardNegative,
}

func checkAgainstRef(t *testing.T, dst, src floatconv.Format, bits uint64) {
	t.Helper()
	for _, m := range modes {
		got, gotFl, err := floatconv.Convert(dst, src, bits, m)
		if err != nil {
			t.Fatalf("Convert(%v->%v, %#x, %v): %v", src, dst, bits, m, err)
		}
		want, wantFl := ref.Convert(dst, src, bits, m)
		if got != want || gotFl != wantFl {
			t.Fatalf("mismatch %v->%v bits=%#x mode=%v: got (%#x,%v), ref (%#x,%v)",
				src, dst, bits, m, got, gotFl, want, wantFl)
		}
	}
}

// Exhaustively convert every binary16 bit pattern to binary32 in all four
// rounding modes and compare against the rational reference.
func TestExhaustiveBinary16ToBinary32VsRef(t *testing.T) {
	for bits := uint64(0); bits < 1<<16; bits++ {
		checkAgainstRef(t, floatconv.Binary32, floatconv.Binary16, bits)
	}
}

// Exhaustively convert every bfloat16 bit pattern to binary32.
func TestExhaustiveBFloat16ToBinary32VsRef(t *testing.T) {
	for bits := uint64(0); bits < 1<<16; bits++ {
		checkAgainstRef(t, floatconv.Binary32, floatconv.BFloat16, bits)
	}
}

// Narrowing binary32 -> binary16: sweep the exponent range around the
// binary16 normal/subnormal/overflow boundaries with many mantissas.
func TestBinary32ToBinary16BoundarySweepVsRef(t *testing.T) {
	lows := []uint64{0, 1, 0x7FFF, 0x2AAA}
	for expField := uint64(127 - 28); expField <= 127+19; expField++ {
		for hi := uint64(0); hi < 256; hi++ {
			for _, lo := range lows {
				mant := hi<<15 | lo
				bits := expField<<23 | mant
				checkAgainstRef(t, floatconv.Binary16, floatconv.Binary32, bits)
				checkAgainstRef(t, floatconv.Binary16, floatconv.Binary32, bits|1<<31)
			}
		}
	}
}

// Random narrowing conversions in both 16-bit flavors.
func TestRandomNarrowingVsRef(t *testing.T) {
	rng := rand.New(rand.NewSource(20260921))
	for i := 0; i < 60000; i++ {
		checkAgainstRef(t, floatconv.Binary16, floatconv.Binary32, uint64(rng.Uint32()))
	}
	for i := 0; i < 30000; i++ {
		checkAgainstRef(t, floatconv.BFloat16, floatconv.Binary32, uint64(rng.Uint32()))
	}
	for i := 0; i < 30000; i++ {
		checkAgainstRef(t, floatconv.Binary16, floatconv.Binary64, rng.Uint64())
	}
}

func TestSpecialValues(t *testing.T) {
	cases := []struct {
		name      string
		dst, src  floatconv.Format
		bits      uint64
		mode      floatconv.RoundingMode
		want      uint64
		wantFlags floatconv.Flags
	}{
		{"+zero preserved", floatconv.Binary32, floatconv.Binary16, 0x0000, floatconv.RoundNearestEven, 0x00000000, 0},
		{"-zero preserved", floatconv.Binary32, floatconv.Binary16, 0x8000, floatconv.RoundNearestEven, 0x80000000, 0},
		{"-zero narrowing", floatconv.Binary16, floatconv.Binary32, 0x80000000, floatconv.RoundTowardPositive, 0x8000, 0},
		{"+inf widen", floatconv.Binary32, floatconv.Binary16, 0x7C00, floatconv.RoundNearestEven, 0x7F800000, 0},
		{"-inf narrow", floatconv.Binary16, floatconv.Binary32, 0xFF800000, floatconv.RoundNearestEven, 0xFC00, 0},
		{"qnan widen keeps payload", floatconv.Binary32, floatconv.Binary16, 0x7E01, floatconv.RoundNearestEven, 0x7FC02000, 0},
		{"snan narrow quiets+invalid", floatconv.Binary16, floatconv.Binary32, 0x7F800001, floatconv.RoundNearestEven, 0x7E00, floatconv.FlagInvalid},
		{"snan payload truncated", floatconv.Binary16, floatconv.Binary32, 0x7FC00001, floatconv.RoundNearestEven, 0x7E00, 0},
		{"nan payload msb kept", floatconv.Binary16, floatconv.Binary32, 0x7FE00000, floatconv.RoundNearestEven, 0x7F00, 0},
		{"nan payload all kept", floatconv.Binary16, floatconv.Binary32, 0x7FFFFFFF, floatconv.RoundNearestEven, 0x7FFF, 0},
		{"nan sign preserved", floatconv.Binary16, floatconv.Binary32, 0xFFC00000, floatconv.RoundNearestEven, 0xFE00, 0},
	}
	for _, c := range cases {
		got, fl, err := floatconv.Convert(c.dst, c.src, c.bits, c.mode)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want || fl != c.wantFlags {
			t.Errorf("%s: got (%#x,%v), want (%#x,%v)", c.name, got, fl, c.want, c.wantFlags)
		}
	}
}

// Finite-only format (no Inf/NaN): the top exponent is an ordinary finite
// range, and overflowing values saturate to max finite.
func TestFiniteOnlyFormat(t *testing.T) {
	fin8 := floatconv.Format{Name: "fin8", ExpBits: 4, MantBits: 3, Bias: 7}
	if err := fin8.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// binary16 1.0 -> fin8: exact.
	got, fl, err := floatconv.Convert(fin8, floatconv.Binary16, 0x3C00, floatconv.RoundNearestEven)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0x38 || fl != 0 { // sign 0, exp field 7, mantissa 0
		t.Fatalf("1.0 -> fin8: got (%#x,%v)", got, fl)
	}
	// binary16 inf -> fin8 saturates to max finite with overflow|inexact.
	got, fl, _ = floatconv.Convert(fin8, floatconv.Binary16, 0x7C00, floatconv.RoundNearestEven)
	want := uint64(0x7F) // exp field 15, mantissa 7: (2^4-1)*2^(8-3) = 240
	if got != want || fl != floatconv.FlagOverflow|floatconv.FlagInexact {
		t.Fatalf("inf -> fin8: got (%#x,%v), want (%#x,overflow|inexact)", got, fl, want)
	}
	// Huge finite value saturates as well.
	got, fl, _ = floatconv.Convert(fin8, floatconv.Binary16, 0x7BFF, floatconv.RoundNearestEven)
	if got != want || fl != floatconv.FlagOverflow|floatconv.FlagInexact {
		t.Fatalf("65504 -> fin8: got (%#x,%v)", got, fl)
	}
	// NaN into a NaN-less format: max finite + invalid.
	got, fl, _ = floatconv.Convert(fin8, floatconv.Binary16, 0x7E00, floatconv.RoundNearestEven)
	if got != want || fl != floatconv.FlagInvalid {
		t.Fatalf("nan -> fin8: got (%#x,%v)", got, fl)
	}
	// Cross-check the whole 8-bit space against the reference.
	for bits := uint64(0); bits < 1<<16; bits++ {
		checkAgainstRef(t, fin8, floatconv.Binary16, bits)
	}
}
