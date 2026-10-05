package decimal

import (
	"errors"
	"math/big"
	"math/rand"
	"testing"
)

func mustParse(t *testing.T, s string) *Decimal {
	t.Helper()
	d, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return d
}

// ratPow10 returns 10^n as a rational.
func ratPow10(n int64) *big.Rat {
	if n >= 0 {
		return new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil))
	}
	return new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(-n), nil))
}

// refRound rounds the exact rational v to scale fractional digits using an
// independent floor-based formulation, returning the rounded value and
// whether v was already representable at that scale.
func refRound(v *big.Rat, scale uint, mode RoundingMode) (*big.Rat, bool) {
	m := new(big.Rat).Mul(v, ratPow10(int64(scale)))
	// Euclidean Div with positive denominator is a true floor.
	floor := new(big.Int).Div(m.Num(), m.Denom())
	rem := new(big.Rat).Sub(m, new(big.Rat).SetInt(floor)) // in [0, 1)
	exact := rem.Sign() == 0
	out := new(big.Int).Set(floor)
	bump := func() { out.Add(out, big.NewInt(1)) }
	switch mode {
	case ToNegativeInf:
	case ToPositiveInf:
		if !exact {
			bump()
		}
	case ToZero:
		if !exact && m.Sign() < 0 {
			bump()
		}
	case TiesToEven:
		c := rem.Cmp(big.NewRat(1, 2))
		if c > 0 || (c == 0 && floor.Bit(0) == 1) {
			bump()
		}
	}
	return new(big.Rat).Quo(new(big.Rat).SetInt(out), ratPow10(int64(scale))), exact
}

func randDecimal(rng *rand.Rand) *Decimal {
	digits := 1 + rng.Intn(40)
	coeff := new(big.Int).Rand(rng, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil))
	if rng.Intn(2) == 0 {
		coeff.Neg(coeff)
	}
	return New(coeff, int64(rng.Intn(51)-25))
}

func checkRatEqual(t *testing.T, what string, got *Decimal, want *big.Rat) {
	t.Helper()
	if got.Rat().Cmp(want) != 0 {
		t.Fatalf("%s: got %s, want rational %s", what, got.String(), want.RatString())
	}
}

func TestArithmeticAgainstRat(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 3000; i++ {
		a, b := randDecimal(rng), randDecimal(rng)
		ra, rb := a.Rat(), b.Rat()
		switch rng.Intn(3) {
		case 0:
			checkRatEqual(t, "add", a.Add(b), new(big.Rat).Add(ra, rb))
		case 1:
			checkRatEqual(t, "sub", a.Sub(b), new(big.Rat).Sub(ra, rb))
		case 2:
			checkRatEqual(t, "mul", a.Mul(b), new(big.Rat).Mul(ra, rb))
		}
	}
}

func TestRoundAgainstRat(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	modes := []RoundingMode{ToZero, ToPositiveInf, ToNegativeInf, TiesToEven}
	for i := 0; i < 5000; i++ {
		d := randDecimal(rng)
		scale := uint(rng.Intn(31))
		mode := modes[rng.Intn(len(modes))]
		got := d.Round(scale, mode)
		want, wantExact := refRound(d.Rat(), scale, mode)
		checkRatEqual(t, "round", got.Value, want)
		if got.Exact != wantExact {
			t.Fatalf("round %s to %d: Exact=%v, want %v", d, scale, got.Exact, wantExact)
		}
	}
}

func TestQuoAgainstRat(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	modes := []RoundingMode{ToZero, ToPositiveInf, ToNegativeInf, TiesToEven}
	for i := 0; i < 5000; i++ {
		num, den := randDecimal(rng), randDecimal(rng)
		if den.Sign() == 0 {
			continue
		}
		scale := uint(rng.Intn(31))
		mode := modes[rng.Intn(len(modes))]
		got, err := num.Quo(den, scale, mode)
		if err != nil {
			t.Fatalf("Quo: %v", err)
		}
		exact := new(big.Rat).Quo(num.Rat(), den.Rat())
		want, wantExact := refRound(exact, scale, mode)
		checkRatEqual(t, "quo", got.Value, want)
		if got.Exact != wantExact {
			t.Fatalf("quo %s / %s at %d: Exact=%v, want %v", num, den, scale, got.Exact, wantExact)
		}
	}
}

func TestNegativeHalves(t *testing.T) {
	cases := []struct {
		in               string
		toZero, toPosInf string
		toNegInf, toEven string
	}{
		{"-2.5", "-2", "-2", "-3", "-2"},
		{"-1.5", "-1", "-1", "-2", "-2"},
		{"-0.5", "0", "0", "-1", "0"},
		{"0.5", "0", "1", "0", "0"},
		{"1.5", "1", "2", "1", "2"},
		{"2.5", "2", "3", "2", "2"},
		{"-3.5", "-3", "-3", "-4", "-4"},
	}
	for _, c := range cases {
		d := mustParse(t, c.in)
		for _, want := range []struct {
			mode RoundingMode
			out  string
		}{
			{ToZero, c.toZero},
			{ToPositiveInf, c.toPosInf},
			{ToNegativeInf, c.toNegInf},
			{TiesToEven, c.toEven},
		} {
			got := d.Round(0, want.mode)
			if got.Exact {
				t.Fatalf("Round(%s, 0): unexpectedly exact", c.in)
			}
			if got.Value.String() != want.out {
				t.Fatalf("Round(%s, 0, mode=%d) = %s, want %s", c.in, want.mode, got.Value, want.out)
			}
		}
	}
}

