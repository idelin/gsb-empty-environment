package decimal128

import (
	"errors"
	"math/big"
	"testing"
)

func TestZeroArithmetic(t *testing.T) {
	zero := mustParse(t, "0.00")
	one := mustParse(t, "1.25")

	sum, err := Add(zero, one)
	if err != nil || sum.String() != "1.250000" {
		t.Fatalf("0 + x: %v %v", sum, err)
	}
	prod, err := Mul(zero, one)
	if err != nil || prod.String() != "0.0000" {
		t.Fatalf("0 * x: %v %v", prod, err)
	}
	if prod.Sign() != 0 {
		t.Fatalf("zero product must be unsigned zero")
	}
	// Zero divided by a non-zero value is zero at the inferred scale.
	q, err := Div(zero, one)
	if err != nil || q.Sign() != 0 {
		t.Fatalf("0 / x: %v %v", q, err)
	}
	// Remainder of zero is zero with the dividend sign convention moot.
	r, err := Mod(zero, one)
	if err != nil || r.Sign() != 0 {
		t.Fatalf("0 mod x: %v %v", r, err)
	}
	if _, _, err := QuoRem(one, zero); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("QuoRem by zero: %v", err)
	}
}

func TestNegationRoundTrip(t *testing.T) {
	// The most negative representable coefficient must negate cleanly.
	maxStr := "99999999999999999999999999999999999999"
	d := mustParse(t, "-"+maxStr)
	if d.String() != "-"+maxStr {
		t.Fatalf("negative extreme = %s", d.String())
	}
	if d.Neg().String() != maxStr {
		t.Fatalf("negation round trip = %s", d.Neg().String())
	}
	if Cmp(d.Neg().Neg(), d) != 0 {
		t.Fatalf("double negation must be identity")
	}
}

func TestScaleAlignmentRounding(t *testing.T) {
	// Adding a fine-scaled value to a coarse one forces lossy alignment.
	a := mustParse(t, "1.00")
	b := mustParse(t, "0.005")
	for _, mode := range []RoundingMode{HalfAwayFromZero, HalfToEven} {
		ctx := Context{Mode: mode}
		sum, err := ctx.Add(a, b)
		if err != nil {
			t.Fatal(err)
		}
		want := roundRat(new(big.Rat).Add(ratOf(a), ratOf(b)), sum.Scale(), mode)
		if signedBig(sum).Cmp(want) != 0 {
			t.Fatalf("aligned add mode %d: got %s want %s", mode, sum.String(), want.String())
		}
	}
}

func TestRescaleModes(t *testing.T) {
	d := mustParse(t, "2.5")
	down, err := (Context{Mode: HalfAwayFromZero}).Rescale(d, 0)
	if err != nil || down.String() != "3" {
		t.Fatalf("rescale away: %s %v", down.String(), err)
	}
	even, err := (Context{Mode: HalfToEven}).Rescale(d, 0)
	if err != nil || even.String() != "2" {
		t.Fatalf("rescale even: %s %v", even.String(), err)
	}
	// Scale-up is lossless and padded.
	up, err := DefaultContext.Rescale(d, 4)
	if err != nil || up.String() != "2.5000" {
		t.Fatalf("rescale up: %s %v", up.String(), err)
	}
}

func TestParseScientificRoundTrip(t *testing.T) {
	// Every parsed value must render back to an equal rational via either
	// fixed or scientific notation.
	samples := []string{
		"12345678901234567890.12345",
		"0.00000000123456789",
		"9.999999999999999999e20",
		"-1.234567890123456789e-10",
		"1E0", "9e+37", "1e-38",
	}
	for _, s := range samples {
		d, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			t.Fatalf("rat parse %q", s)
		}
		expectRat(t, "roundtrip "+s, d, r)
		// Scientific form parses back to the identical decimal value.
		d2, err := Parse(d.StringScientific())
		if err != nil {
			t.Fatalf("re-parse scientific %q: %v", d.StringScientific(), err)
		}
		if Cmp(d, d2) != 0 {
			t.Fatalf("scientific round trip %q vs %q", d.String(), d2.String())
		}
	}
}

func TestParseOverflowRejected(t *testing.T) {
	// 10^38 needs 39 integer digits and must be rejected.
	if _, err := Parse("100000000000000000000000000000000000000"); !errors.Is(err, ErrOverflow) {
		t.Fatalf("10^38 parse: %v", err)
	}
	// Exponent pushing a 38-digit integer past the bound must fail too.
	if _, err := Parse("99999999999999999999999999999999999999e1"); !errors.Is(err, ErrOverflow) {
		t.Fatalf("scaled extreme parse: %v", err)
	}
}

func TestDivisionOverflowAndExtremes(t *testing.T) {
	// max / a fraction whose inferred quotient exceeds 38 integer digits.
	maxPos := mustParse(t, "99999999999999999999999999999999999999")
	tiny := MustNew(0, 1, 38, 38) // 10^-38
	if _, err := Div(maxPos, tiny); !errors.Is(err, ErrOverflow) {
		t.Fatalf("extreme division expected overflow, got %v", err)
	}

	// max / max is exactly 1 regardless of sign combination.
	q, err := Div(maxPos, maxPos)
	if err != nil {
		t.Fatal(err)
	}
	expectRat(t, "max/max", q, big.NewRat(1, 1))
	maxNeg := mustParse(t, "-99999999999999999999999999999999999999")
	q2, err := Div(maxNeg, maxPos)
	if err != nil {
		t.Fatal(err)
	}
	expectRat(t, "-max/max", q2, big.NewRat(-1, 1))

	// Dividing by a power of ten exercises the k<0 (divisor-widening) path.
	num := mustParse(t, "12345678901234567890123456789012345678")
	den := mustParse(t, "1000")
	q3, err := Div(num, den)
	if err != nil {
		t.Fatal(err)
	}
	expectRat(t, "power-of-ten div", q3,
		new(big.Rat).Quo(ratOf(num), ratOf(den)))
}
