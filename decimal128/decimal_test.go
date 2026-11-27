package decimal128

import (
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

// ---------- big.Rat reference helpers ----------

func ratOf(d Decimal128) *big.Rat {
	c := new(big.Int)
	if d.coeff.hi < 0 {
		c.Set(u256ToBig(d.coeff.neg().mag().to256())).Neg(c)
	} else {
		c.Set(u256ToBig(d.coeff.mag().to256()))
	}
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d.scale)), nil)
	return new(big.Rat).SetFrac(c, den)
}

// roundRat returns the coefficient c such that c/10^scale rounds the rational
// value x under the requested half rule.
func roundRat(x *big.Rat, scale int, mode RoundingMode) *big.Int {
	num := new(big.Int).Mul(x.Num(),
		new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil))
	q, r := new(big.Int).QuoRem(num, x.Denom(), new(big.Int))
	if r.Sign() == 0 {
		return q
	}
	neg := num.Sign() < 0
	if neg {
		num.Neg(num)
		q.Abs(q)
		r.Abs(r)
	}
	twice := new(big.Int).Lsh(r, 1)
	den := new(big.Int).Abs(x.Denom())
	cmp := twice.Cmp(den)
	if cmp > 0 || (cmp == 0 && mode == HalfAwayFromZero) {
		q.Add(q, big.NewInt(1))
	} else if cmp == 0 && mode == HalfToEven && q.Bit(0) == 1 {
		q.Add(q, big.NewInt(1))
	}
	if neg {
		q.Neg(q)
	}
	return q
}

func mustParse(t *testing.T, s string) Decimal128 {
	t.Helper()
	d, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return d
}

func expectRatMode(t *testing.T, name string, got Decimal128, want *big.Rat, mode RoundingMode) {
	t.Helper()
	wantCoeff := roundRat(want, got.Scale(), mode)
	if signedBig(got).Cmp(wantCoeff) != 0 {
		t.Fatalf("%s: value mismatch\n got=%s (%s)\n want=%s (coeff %s at scale %d)",
			name, got.String(), ratOf(got).FloatString(45),
			want.FloatString(45), wantCoeff.String(), got.Scale())
	}
}

func expectRat(t *testing.T, name string, got Decimal128, want *big.Rat) {
	expectRatMode(t, name, got, want, HalfAwayFromZero)
}

func signedBig(d Decimal128) *big.Int {
	c := new(big.Int).Set(u256ToBig(d.coeff.mag().to256()))
	if d.coeff.hi < 0 {
		c.Neg(c)
	}
	return c
}

// ---------- parsing / formatting ----------

func TestParseFormat(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0", "0"},
		{"-0", "0"},
		{"-0.000", "0.000"},
		{"000123", "123"},
		{"0.1", "0.1"},
		{".5", "0.5"},
		{"1.2300", "1.2300"},
		{"123.456", "123.456"},
		{"1e3", "1000"},
		{"1.5e2", "150"},
		{"1.5e-2", "0.015"},
		{"100e-2", "1.00"},
		{"-2.5E+1", "-25"},
		{"99999999999999999999999999999999999999", "99999999999999999999999999999999999999"},
		{"0.00000000000000000000000000000000000001", "0.00000000000000000000000000000000000001"},
	}
	for _, tc := range cases {
		d, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		if got := d.String(); got != tc.want {
			t.Fatalf("Parse(%q).String() = %q, want %q", tc.in, got, tc.want)
		}
		r, ok := new(big.Rat).SetString(tc.in)
		if !ok {
			t.Fatalf("rat SetString failed for %q", tc.in)
		}
		expectRat(t, "parse "+tc.in, d, r)
	}

	for _, bad := range []string{"", "   ", "1.2.3", "abc", "1e", ".", "1e9999", "-", "+"} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("Parse(%q) expected error", bad)
		}
	}

	nz := mustParse(t, "-0.00")
	if hi, _ := nz.Coeff(); hi != 0 {
		t.Fatalf("negative zero not normalized: %+v", nz)
	}
}

func TestScientificFormat(t *testing.T) {
	cases := map[string]string{
		"123.456": "1.23456E+2",
		"0.0012":  "1.2E-3",
		"-500":    "-5.00E+2",
		"0":       "0E+0",
	}
	for in, want := range cases {
		d := mustParse(t, in)
		if got := d.StringScientific(); got != want {
			t.Fatalf("%q scientific = %q, want %q", in, got, want)
		}
	}
}

