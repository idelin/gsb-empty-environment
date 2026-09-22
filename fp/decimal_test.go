package fp

import (
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"testing"
)

// TestDoubleRoundingFailure gives the genuine disagreement required by the
// task: a decimal value just above the binary16 midpoint 1+2^-11 that binary64
// rounds down to the midpoint itself; binary16 then rounds that exact midpoint
// by ties-to-even to 1.0, while direct conversion goes up to 1+2^-10.
func TestDoubleRoundingCounterexample(t *testing.T) {
	// mid = 1 + 2^-11 = 2049/2048.
	// binary64 numbers adjacent to mid are m (exactly representable) and
	// m + 2^-63 (the spacing at this magnitude is 2^-63). Choose a decimal
	// delta in (0, 2^-64), i.e. between mid and the half-way point to the
	// next binary64 number, so binary64 rounds DOWN to mid.
	//
	// dec = mid + 2^-70, written as an exact decimal:
	//   2^-70 = 8470329472543003390683225006796419620513916015625 / 10^51...
	// To keep a readable literal, take 1.00048828125000000000008272
	const dec = "1.00048828125000000000008272"

	x, err := ParseDecimal(dec)
	if err != nil {
		t.Fatal(err)
	}
	mid := new(big.Rat).SetFrac(big.NewInt(2049), big.NewInt(2048))
	got := new(big.Rat).SetFrac(x.Num, x.Den)
	if got.Cmp(mid) <= 0 {
		t.Fatal("decimal must be strictly above the midpoint")
	}
	// delta must be below half the binary64 gap at mid, which is 2^-64.
	delta := new(big.Rat).Sub(got, mid)
	halfGap64 := new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Lsh(big.NewInt(1), 64))
	if delta.Cmp(halfGap64) >= 0 {
		t.Fatalf("delta %v must be below 2^-64 %v", delta, halfGap64)
	}

	// Direct: value is strictly above the midpoint, rounds to 1+2^-10.
	direct, err := ConvertString(Binary16, dec, ToNearestEven)
	if err != nil {
		t.Fatal(err)
	}
	if direct.Bits != 0x3c01 {
		t.Fatalf("direct = %04x, want 3c01 (1 + 2^-10)", direct.Bits)
	}

	// Via binary64: the value rounds down to the exact midpoint m.
	fv2, _ := strconv.ParseFloat(dec, 64)
	f64 := math.Float64bits(fv2)
	if want := math.Float64bits(1.0 + math.Ldexp(1, -11)); f64 != want {
		t.Fatalf("binary64 = %016x want midpoint %016x", f64, want)
	}
	// binary16 ties-to-even at the midpoint: candidates have unit
	// significands 2048 (even) and 2049 (odd), so it rounds DOWN to 1.0.
	via := Convert(Binary16, Binary64, f64, ToNearestEven)
	if via.Bits != 0x3c00 {
		t.Fatalf("via binary64 = %04x, want 3c00", via.Bits)
	}
	if direct.Bits == via.Bits {
		t.Fatal("expected direct and via-binary64 to differ")
	}
}

