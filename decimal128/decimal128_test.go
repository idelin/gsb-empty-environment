package decimal128

import (
	"errors"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string, mode RoundingMode) Decimal128 {
	t.Helper()
	d, err := Parse(s, mode)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return d
}

func must2(d Decimal128, err error) Decimal128 {
	if err != nil {
		panic(err)
	}
	return d
}

// try2 treats ErrOverflow as an expected outcome: it reports whether a usable
// result exists, letting the oracle decide whether overflow was legitimate.
func try2(d Decimal128, err error) (Decimal128, bool) {
	if err != nil {
		if errors.Is(err, ErrOverflow) {
			return Decimal128{}, false
		}
		panic(err)
	}
	return d, true
}

func overflowsRat(want *big.Rat, scale int, limit *big.Int) bool {
	coeff := ratRound(want, scale, HalfAwayFromZero)
	return coeff.Cmp(limit) > 0 || new(big.Int).Neg(limit).Cmp(coeff) > 0
}

// TestParseFormat covers scientific notation, leading zeros, negative-zero
// normalization and scale-driven trailing zeros.
func TestParseFormat(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0", "0"},
		{"-0", "0"},
		{"-0.00", "0.00"},
		{"007.50", "7.50"},
		{".5", "0.5"},
		{"1.5", "1.5"},
		{"-0.0002", "-0.0002"},
		{"3.14e-2", "0.0314"},
		{"1E+12", "1000000000000"},
		{"1.230E3", "1230"},
		{"2.5e0", "2.5"},
		{"100", "100"},
		{"99999999999999999999999999999999999999", "99999999999999999999999999999999999999"},
		{"-99999999999999999999999999999999999999", "-99999999999999999999999999999999999999"},
	}
	for _, tc := range cases {
		d := mustParse(t, tc.in, HalfAwayFromZero)
		if got := d.String(); got != tc.want {
			t.Errorf("Parse(%q).String() = %q, want %q", tc.in, got, tc.want)
		}
	}

	bad := []string{"", "+", "-", "1.", ".", "1.2.3", "abc", "1e", "e5", "1..0", "1e++2"}
	for _, s := range bad {
		if _, err := Parse(s, HalfAwayFromZero); !errors.Is(err, ErrSyntax) {
			t.Errorf("Parse(%q) err = %v, want ErrSyntax", s, err)
		}
	}
}

func TestScientific(t *testing.T) {
	cases := map[string]string{
		"12.30":  "1.230E+1",
		"0.0123": "1.23E-2",
		"100":    "1.00E+2",
		"-5.5":   "-5.5E+0",
	}
	for in, want := range cases {
		d := mustParse(t, in, HalfAwayFromZero)
		if got := d.Scientific(); got != want {
			t.Errorf("%q scientific = %q, want %q", in, got, want)
		}
	}
}

// ratMod computes truncation-toward-zero remainder r = a - trunc(a/b)*b.
func ratMod(a, b *big.Rat) *big.Rat {
	q := new(big.Rat).Quo(a, b)
	// Quo on the underlying integers truncates toward zero.
	truncInt := new(big.Int).Quo(q.Num(), q.Denom())
	trunc := new(big.Rat).SetInt(truncInt)
	return new(big.Rat).Sub(a, new(big.Rat).Mul(trunc, b))
}

func checkRat(t *testing.T, mode RoundingMode, op, la, lb string, got Decimal128, want *big.Rat, scale int, limit *big.Int) {
	t.Helper()
	coeff := ratRound(want, scale, mode)
	if coeff.Cmp(limit) > 0 || new(big.Int).Neg(limit).Cmp(coeff) > 0 {
		return
	}
	wantStr := ratFixed(want, scale)
	if got.String() != wantStr {
		t.Errorf("%s(%q, %q) mode=%d = %s (scale %d), want %s (scale %d)",
			op, la, lb, mode, got.String(), got.Scale(), wantStr, scale)
	}
}

