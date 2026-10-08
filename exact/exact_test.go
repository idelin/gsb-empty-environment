package exact

import (
	"math/big"
	"math/rand"
	"testing"
)

func TestParseDecimalExact(t *testing.T) {
	cases := []struct {
		in   string
		want string // exact rational
	}{
		{"0.1", "1/10"},
		{"2.5", "5/2"},
		{"-0.001", "-1/1000"},
		{"123.456", "15432/125"},
		{"+7", "7"},
		{".5", "1/2"},
		{"-0", "0"},
		{"00012.3400", "617/50"},
	}
	for _, c := range cases {
		r, err := ParseDecimal(c.in)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", c.in, err)
		}
		if r.RatString() != c.want {
			t.Errorf("ParseDecimal(%q) = %s, want %s", c.in, r.RatString(), c.want)
		}
	}
	for _, bad := range []string{"", "abc", "1.2.3", "-", ".", "1,5", "1e3"} {
		if _, err := ParseDecimal(bad); err == nil {
			t.Errorf("ParseDecimal(%q) expected error", bad)
		}
	}
}

func TestRoundHalfEvenMidpoints(t *testing.T) {
	cases := []struct {
		in     string
		places int
		want   string
		tie    bool
	}{
		// positive midpoints
		{"2.5", 0, "2", true},
		{"3.5", 0, "4", true},
		{"0.125", 2, "0.12", true},
		{"0.375", 2, "0.38", true},
		{"9.995", 2, "10.00", true}, // tie + carry across decimal point
		// negative midpoints
		{"-2.5", 0, "-2", true},
		{"-3.5", 0, "-4", true},
		{"-0.125", 2, "-0.12", true},
		{"-0.375", 2, "-0.38", true},
		{"-9.995", 2, "-10.00", true}, // floor is already even
		{"-10.005", 2, "-10.00", true},
		// negative zero must normalize to zero
		{"-0.5", 0, "0", true},
		{"-0.001", 2, "0.00", false},
		{"-0.0049", 2, "0.00", false},
		// non-tie carry
		{"99.999", 2, "100.00", false},
		{"-99.999", 2, "-100.00", false},
		{"0.0051", 2, "0.01", false},
		{"0.0049", 2, "0.00", false},
		// exact values
		{"1.25", 2, "1.25", false},
		{"-7", 0, "-7", false},
	}
	for _, c := range cases {
		v := MustDecimal(c.in)
		got := FormatFixed(v, c.places)
		if got != c.want {
			t.Errorf("FormatFixed(%s, %d) = %s, want %s", c.in, c.places, got, c.want)
		}
		_, ev := RoundHalfEven(v, c.places)
		if ev.Tie != c.tie {
			t.Errorf("RoundHalfEven(%s, %d) tie=%v, want %v (%s)", c.in, c.places, ev.Tie, c.tie, ev)
		}
		if got == "0" || got == "0.00" {
			if ev.Result.Sign() != 0 {
				t.Errorf("negative zero not normalized for %s: coef=%s", c.in, ev.Result)
			}
		}
	}
}

// referenceRound is an independent half-even implementation used only by
// tests: it isolates the fractional part of value*10^places with big.Rat
// arithmetic and compares against 1/2.
func referenceRound(v *big.Rat, places int) string {
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil))
	s := new(big.Rat).Mul(v, scale)
	num, den := s.Num(), s.Denom()
	floor, rem := new(big.Int), new(big.Int)
	floor.QuoRem(num, den, rem)
	if rem.Sign() < 0 {
		floor.Sub(floor, big.NewInt(1))
		rem.Add(rem, den)
	}
	frac := new(big.Rat).SetFrac(rem, den)
	half := big.NewRat(1, 2)
	switch frac.Cmp(half) {
	case 1:
		floor.Add(floor, big.NewInt(1))
	case 0:
		if floor.Bit(0) == 1 {
			floor.Add(floor, big.NewInt(1))
		}
	}
	return FormatCoef(floor, places)
}

func TestRoundAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 2000; i++ {
		// random decimal with up to 6 fractional digits, random sign
		mant := rng.Int63n(2000000) - 1000000
		dec := rng.Intn(7)
		den := int64(1)
		for j := 0; j < dec; j++ {
			den *= 10
		}
		v := big.NewRat(mant, den)
		places := rng.Intn(5)
		got := FormatFixed(v, places)
		want := referenceRound(v, places)
		if got != want {
			t.Fatalf("FormatFixed(%s, %d) = %s, reference %s", v.RatString(), places, got, want)
		}
	}
}

func TestChainDivideByZero(t *testing.T) {
	zero := big.NewRat(0, 1)
	_, err := EvalChain(MustDecimal("1.0"), []ChainOp{DivBy(zero, "bad")}, 2)
	if !IsDivideByZero(err) {
		t.Fatalf("expected divide-by-zero error, got %v", err)
	}
	if _, err := Rational(1, 0); err != ErrDivideByZero {
		t.Fatalf("Rational(1,0) = %v, want ErrDivideByZero", err)
	}
}

func TestChainExactThenRound(t *testing.T) {
	// 1 statute mile = 25146/15625 km exactly (1.609344).
	kmPerMile := big.NewRat(25146, 15625)
	res, err := EvalChainDecimal("2.5", []ChainOp{
		MulBy(kmPerMile, "mi->km"),
		MulBy(big.NewRat(1000, 1), "km->m"),
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := big.NewRat(402336, 100)
	if res.Exact.Cmp(want) != 0 {
		t.Fatalf("exact = %s, want %s", res.Exact.RatString(), want.RatString())
	}
	if res.Text != "4023.36" {
		t.Fatalf("text = %s, want 4023.36", res.Text)
	}
}

func TestChainRoundTripExact(t *testing.T) {
	// Repeated conversions there and back must return the exact original
	// value: no float drift is possible.
	factors := []*big.Rat{
		big.NewRat(25146, 15625), // mi -> km
		big.NewRat(3937, 156),    // approx in -> cm style rational
		big.NewRat(7, 3),
	}
	start := MustDecimal("123.456")
	forward := []ChainOp{}
	backward := []ChainOp{}
	for i, f := range factors {
		forward = append(forward, MulBy(f, "fwd"))
		backward = append([]ChainOp{DivBy(f, "back")}, backward...)
		_ = i
	}
	res, err := EvalChain(start, append(forward, backward...), -1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Exact.Cmp(start) != 0 {
		t.Fatalf("round trip = %s, want %s", res.Exact.RatString(), start.RatString())
	}
}

// referenceChain folds all factors into a single rational first and applies
// it in one multiplication — a deliberately different computation path from
// EvalChain's step-by-step loop.
func referenceChain(start *big.Rat, ops []ChainOp) *big.Rat {
	combined := big.NewRat(1, 1)
	for _, op := range ops {
		if op.Mul {
			combined.Mul(combined, op.Factor)
		} else {
			combined.Quo(combined, op.Factor)
		}
	}
	return new(big.Rat).Mul(start, combined)
}

func TestChainAgainstBigIntFractionReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		mant := rng.Int63n(20000000) - 10000000
		start := big.NewRat(mant, 10000)
		var ops []ChainOp
		for j := 0; j < 1+rng.Intn(5); j++ {
			num := rng.Int63n(999) + 1
			den := rng.Int63n(999) + 1
			if rng.Intn(2) == 0 {
				num = -num
			}
			f := big.NewRat(num, den)
			if rng.Intn(2) == 0 {
				ops = append(ops, MulBy(f, ""))
			} else {
				ops = append(ops, DivBy(f, ""))
			}
		}
		places := rng.Intn(4)
		res, err := EvalChain(start, ops, places)
		if err != nil {
			t.Fatal(err)
		}
		ref := referenceChain(start, ops)
		if res.Exact.Cmp(ref) != 0 {
			t.Fatalf("exact = %s, reference %s", res.Exact.RatString(), ref.RatString())
		}
		if got, want := res.Text, referenceRound(ref, places); got != want {
			t.Fatalf("rounded = %s, reference %s", got, want)
		}
	}
}
