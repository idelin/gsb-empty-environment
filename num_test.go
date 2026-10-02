package algebraic

import (
	"math/big"
	"testing"
)

// The core acceptance case: sqrt(2)+sqrt(3) vs sqrt(5+2*sqrt(6)).
// The two numbers are equal; the algorithm must prove equality exactly
// (the difference polynomial has 0 as a root inside its isolating
// interval) instead of reporting a tiny floating-point inequality.
func TestEqualityWithNestedRadical(t *testing.T) {
	a := Add(Sqrt(Rat(2)), Sqrt(Rat(3)))
	inner := Add(Rat(5), Mul(Rat(2), Sqrt(Rat(6))))
	b := Sqrt(inner)
	if got := Compare(a, b); got != 0 {
		t.Fatalf("Compare(sqrt(2)+sqrt(3), sqrt(5+2*sqrt(6))) = %d, want 0", got)
	}
	// Both sides must reduce to the same minimal polynomial x^4-10x^2+1.
	want := RatPoly(1, 0, -10, 0, 1)
	if !a.MinimalPoly().Equal(want) {
		t.Fatalf("minimal poly of sqrt(2)+sqrt(3) = %s, want %s", a.MinimalPoly(), want)
	}
	if !b.MinimalPoly().Equal(want) {
		t.Fatalf("minimal poly of sqrt(5+2*sqrt(6)) = %s, want %s", b.MinimalPoly(), want)
	}
}

// sqrt(2)+sqrt(3) = 3.14626... < sqrt(10) = 3.16227...; the sign of
// the difference must be decided exactly, without floating point.
func TestCompareSqrt2PlusSqrt3WithSqrt10(t *testing.T) {
	a := Add(Sqrt(Rat(2)), Sqrt(Rat(3)))
	b := Sqrt(Rat(10))
	if got := Compare(a, b); got != -1 {
		t.Fatalf("Compare(sqrt(2)+sqrt(3), sqrt(10)) = %d, want -1", got)
	}
	if got := Compare(b, a); got != 1 {
		t.Fatalf("Compare(sqrt(10), sqrt(2)+sqrt(3)) = %d, want 1", got)
	}
}

func TestSign(t *testing.T) {
	cases := []struct {
		n    Num
		want int
	}{
		{Sub(Sqrt(Rat(2)), Rat(1)), 1},
		{Sub(Sqrt(Rat(2)), Rat(2)), -1},
		{Sub(Rat(2), Sqrt(Rat(4))), 0},
		{Sub(Sqrt(Rat(2)), Sqrt(Rat(3))), -1},
		{Rat(0), 0},
		{RatFrac(-3, 7), -1},
	}
	for i, c := range cases {
		if got := c.n.Sign(); got != c.want {
			t.Fatalf("case %d: Sign() = %d, want %d", i, got, c.want)
		}
	}
}

func TestSqrtOfPerfectSquare(t *testing.T) {
	if got := Compare(Sqrt(Rat(4)), Rat(2)); got != 0 {
		t.Fatalf("Compare(sqrt(4), 2) = %d, want 0", got)
	}
	if got := Compare(Sqrt(RatFrac(9, 16)), RatFrac(3, 4)); got != 0 {
		t.Fatalf("Compare(sqrt(9/16), 3/4) = %d, want 0", got)
	}
}

func TestMultiplication(t *testing.T) {
	if got := Compare(Mul(Sqrt(Rat(2)), Sqrt(Rat(3))), Sqrt(Rat(6))); got != 0 {
		t.Fatalf("Compare(sqrt(2)*sqrt(3), sqrt(6)) = %d, want 0", got)
	}
	if got := Compare(Mul(Sqrt(Rat(2)), Sqrt(Rat(2))), Rat(2)); got != 0 {
		t.Fatalf("Compare(sqrt(2)*sqrt(2), 2) = %d, want 0", got)
	}
	if got := Compare(Mul(Rat(2), Sqrt(Rat(2))), Sqrt(Rat(8))); got != 0 {
		t.Fatalf("Compare(2*sqrt(2), sqrt(8)) = %d, want 0", got)
	}
}

func TestRationals(t *testing.T) {
	if got := Compare(RatFrac(1, 2), RatFrac(2, 3)); got != -1 {
		t.Fatalf("Compare(1/2, 2/3) = %d, want -1", got)
	}
	if got := Compare(RatFrac(2, 3), RatFrac(2, 3)); got != 0 {
		t.Fatalf("Compare(2/3, 2/3) = %d, want 0", got)
	}
	if got := Compare(Add(RatFrac(1, 3), RatFrac(1, 6)), RatFrac(1, 2)); got != 0 {
		t.Fatalf("Compare(1/3+1/6, 1/2) = %d, want 0", got)
	}
}

func TestResultant(t *testing.T) {
	// res(x^2-2, x^2-3) = (2-3)*(2-3) = 1
	r := Resultant(RatPoly(-2, 0, 1), RatPoly(-3, 0, 1))
	if r.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("Resultant(x^2-2, x^2-3) = %s, want 1", r)
	}
}

func TestSumPolynomial(t *testing.T) {
	// The annihilating polynomial of sqrt(2)+sqrt(3).
	h := polySumPoly(RatPoly(-2, 0, 1), RatPoly(-3, 0, 1)).Monic()
	want := RatPoly(1, 0, -10, 0, 1)
	if !h.Equal(want) {
		t.Fatalf("sum polynomial = %s, want %s", h, want)
	}
}

func TestFactorIrreducible(t *testing.T) {
	// x^4 - 10x^2 + 1 is irreducible over Q.
	fs := Factor(RatPoly(1, 0, -10, 0, 1))
	if len(fs) != 1 {
		t.Fatalf("Factor(x^4-10x^2+1) returned %d factors, want 1", len(fs))
	}
}

func TestFactorReducible(t *testing.T) {
	// x^4 - 5x^2 + 4 = (x-1)(x+1)(x-2)(x+2)
	fs := Factor(RatPoly(4, 0, -5, 0, 1))
	if len(fs) != 4 {
		t.Fatalf("Factor(x^4-5x^2+4) returned %d factors, want 4: %v", len(fs), fs)
	}
	// (x^2-2)^2 must factor to a single irreducible x^2-2.
	fs = Factor(RatPoly(4, 0, -4, 0, 1))
	if len(fs) != 1 || !fs[0].Equal(RatPoly(-2, 0, 1)) {
		t.Fatalf("Factor((x^2-2)^2) = %v, want [x^2-2]", fs)
	}
}

func TestIsolate(t *testing.T) {
	ivs := Isolate(RatPoly(-2, 0, 1))
	if len(ivs) != 2 {
		t.Fatalf("Isolate(x^2-2) returned %d intervals, want 2", len(ivs))
	}
	if ivs[0][0].Sign() >= 0 || ivs[1][1].Sign() <= 0 {
		t.Fatalf("Isolate(x^2-2) intervals not ordered around 0: %v", ivs)
	}
}