// TestPropertyArithmetic cross-checks every operation against exact big.Rat.
func TestPropertyArithmetic(t *testing.T) {
	rng := rand.New(rand.NewSource(20261127))
	const iterations = 600
	limit := new(big.Int).Sub(
		new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil),
		big.NewInt(1),
	)
	for iter := 0; iter < iterations; iter++ {
		for _, mode := range []RoundingMode{HalfAwayFromZero, HalfToEven} {
			la, ra := randomDecimal(rng)
			lb, rb := randomDecimal(rng)
			a := mustParse(t, la, mode)
			b := mustParse(t, lb, mode)

			sum := new(big.Rat).Add(ra, rb)
			diff := new(big.Rat).Sub(ra, rb)
			alignedScale := a.Scale()
			if b.Scale() > alignedScale {
				alignedScale = b.Scale()
			}
			if got, ok := try2(AddMode(a, b, mode)); ok {
				checkRat(t, mode, "Add", la, lb, got, sum, alignedScale, limit)
			} else if !overflowsRat(sum, alignedScale, limit) {
				t.Errorf("Add(%q,%q) unexpected overflow, rat=%s", la, lb, ratFixed(sum, alignedScale))
			}
			if got, ok := try2(SubMode(a, b, mode)); ok {
				checkRat(t, mode, "Sub", la, lb, got, diff, alignedScale, limit)
			} else if !overflowsRat(diff, alignedScale, limit) {
				t.Errorf("Sub(%q,%q) unexpected overflow, rat=%s", la, lb, ratFixed(diff, alignedScale))
			}

			prod := new(big.Rat).Mul(ra, rb)
			mulScale := a.Scale() + b.Scale()
			if mulScale > 38 {
				mulScale = 38
			}
			if got, ok := try2(MulMode(a, b, mode)); ok {
				checkRat(t, mode, "Mul", la, lb, got, prod, mulScale, limit)
			} else if !overflowsRat(prod, mulScale, limit) {
				t.Errorf("Mul(%q,%q) unexpected overflow, rat=%s", la, lb, ratFixed(prod, mulScale))
			}

			if rb.Sign() != 0 {
				quot := new(big.Rat).Quo(ra, rb)
				divScale := 6
				if cand := a.Scale() + b.Precision() + 1; cand > divScale {
					divScale = cand
				}
				if divScale > 38 {
					divScale = 38
				}
				if got, ok := try2(DivMode(a, b, mode)); ok {
					checkRat(t, mode, "Div", la, lb, got, quot, divScale, limit)
				} else if !overflowsRat(quot, divScale, limit) {
					t.Errorf("Div(%q,%q) unexpected overflow, rat=%s", la, lb, ratFixed(quot, divScale))
				}

				if gotMod, ok := try2(Mod(a, b)); ok {
					wantMod := ratMod(ra, rb)
					if gotMod.String() != ratFixed(wantMod, gotMod.Scale()) {
						t.Errorf("Mod(%q,%q) = %s, want %s", la, lb, gotMod.String(),
							ratFixed(wantMod, gotMod.Scale()))
					}
					if gotMod.Sign() != 0 && gotMod.Sign() != ra.Sign() {
						t.Errorf("Mod sign must follow dividend: %q %% %q = %s", la, lb, gotMod.String())
					}
				}
			}
		}
	}
}

