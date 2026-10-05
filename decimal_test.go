package rowconv

import (
	"math/big"
	"testing"
)

func mustBig(t *testing.T, s string) *big.Int {
	t.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad big.Int literal %q", s)
	}
	return n
}

func TestRescaleDecimalTiesToEvenNegative(t *testing.T) {
	cases := []struct {
		coeff              string
		fromScale, toScale int
		want               string
	}{
		{"-25", 1, 0, "-2"}, // -2.5 -> tie -> -2 (even)
		{"-35", 1, 0, "-4"}, // -3.5 -> tie -> -4 (even)
		{"-15", 1, 0, "-2"}, // -1.5 -> tie -> -2 (even)
		{"-5", 1, 0, "0"},   // -0.5 -> tie -> 0 (even)
		{"-24", 1, 0, "-2"}, // -2.4 -> -2
		{"-26", 1, 0, "-3"}, // -2.6 -> -3
		{"25", 1, 0, "2"},   // 2.5 -> tie -> 2
		{"35", 1, 0, "4"},   // 3.5 -> tie -> 4
		{"125", 2, 1, "12"}, // 1.25 -> tie -> 1.2 (12 even)
		{"135", 2, 1, "14"}, // 1.35 -> tie -> 1.4 (14 even)
		{"-125", 2, 1, "-12"},
		{"-135", 2, 1, "-14"},
		{"-1234550", 4, 2, "-12346"}, // -123.455 -> tie -> -123.46
	}
	for _, c := range cases {
		got, err := RescaleDecimal(mustBig(t, c.coeff), c.fromScale, c.toScale, TiesToEven)
		if err != nil {
			t.Fatalf("RescaleDecimal(%s, %d, %d): %v", c.coeff, c.fromScale, c.toScale, err)
		}
		if got.String() != c.want {
			t.Errorf("RescaleDecimal(%s, %d, %d) = %s, want %s",
				c.coeff, c.fromScale, c.toScale, got, c.want)
		}
	}
}

func TestRescaleDecimalReject(t *testing.T) {
	// Exact divisions succeed in reject mode.
	got, err := RescaleDecimal(mustBig(t, "250"), 2, 1, Reject)
	if err != nil || got.String() != "25" {
		t.Errorf("RescaleDecimal(250, 2, 1) = %v, %v; want 25, nil", got, err)
	}
	got, err = RescaleDecimal(mustBig(t, "-1000"), 3, 0, Reject)
	if err != nil || got.String() != "-1" {
		t.Errorf("RescaleDecimal(-1000, 3, 0) = %v, %v; want -1, nil", got, err)
	}
	// Inexact divisions are rejected.
	if _, err := RescaleDecimal(mustBig(t, "25"), 1, 0, Reject); err == nil {
		t.Error("RescaleDecimal(25, 1, 0, Reject): expected error")
	}
	if _, err := RescaleDecimal(mustBig(t, "-1"), 0, -1, Reject); err == nil {
		t.Error("RescaleDecimal with negative scale: expected error")
	}
	// Upscaling is always exact.
	got, err = RescaleDecimal(mustBig(t, "-5"), 0, 3, Reject)
	if err != nil || got.String() != "-5000" {
		t.Errorf("RescaleDecimal(-5, 0, 3) = %v, %v; want -5000, nil", got, err)
	}
}

// ratRound independently recomputes rescaling with rational arithmetic:
// value = coeff * 10^(toScale-fromScale) as an exact fraction, then rounds
// to an integer with ties-to-even. It also reports exactness.
func ratRound(coeff *big.Int, fromScale, toScale int) (q *big.Int, exact bool) {
	r := new(big.Rat).SetInt(coeff)
	diff := fromScale - toScale
	abs := diff
	if abs < 0 {
		abs = -abs
	}
	pow := new(big.Rat).SetInt(pow10(abs))
	if diff > 0 {
		r.Quo(r, pow)
	} else {
		r.Mul(r, pow)
	}
	num, den := r.Num(), r.Denom()
	q = new(big.Int)
	rem := new(big.Int)
	q.QuoRem(num, den, rem) // toward zero
	if rem.Sign() == 0 {
		return q, true
	}
	twice := new(big.Int).Lsh(new(big.Int).Abs(rem), 1)
	cmp := twice.Cmp(den)
	odd := new(big.Int).And(new(big.Int).Abs(q), big.NewInt(1)).Sign() == 1
	if cmp > 0 || (cmp == 0 && odd) {
		if num.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return q, false
}

// TestRescaleDecimalMatchesRational cross-checks RescaleDecimal against an
// independent big.Rat computation for both rounding modes.
func TestRescaleDecimalMatchesRational(t *testing.T) {
	coeffs := []string{
		"0", "1", "-1", "7", "-7", "25", "-25", "999", "-999",
		"123456789012345678901234567890", "-123456789012345678901234567890",
		"500000000000000000000000000005", "-500000000000000000000000000005",
	}
	scales := []int{0, 1, 2, 5, 9}
	for _, cs := range coeffs {
		coeff := mustBig(t, cs)
		for _, from := range scales {
			for _, to := range scales {
				want, exact := ratRound(coeff, from, to)
				got, err := RescaleDecimal(coeff, from, to, TiesToEven)
				if err != nil {
					t.Fatalf("ties-to-even (%s, %d, %d): %v", cs, from, to, err)
				}
				if got.Cmp(want) != 0 {
					t.Errorf("ties-to-even (%s, %d, %d) = %s, rat says %s",
						cs, from, to, got, want)
				}
				_, rejErr := RescaleDecimal(coeff, from, to, Reject)
				if exact && rejErr != nil {
					t.Errorf("reject (%s, %d, %d): exact but got error %v", cs, from, to, rejErr)
				}
				if !exact && rejErr == nil {
					t.Errorf("reject (%s, %d, %d): inexact but got no error", cs, from, to)
				}
			}
		}
	}
}