// TestKnownDecimals checks direct decimal conversion for values whose rounding
// is textbook, exercising every target format.
func TestKnownDecimals(t *testing.T) {
	cases := []struct {
		s    string
		f16  uint16
		bf16 uint16
		f32  uint32
		fl   Flags
	}{
		{"0", 0x0000, 0x0000, 0x00000000, 0},
		{"-0", 0x8000, 0x8000, 0x80000000, 0},
		{"1", 0x3c00, 0x3f80, 0x3f800000, 0},
		{"-1", 0xbc00, 0xbf80, 0xbf800000, 0},
		{"1.5", 0x3e00, 0x3fc0, 0x3fc00000, 0},
		{"0.1", 0x2e66, 0x3dcd, 0x3dcccccd, FlagInexact},
		{"65504", 0x7bff, 0x4780, 0x477fe000, 0},
		{"1e-10", 0x0000, 0x2edc, 0x2edbe6ff, FlagInexact},
	}
	for _, c := range cases {
		r16, err := ConvertString(Binary16, c.s, ToNearestEven)
		if err != nil {
			t.Fatal(err)
		}
		if r16.Bits != uint64(c.f16) {
			t.Errorf("%s -> f16 %04x want %04x", c.s, r16.Bits, c.f16)
		}
		rb, err := ConvertString(BFloat16, c.s, ToNearestEven)
		if err != nil {
			t.Fatal(err)
		}
		if rb.Bits != uint64(c.bf16) {
			t.Errorf("%s -> bf16 %04x want %04x", c.s, rb.Bits, c.bf16)
		}
		r32, err := ConvertString(Binary32, c.s, ToNearestEven)
		if err != nil {
			t.Fatal(err)
		}
		if r32.Bits != uint64(c.f32) {
			t.Errorf("%s -> f32 %08x want %08x", c.s, r32.Bits, c.f32)
		}
		if r32.Flags != c.fl {
			t.Errorf("%s f32 flags %v want %v", c.s, r32.Flags, c.fl)
		}
		// Every decimal result must equal the independent reference.
		ref := ReferenceConvert(Binary32, xMust(c.s), ToNearestEven)
		if ref.Bits != r32.Bits || ref.Flags != r32.Flags {
			t.Errorf("%s reference mismatch got %08x/%v ref %08x/%v", c.s, r32.Bits, r32.Flags, ref.Bits, ref.Flags)
		}
	}
}

func xMust(s string) Exact {
	x, err := ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return x
}

func TestParseSpecial(t *testing.T) {
	for _, s := range []string{"inf", "Infinity", "-inf", "+infinity"} {
		x, err := ParseDecimal(s)
		if err != nil || x.Kind != KindInf {
			t.Fatalf("%s: %+v err=%v", s, x, err)
		}
	}
	for _, s := range []string{"nan", "NaN", "nan(5)", "-nan(16)"} {
		x, err := ParseDecimal(s)
		if err != nil || x.Kind != KindNaN || !x.NaN.Quiet {
			t.Fatalf("%s: %+v err=%v", s, x, err)
		}
	}
	for _, s := range []string{"", "  ", "1.2.3", "1e", "e1", "0x1p4", "nan(-1)"} {
		if _, err := ParseDecimal(s); err == nil {
			t.Fatalf("expected error for %q", s)
		}
	}
}

// TestRandomDecimalsVsReference fuzzes decimal literals spanning the tiny,
// normal and overflow ranges for every format and mode, cross-checking the
// direct parser against the independent big.Rat reference.
func TestRandomDecimalsVsReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	magnitudes := []int{-40, -30, -25, -24, -20, -10, -7, -1, 0, 1, 5, 10, 20, 30, 38, 39, 40, 100}
	for _, f := range []*Format{Binary16, BFloat16, Binary32} {
		for _, m := range magnitudes {
			for iter := 0; iter < 40; iter++ {
				s := randomDecimal(rng, m)
				x, err := ParseDecimal(s)
				if err != nil {
					t.Fatalf("%q: %v", s, err)
				}
				for _, mode := range allModes() {
					got := ConvertValue(f, x, mode)
					ref := ReferenceConvert(f, x, mode)
					if got.Bits != ref.Bits || got.Flags != ref.Flags {
						t.Fatalf("%s -> %s mode=%d got=%0*x/%v ref=%0*x/%v",
							s, f.Name(), mode,
							f.Width()*2, got.Bits, got.Flags,
							f.Width()*2, ref.Bits, ref.Flags)
					}
				}
			}
		}
	}
}

func randomDecimal(rng *rand.Rand, magExp int) string {
	sign := ""
	if rng.Intn(2) == 0 {
		sign = "-"
	}
	digits := 1 + rng.Intn(24)
	var b []byte
	for i := 0; i < digits; i++ {
		if i == 0 {
			b = append(b, byte('1'+rng.Intn(9)))
		} else {
			b = append(b, byte('0'+rng.Intn(10)))
		}
	}
	dot := 1 + rng.Intn(digits)
	var mant string
	if dot >= digits {
		mant = string(b)
	} else {
		mant = string(b[:dot]) + "." + string(b[dot:])
	}
	exp := magExp - dot + rng.Intn(3) - 1
	return sign + mant + "e" + itoa(exp)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