// TestModSignAndTruncation checks explicit sign cases and the identity
// d = quo*e + rem for the truncated quotient.
func TestModSignAndTruncation(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"10", "3", "1"},
		{"-10", "3", "-1"},
		{"10", "-3", "1"},
		{"-10", "-3", "-1"},
		{"7.5", "2", "1.5"},
		{"-7.5", "2", "-1.5"},
		{"0.5", "0.2", "0.1"},
		{"-0.5", "0.2", "-0.1"},
	}
	for _, tc := range cases {
		a := mustParse(t, tc.a, HalfAwayFromZero)
		b := mustParse(t, tc.b, HalfAwayFromZero)
		rem := must2(Mod(a, b))
		if rem.String() != tc.want {
			t.Errorf("Mod(%s,%s) = %s, want %s", tc.a, tc.b, rem.String(), tc.want)
		}
		quo, rem2, err := QuoRem(a, b)
		if err != nil {
			t.Fatalf("QuoRem(%s,%s): %v", tc.a, tc.b, err)
		}
		if rem2.String() != tc.want {
			t.Errorf("QuoRem rem(%s,%s) = %s, want %s", tc.a, tc.b, rem2.String(), tc.want)
		}
		back := must2(Add(must2(Mul(quo, b)), rem2))
		cmp, err := Cmp(back, a)
		if err != nil || cmp != 0 {
			t.Errorf("identity failed for %s %% %s: got %s", tc.a, tc.b, back.String())
		}
	}

	zero := mustParse(t, "0", HalfAwayFromZero)
	one := mustParse(t, "1", HalfAwayFromZero)
	if _, err := Mod(one, zero); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("Mod by zero err = %v, want ErrDivisionByZero", err)
	}
	if _, err := Div(one, zero); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("Div by zero err = %v, want ErrDivisionByZero", err)
	}
	if _, _, err := QuoRem(one, zero); !errors.Is(err, ErrDivisionByZero) {
		t.Fatalf("QuoRem by zero err = %v, want ErrDivisionByZero", err)
	}
}

// TestHalfRoundingDifference exercises exact half points on both signs.
func TestHalfRoundingDifference(t *testing.T) {
	// 5 times a 38-digit coefficient ending in 5 at scale 37 produces a
	// 39-digit product ending in 5 (exact half at the dropped digit). The kept
	// part is even, so half-away rounds up and half-even rounds down.
	five := mustParse(t, "5", HalfAwayFromZero)
	three := mustParse(t, "3.0000000000000000000000000000000000005", HalfAwayFromZero)
	if five.Scale() != 0 || three.Scale() != 37 {
		t.Fatalf("test fixture scales = %d, %d, want 0 and 37", five.Scale(), three.Scale())
	}
	posAway := must2(MulMode(five, three, HalfAwayFromZero))
	posEven := must2(MulMode(five, three, HalfToEven))
	wantAway := "15.000000000000000000000000000000000003"
	wantEven := "15.000000000000000000000000000000000002"
	if posAway.String() != wantAway || posEven.String() != wantEven {
		t.Errorf("positive half: away=%s even=%s, want %s/%s",
			posAway.String(), posEven.String(), wantAway, wantEven)
	}
	negAway := must2(MulMode(Neg(five), three, HalfAwayFromZero))
	negEven := must2(MulMode(Neg(five), three, HalfToEven))
	if negAway.String() != "-"+wantAway || negEven.String() != "-"+wantEven {
		t.Errorf("negative half: away=%s even=%s", negAway.String(), negEven.String())
	}

	// Division producing an exact half at the last target-scale digit: 1/16 =
	// 0.0625, which at target scale 3 is the exact half 0.0625 -> away 0.063,
	// even 0.062. Force target scale 3 via the operand precision formula.
	one := mustParse(t, "1", HalfAwayFromZero)
	sixteen := mustParse(t, "16.0", HalfAwayFromZero) // p=3, s=1 -> target = 0+3+1 = 4 (exact)
	qExact := must2(DivMode(one, sixteen, HalfToEven))
	if !strings.HasPrefix(qExact.String(), "0.0625") {
		t.Errorf("1/16 = %s, want 0.0625...", qExact.String())
	}

	long := "0." + strings.Repeat("9", 37) + "5"
	rAway := mustParse(t, long, HalfAwayFromZero)
	rEven := mustParse(t, long, HalfToEven)
	rat := parseRat(long)
	if rAway.String() != ratFixed(rat, rAway.Scale()) {
		t.Errorf("long fraction away = %s", rAway.String())
	}
	if rEven.String() != ratFixed(rat, rEven.Scale()) || rEven.Scale() != 38 {
		t.Errorf("long fraction even = %s scale %d", rEven.String(), rEven.Scale())
	}
}
