package decimal128

import (
	"errors"
	"math/big"
	"testing"
)

const max38 = "99999999999999999999999999999999999999" // 10^38 - 1

func TestExtremeValues(t *testing.T) {
	maxPos := mustParse(t, max38)
	maxNeg := mustParse(t, "-"+max38)

	if hi, lo := maxPos.Coeff(); hi < 0 {
		t.Fatalf("max positive has negative high limb")
	} else if (u128{hi: uint64(hi), lo: lo}).cmp(pow10u128[38]) >= 0 {
		t.Fatalf("max positive not below 10^38")
	}
	if maxNeg.Sign() >= 0 || maxPos.Sign() <= 0 {
		t.Fatalf("sign wrong at extremes")
	}

	// max at scale 1 stays inside range and round-trips through arithmetic
	// with a narrow operand whose inferred result type fits in 38 digits.
	// A 38-digit coefficient at scale 1 needs a narrow (precision 1, scale 0)
	// multiplier so the inferred product type stays within 38 digits.
	maxS1 := MustNew(maxPos.coeff.hi, maxPos.coeff.lo, 38, 1)
	one := MustNew(0, 1, 1, 0)
	prod, err := Mul(maxS1, one)
	if err != nil {
		t.Fatalf("max(scale1)*1: %v", err)
	}
	expectRat(t, "max(scale1)*1", prod, ratOf(maxS1))

	// (-max) / max = -1 exactly.
	quot, err := Div(maxNeg, maxPos)
	if err != nil {
		t.Fatalf("extreme div: %v", err)
	}
	if quot.Sign() >= 0 {
		t.Fatalf("expected negative quotient, got %s", quot.String())
	}
	expectRat(t, "extreme div", quot, big.NewRat(-1, 1))

	// max / 1 with full result type: must preserve all 38 digits.
	q2, err := Div(maxPos, one)
	if err != nil {
		t.Fatalf("max/1: %v", err)
	}
	expectRat(t, "max/1", q2, ratOf(maxPos))

	// max + 1 overflows.
	if _, err := Add(maxPos, one); !errors.Is(err, ErrOverflow) {
		t.Fatalf("max+1 expected ErrOverflow, got %v", err)
	}
	// max * max overflows.
	if _, err := Mul(maxPos, maxPos); !errors.Is(err, ErrOverflow) {
		t.Fatalf("max*max expected ErrOverflow, got %v", err)
	}
	// max + max overflows.
	if _, err := Add(maxPos, maxPos); !errors.Is(err, ErrOverflow) {
		t.Fatalf("max+max expected ErrOverflow, got %v", err)
	}
}

func TestCarryBorrowAcross64Bits(t *testing.T) {
	// 2^64 - 1 plus 2 must cross the 64-bit boundary.
	a := mustParse(t, "18446744073709551615") // 2^64 - 1
	b := mustParse(t, "2")
	sum, err := Add(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if sum.String() != "18446744073709551617.000000" {
		t.Fatalf("carry add = %s", sum.String())
	}
	expectRat(t, "carry", sum, new(big.Rat).Add(ratOf(a), ratOf(b)))

	// 2^64 minus 1 exercises subtraction borrow between limbs.
	base := mustParse(t, "18446744073709551616") // 2^64
	one := mustParse(t, "1")
	diff, err := Sub(base, one)
	if err != nil {
		t.Fatal(err)
	}
	if diff.String() != "18446744073709551615.000000" {
		t.Fatalf("borrow sub = %s", diff.String())
	}

	// Two 20-digit factors force the internal 256-bit wide product.
	x := mustParse(t, "12345678901234567890")
	// A 10-digit factor keeps the rounded result inside 38 digits while the
	// raw 40-limb product still travels through the 256-bit accumulator.
	y := mustParse(t, "9876543210")
	prod, err := Mul(x, y)
	if err != nil {
		t.Fatal(err)
	}
	expectRat(t, "wide mul", prod, new(big.Rat).Mul(ratOf(x), ratOf(y)))

	// The full 40-digit product must be rejected as out of range.
	yBig := mustParse(t, "98765432109876543210")
	if _, err := Mul(x, yBig); !errors.Is(err, ErrOverflow) {
		t.Fatalf("40-digit product expected ErrOverflow, got %v", err)
	}

	// Long division whose numerator lives above 128 bits after scaling.
	num := mustParse(t, "100000000000000000000") // 10^20
	den := mustParse(t, "3")
	q, err := Div(num, den)
	if err != nil {
		t.Fatal(err)
	}
	expectRat(t, "wide div", q, new(big.Rat).Quo(ratOf(num), ratOf(den)))
}

func TestModSemantics(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"10", "3", "1"},
		{"-10", "3", "-1"},
		{"10", "-3", "1"},
		{"-10", "-3", "-1"},
		{"7.5", "2", "1.5"},
		{"-7.5", "2", "-1.5"},
		{"0.55", "0.1", "0.05"},
		{"18446744073709551617", "18446744073709551615", "2"},
		{"-18446744073709551617", "18446744073709551615", "-2"},
	}
	for _, tc := range cases {
		a, b := mustParse(t, tc.a), mustParse(t, tc.b)
		rem, err := Mod(a, b)
		if err != nil {
			t.Fatalf("mod %s %% %s: %v", tc.a, tc.b, err)
		}
		if rem.String() != tc.want {
			t.Fatalf("%s mod %s = %s, want %s", tc.a, tc.b, rem.String(), tc.want)
		}
		q, r, err := QuoRem(a, b)
		if err != nil {
			t.Fatal(err)
		}
		// Identity a = q*b + r checked without extra rounding: align all
		// values to the common scale used by QuoRem and compare rationals.
		if Cmp(r, rem) != 0 {
			t.Fatalf("QuoRem remainder differs from Mod: %s vs %s", r.String(), rem.String())
		}
		wantRat := new(big.Rat).Add(
			new(big.Rat).Mul(ratOf(q), ratOf(b)),
			ratOf(r))
		if wantRat.Cmp(ratOf(a)) != 0 {
			t.Fatalf("quo/rem identity failed for %s, %s", tc.a, tc.b)
		}
		// Quotient truncates toward zero: |q*b| <= |a|.
		if Cmp(mustMul(t, q, b).Abs(), a.Abs()) > 0 {
			t.Fatalf("quotient not truncated toward zero")
		}
	}

	if _, err := Mod(mustParse(t, "1"), mustParse(t, "0")); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("mod by zero: %v", err)
	}
	if _, err := Div(mustParse(t, "1"), mustParse(t, "0")); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("div by zero: %v", err)
	}
}

