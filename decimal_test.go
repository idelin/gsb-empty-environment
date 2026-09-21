package floatconv_test

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"testing"

	"floatconv"
	"floatconv/ref"
)

// TestDoubleRoundingCounterexample demonstrates, with an exact derivation,
// a decimal string whose correct binary16 value differs from the value
// obtained by first parsing to binary64 and then narrowing.
//
// Let x = 1 + 2^-11 + 2^-70.
//
// Direct binary16 rounding (nearest-even): the two candidates around x are
// 1 (0x3C00) and 1+2^-10 (0x3C01); their midpoint is m = 1+2^-11. Since
// x = m + 2^-70 > m, x rounds UP to 0x3C01.
//
// Via binary64: binary64 has p = 53, so near 1+2^-11 the spacing is 2^-52
// and m itself is exactly representable. The nearest binary64 values to x
// are m (distance 2^-70) and m+2^-52 (distance 2^-52-2^-70 > 2^-70), so
// float64(x) = m exactly. Narrowing m to binary16 hits the tie exactly and
// ties-to-even selects 0x3C00.
//
// Hence direct conversion yields 0x3C01 while the binary64-detour yields
// 0x3C00. The decimal string is exact: x = (2^70+2^59+1)*5^70 / 10^70.
func TestDoubleRoundingCounterexample(t *testing.T) {
	num := new(big.Int).Lsh(big.NewInt(1), 70)
	num.Add(num, new(big.Int).Lsh(big.NewInt(1), 59))
	num.Add(num, big.NewInt(1))
	num.Mul(num, new(big.Int).Exp(big.NewInt(5), big.NewInt(70), nil))
	digits := num.String()
	if len(digits) != 71 {
		t.Fatalf("expected 71 digits, got %d", len(digits))
	}
	s := digits[:1] + "." + digits[1:]

	// Sanity: the exact midpoint itself converts to 0x3C00 directly.
	midBits, midFl, err := floatconv.ParseDecimal(floatconv.Binary16, "1.00048828125", floatconv.RoundNearestEven)
	if err != nil {
		t.Fatal(err)
	}
	if midBits != 0x3C00 || midFl != floatconv.FlagInexact {
		t.Fatalf("midpoint direct: got (%#x,%v), want (0x3c00,inexact)", midBits, midFl)
	}

	// Direct decimal -> binary16 conversion rounds up to 0x3C01.
	direct, fl, err := floatconv.ParseDecimal(floatconv.Binary16, s, floatconv.RoundNearestEven)
	if err != nil {
		t.Fatal(err)
	}
	if direct != 0x3C01 || fl != floatconv.FlagInexact {
		t.Fatalf("direct: got (%#x,%v), want (0x3c01,inexact)", direct, fl)
	}

	// The binary64 detour: parse to float64 (host), then narrow.
	f64, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatal(err)
	}
	const midpointBits64 = 0x3FF0000000000000 | (1 << 41) // 1 + 2^-11
	if got := math.Float64bits(f64); got != midpointBits64 {
		t.Fatalf("float64(x) = %#x, want midpoint %#x", got, uint64(midpointBits64))
	}
	via64, fl64, err := floatconv.Convert(floatconv.Binary16, floatconv.Binary64, math.Float64bits(f64), floatconv.RoundNearestEven)
	if err != nil {
		t.Fatal(err)
	}
	if via64 != 0x3C00 || fl64 != floatconv.FlagInexact {
		t.Fatalf("via binary64: got (%#x,%v), want (0x3c00,inexact)", via64, fl64)
	}

	if direct == via64 {
		t.Fatalf("expected counterexample, both gave %#x", direct)
	}
	t.Logf("decimal %s", s)
	t.Logf("direct -> %#04x, via binary64 -> %#04x", direct, via64)
}

func TestParseDecimalSpecific(t *testing.T) {
	cases := []struct {
		s    string
		want uint64
		fl   floatconv.Flags
	}{
		{"0", 0x0000, 0},
		{"-0", 0x8000, 0},
		{"-0.0e5", 0x8000, 0},
		{"1", 0x3C00, 0},
		{"-2", 0xC000, 0},
		{"0.5", 0x3800, 0},
		{"65504", 0x7BFF, 0}, // max finite, exact
		{"65520", 0x7C00, floatconv.FlagOverflow | floatconv.FlagInexact}, // overflow midpoint
		{"65519", 0x7BFF, floatconv.FlagInexact},
		{"0.00006103515625", 0x0400, 0},                                                          // 2^-14 min normal, exact
		{"0.000000059604644775390625", 0x0001, 0},                                                // 2^-24 min subnormal, exact
		{"0.0000000298023223876953125", 0x0000, floatconv.FlagUnderflow | floatconv.FlagInexact}, // 2^-25 ties to zero
		{"1e-10", 0x0000, floatconv.FlagUnderflow | floatconv.FlagInexact},
		{"1e10", 0x7C00, floatconv.FlagOverflow | floatconv.FlagInexact},
		{"0.1", 0x2E66, floatconv.FlagInexact}, // 0.1 -> 0.0999755859375
		{"inf", 0x7C00, 0},
		{"-Infinity", 0xFC00, 0},
		{"nan", 0x7E00, 0},
		{"-nan", 0xFE00, 0},
	}
	for _, c := range cases {
		got, fl, err := floatconv.ParseDecimal(floatconv.Binary16, c.s, floatconv.RoundNearestEven)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", c.s, err)
		}
		if got != c.want || fl != c.fl {
			t.Errorf("ParseDecimal(%q) = (%#x,%v), want (%#x,%v)", c.s, got, fl, c.want, c.fl)
		}
	}
}

func TestParseDecimalErrors(t *testing.T) {
	for _, s := range []string{"", "abc", "1.2.3", "1e", "1e+", ".", "+", "1e99999999", "0x10"} {
		if _, _, err := floatconv.ParseDecimal(floatconv.Binary16, s, floatconv.RoundNearestEven); err == nil {
			t.Errorf("ParseDecimal(%q): expected error", s)
		}
	}
}

// Cross-check decimal conversion against the rational reference on random
// decimal strings, all formats and modes.
func TestParseDecimalVsRef(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	formats := []floatconv.Format{floatconv.Binary16, floatconv.BFloat16, floatconv.Binary32}
	for i := 0; i < 3000; i++ {
		s := randomDecimal(rng)
		for _, f := range formats {
			for _, m := range modes {
				got, gotFl, err := floatconv.ParseDecimal(f, s, m)
				if err != nil {
					t.Fatalf("ParseDecimal(%q): %v", s, err)
				}
				want, wantFl, err := ref.ParseDecimal(f, s, m)
				if err != nil {
					t.Fatalf("ref.ParseDecimal(%q): %v", s, err)
				}
				if got != want || gotFl != wantFl {
					t.Fatalf("decimal %q -> %v mode %v: got (%#x,%v), ref (%#x,%v)",
						s, f, m, got, gotFl, want, wantFl)
				}
			}
		}
	}
}

func randomDecimal(rng *rand.Rand) string {
	s := ""
	if rng.Intn(2) == 0 {
		s += "-"
	}
	n := 1 + rng.Intn(25)
	digits := ""
	for i := 0; i < n; i++ {
		digits += string(rune('0' + rng.Intn(10)))
	}
	if n > 1 && rng.Intn(2) == 0 {
		p := 1 + rng.Intn(n-1)
		digits = digits[:p] + "." + digits[p:]
	}
	s += digits
	if rng.Intn(2) == 0 {
		s += fmt.Sprintf("e%+d", rng.Intn(91)-45)
	}
	return s
}