func TestHugeExponents(t *testing.T) {
	one := NewInt(1)
	big1 := New(big.NewInt(1), 2000)   // 1e2000
	small := New(big.NewInt(1), -2000) // 1e-2000

	sum := big1.Add(small)
	checkRatEqual(t, "1e2000+1e-2000", sum, new(big.Rat).Add(big1.Rat(), small.Rat()))
	checkRatEqual(t, "(a+b)-a", sum.Sub(big1), small.Rat())
	checkRatEqual(t, "1e2000*1e-2000", big1.Mul(small), one.Rat())

	hip := New(big.NewInt(7), 1500)
	q, err := hip.Quo(NewInt(3), 100, TiesToEven)
	if err != nil {
		t.Fatal(err)
	}
	if q.Exact {
		t.Fatal("7e1500/3 must be inexact")
	}
	want, _ := refRound(new(big.Rat).Quo(hip.Rat(), big.NewRat(3, 1)), 100, TiesToEven)
	checkRatEqual(t, "7e1500/3", q.Value, want)

	// (1e1000 + 1) * 1e-1000 = 1 + 1e-1000
	got := New(big.NewInt(1), 1000).Add(one).Mul(New(big.NewInt(1), -1000))
	checkRatEqual(t, "spread mul", got, new(big.Rat).Add(one.Rat(), New(big.NewInt(1), -1000).Rat()))
}

func TestBudgetHonesty(t *testing.T) {
	// 1/3 never terminates: every budget must report inexact.
	for scale := uint(0); scale <= 60; scale++ {
		r, err := NewInt(1).Quo(NewInt(3), scale, TiesToEven)
		if err != nil {
			t.Fatal(err)
		}
		if r.Exact {
			t.Fatalf("1/3 at scale %d falsely claims exact", scale)
		}
		want, _ := refRound(big.NewRat(1, 3), scale, TiesToEven)
		checkRatEqual(t, "1/3", r.Value, want)
	}
	// 1/2 needs one digit: scale 0 is inexact, scale 1 is exact.
	r0, _ := NewInt(1).Quo(NewInt(2), 0, TiesToEven)
	if r0.Exact || r0.Value.String() != "0" {
		t.Fatalf("1/2 @0 = %s exact=%v, want inexact 0", r0.Value, r0.Exact)
	}
	r1, _ := NewInt(1).Quo(NewInt(2), 1, TiesToEven)
	if !r1.Exact || r1.Value.String() != "0.5" {
		t.Fatalf("1/2 @1 = %s exact=%v, want exact 0.5", r1.Value, r1.Exact)
	}
	// 22/7 at scale 2 is 3.14 and inexact.
	r, _ := NewInt(22).Quo(NewInt(7), 2, ToZero)
	if r.Exact || r.Value.String() != "3.14" {
		t.Fatalf("22/7 @2 = %s exact=%v, want inexact 3.14", r.Value, r.Exact)
	}
	// Zero dividend is exact at any budget.
	z, err := NewInt(0).Quo(NewInt(7), 10, ToZero)
	if err != nil || !z.Exact || z.Value.Sign() != 0 {
		t.Fatalf("0/7 = %v exact=%v err=%v", z.Value, z.Exact, err)
	}
}

func TestDivByZero(t *testing.T) {
	if _, err := NewInt(1).Quo(NewInt(0), 10, TiesToEven); !errors.Is(err, ErrDivByZero) {
		t.Fatalf("1/0 err = %v, want ErrDivByZero", err)
	}
	if _, err := NewInt(0).Quo(NewInt(0), 10, TiesToEven); !errors.Is(err, ErrDivByZero) {
		t.Fatalf("0/0 err = %v, want ErrDivByZero", err)
	}
}

func TestImmutability(t *testing.T) {
	coeff := big.NewInt(123)
	d := New(coeff, -2)
	coeff.SetInt64(999999) // mutating the caller's big.Int must not leak in
	if d.String() != "1.23" {
		t.Fatalf("New did not copy coeff: %s", d)
	}
	e := mustParse(t, "0.077")
	sum := d.Add(e)
	if d.String() != "1.23" || e.String() != "0.077" {
		t.Fatalf("Add mutated operands: d=%s e=%s", d, e)
	}
	if sum.String() != "1.307" {
		t.Fatalf("1.23+0.077 = %s", sum)
	}
	got := sum.Coeff()
	got.SetInt64(0) // mutating the extracted copy must not leak back
	if sum.String() != "1.307" {
		t.Fatalf("Coeff leaked internal state: %s", sum)
	}
}

func TestParseStringRoundTrip(t *testing.T) {
	fixed := []struct{ in, out string }{
		{"0", "0"}, {"-0", "0"}, {"123", "123"}, {"-0.5", "-0.5"},
		{"1.5e3", "1500"}, {"1.5e-3", "0.0015"}, {"2E-7", "0.0000002"},
		{".25", "0.25"}, {"5.", "5"}, {"+7", "7"}, {"1e100", "1e100"},
	}
	for _, c := range fixed {
		if got := mustParse(t, c.in).String(); got != c.out {
			t.Fatalf("Parse(%q).String() = %q, want %q", c.in, got, c.out)
		}
	}
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 2000; i++ {
		d := randDecimal(rng)
		back, err := Parse(d.String())
		if err != nil {
			t.Fatalf("reparse %q: %v", d.String(), err)
		}
		if !back.Equal(d) {
			t.Fatalf("round trip: %s -> %s", d, back)
		}
	}
	for _, bad := range []string{"", ".", "e5", "1e", "abc", "1.2.3", "--1"} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("Parse(%q) unexpectedly succeeded", bad)
		}
	}
}