func mustMul(t *testing.T, a, b Decimal128) Decimal128 {
	t.Helper()
	d, err := Mul(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestHalfRoundingDivision(t *testing.T) {
	cases := []struct {
		a, b       string
		scale      int
		away, even string
	}{
		{"1", "8", 2, "0.13", "0.12"}, // 0.125 tie, 12 even vs 13
		{"-1", "8", 2, "-0.13", "-0.12"},
		{"5", "8", 2, "0.63", "0.62"}, // 0.625 tie, 12 even
		{"-5", "8", 2, "-0.63", "-0.62"},
		{"7", "8", 2, "0.88", "0.88"}, // 0.875 -> nearest 0.88 either mode
		{"1", "3", 6, "0.333333", "0.333333"},
	}
	for _, tc := range cases {
		a, b := mustParse(t, tc.a), mustParse(t, tc.b)
		// Force the target scale by dividing into a rescaled dividend.
		ctxAway := Context{Mode: HalfAwayFromZero}
		ctxEven := Context{Mode: HalfToEven}
		qa, err := ctxAway.Div(a, b)
		if err != nil {
			t.Fatal(err)
		}
		qe, err := ctxEven.Div(a, b)
		if err != nil {
			t.Fatal(err)
		}
		// The inferred type may carry more digits; rescale both to tc.scale.
		ra, err := ctxAway.Rescale(qa, tc.scale)
		if err != nil {
			t.Fatal(err)
		}
		re, err := ctxEven.Rescale(qe, tc.scale)
		if err != nil {
			t.Fatal(err)
		}
		if ra.String() != tc.away {
			t.Fatalf("away %s/%s = %s, want %s", tc.a, tc.b, ra.String(), tc.away)
		}
		if re.String() != tc.even {
			t.Fatalf("even %s/%s = %s, want %s", tc.a, tc.b, re.String(), tc.even)
		}
		// Cross-check against big.Rat with the same mode.
		wantAway := roundRat(new(big.Rat).Quo(ratOf(a), ratOf(b)), tc.scale, HalfAwayFromZero)
		wantEven := roundRat(new(big.Rat).Quo(ratOf(a), ratOf(b)), tc.scale, HalfToEven)
		if signedBig(ra).Cmp(wantAway) != 0 || signedBig(re).Cmp(wantEven) != 0 {
			t.Fatalf("rat mismatch at %s/%s scale %d", tc.a, tc.b, tc.scale)
		}
	}
}

func TestCmpAndSign(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.5", "1.50", 0},
		{"-0.00", "0.0", 0},
		{"1.000", "0.999", 1},
		{"-1.000", "-0.999", -1},
		{"10", "9.9999", 1},
		{"0.0001", "0.00009", 1},
	}
	for _, tc := range cases {
		if got := Cmp(mustParse(t, tc.a), mustParse(t, tc.b)); got != tc.want {
			t.Fatalf("Cmp(%s,%s)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
	d := mustParse(t, "-3.14")
	if d.Abs().String() != "3.14" || d.Neg().String() != "3.14" {
		t.Fatalf("abs/neg wrong: %s %s", d.Abs().String(), d.Neg().String())
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(0, 1, 0, 0); !errors.Is(err, ErrInvalidPrecision) {
		t.Fatalf("precision 0: %v", err)
	}
	if _, err := New(0, 1, 39, 0); !errors.Is(err, ErrInvalidPrecision) {
		t.Fatalf("precision 39: %v", err)
	}
	if _, err := New(0, 100, 5, 6); !errors.Is(err, ErrInvalidPrecision) {
		t.Fatalf("scale>precision: %v", err)
	}
	// Coefficient 10^p is out of range for DECIMAL(p).
	if _, err := New(0, 1000, 3, 0); !errors.Is(err, ErrOverflow) {
		t.Fatalf("coefficient overflow: %v", err)
	}
	d, err := New(0, 123, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	if d.String() != "1.23" || d.Precision() != 5 {
		t.Fatalf("New round trip wrong: %s p=%d", d.String(), d.Precision())
	}
}