func TestParseRoundingModes(t *testing.T) {
	const tie = "12345678901234567890123456789012345678.5"
	away, err := Context{Mode: HalfAwayFromZero}.Parse(tie)
	if err != nil {
		t.Fatal(err)
	}
	even, err := Context{Mode: HalfToEven}.Parse(tie)
	if err != nil {
		t.Fatal(err)
	}
	if away.String() != "12345678901234567890123456789012345679" {
		t.Fatalf("away = %s", away.String())
	}
	if even.String() != "12345678901234567890123456789012345678" {
		t.Fatalf("even = %s", even.String())
	}

	negAway, _ := Context{Mode: HalfAwayFromZero}.Parse("-" + tie)
	negEven, _ := Context{Mode: HalfToEven}.Parse("-" + tie)
	if negAway.String() != "-12345678901234567890123456789012345679" {
		t.Fatalf("neg away = %s", negAway.String())
	}
	if negEven.String() != "-12345678901234567890123456789012345678" {
		t.Fatalf("neg even = %s", negEven.String())
	}

	up, err := Context{Mode: HalfToEven}.Parse("12345678901234567890123456789012345678.51")
	if err != nil {
		t.Fatal(err)
	}
	if up.String() != "12345678901234567890123456789012345679" {
		t.Fatalf("non-tie up = %s", up.String())
	}
}

// ---------- type inference ----------

func TestInference(t *testing.T) {
	p, s := inferAddSub(38, 10, 38, 10)
	if p != 38 || s != 0 {
		t.Fatalf("addsub infer = (%d,%d), want (38,0)", p, s)
	}
	p, s = inferMul(20, 5, 20, 5)
	if p != 38 || s != 7 { // (41,10) reduced by 3
		t.Fatalf("mul infer = (%d,%d), want (38,7)", p, s)
	}
	p, s = inferMul(30, 10, 30, 10)
	if p != 41 || s != 0 || p <= MaxPrecision {
		t.Fatalf("mul clamp = (%d,%d), want overflow marker (41,0)", p, s)
	}
	p, s = inferAddSub(10, 4, 10, 4)
	if p != 16 || s != 6 {
		t.Fatalf("addsub small = (%d,%d), want (16,6)", p, s)
	}
	p, s = inferDiv(38, 10, 20, 5)
	if p != 38 || s != 5 {
		t.Fatalf("div infer = (%d,%d), want (38,5)", p, s)
	}
}

// ---------- arithmetic vs big.Rat ----------

func randomDecimal(rng *rand.Rand) Decimal128 {
	whole := rng.Intn(18) + 1
	frac := rng.Intn(7)
	var sb strings.Builder
	if rng.Intn(3) == 0 {
		sb.WriteByte('-')
	}
	for i := 0; i < whole; i++ {
		if i == 0 {
			sb.WriteByte(byte('1' + rng.Intn(9)))
		} else {
			sb.WriteByte(byte('0' + rng.Intn(10)))
		}
	}
	if frac > 0 {
		sb.WriteByte('.')
		for i := 0; i < frac; i++ {
			sb.WriteByte(byte('0' + rng.Intn(10)))
		}
	}
	d, err := Parse(sb.String())
	if err != nil {
		panic(err)
	}
	return d
}

func TestArithmeticReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 400; iter++ {
		a := randomDecimal(rng)
		b := randomDecimal(rng)
		ra, rb := ratOf(a), ratOf(b)
		for _, mode := range []RoundingMode{HalfAwayFromZero, HalfToEven} {
			ctx := Context{Mode: mode}

			sum, err := ctx.Add(a, b)
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			expectRatMode(t, "add", sum, new(big.Rat).Add(ra, rb), mode)

			diff, err := ctx.Sub(a, b)
			if err != nil {
				t.Fatalf("sub: %v", err)
			}
			expectRatMode(t, "sub", diff, new(big.Rat).Sub(ra, rb), mode)

			prod, err := ctx.Mul(a, b)
			if err != nil {
				t.Fatalf("mul: %v", err)
			}
			expectRatMode(t, "mul", prod, new(big.Rat).Mul(ra, rb), mode)

			if b.IsZero() {
				continue
			}
			quot, err := ctx.Div(a, b)
			if err != nil {
				t.Fatalf("div: %v", err)
			}
			want := roundRat(new(big.Rat).Quo(ra, rb), quot.Scale(), mode)
			if signedBig(quot).Cmp(want) != 0 {
				t.Fatalf("div mode %d: a=%s b=%s got=%s want coeff %s at scale %d",
					mode, a.String(), b.String(), quot.String(), want.String(), quot.Scale())
			}

			rem, err := ctx.Mod(a, b)
			if err != nil {
				t.Fatalf("mod: %v", err)
			}
			// r = a - trunc(a/b)*b, evaluated exactly over the rationals.
			an, ad := ra.Num(), ra.Denom()
			bn, bd := rb.Num(), rb.Denom()
			num := new(big.Int).Mul(an, bd)
			den := new(big.Int).Mul(ad, bn)
			trunc := new(big.Int).Quo(num, den)
			wantNum := new(big.Int).Sub(num, new(big.Int).Mul(trunc, den))
			wantRat := new(big.Rat).SetFrac(wantNum, new(big.Int).Mul(ad, bd))
			expectRat(t, "mod", rem, wantRat)
			if rem.Sign() != 0 && rem.Sign() != a.Sign() {
				t.Fatalf("mod sign must follow dividend: %s mod %s = %s",
					a.String(), b.String(), rem.String())
			}
		}
	}
}
