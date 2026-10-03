package algebraic

import (
	"math/big"
	"testing"
)

func requireSqrt(t *testing.T, n *Number) *Number {
	t.Helper()
	out, err := Sqrt(n)
	if err != nil {
		t.Fatalf("Sqrt(%v): %v", n, err)
	}
	return out
}

func requireDiv(t *testing.T, a, b *Number) *Number {
	t.Helper()
	out, err := Div(a, b)
	if err != nil {
		t.Fatalf("Div(%v, %v): %v", a, b, err)
	}
	return out
}

func TestNestedRadicalEquality(t *testing.T) {
	sqrt2 := requireSqrt(t, Int(2))
	sqrt3 := requireSqrt(t, Int(3))
	sqrt6 := requireSqrt(t, Int(6))

	left := Add(sqrt2, sqrt3)
	right := requireSqrt(t, Add(Int(5), Mul(Int(2), sqrt6)))

	if got := left.Compare(right); got != 0 {
		t.Fatalf("Compare(%v, %v) = %d, want 0", left, right, got)
	}

	leftPoly := left.MinimalPolynomial()
	rightPoly := right.MinimalPolynomial()
	if leftPoly.String() != "x^4 - 10*x^2 + 1" {
		t.Fatalf("left minimal polynomial = %s", leftPoly)
	}
	if leftPoly.String() != rightPoly.String() {
		t.Fatalf("minimal polynomials differ: %s and %s", leftPoly, rightPoly)
	}
	if !intervalEqual(left.IsolatingInterval(), right.IsolatingInterval()) {
		t.Fatalf("isolating intervals differ: %s and %s", left.IsolatingInterval(), right.IsolatingInterval())
	}
}

func TestStrictRadicalComparison(t *testing.T) {
	sqrt2 := requireSqrt(t, Int(2))
	sqrt3 := requireSqrt(t, Int(3))
	sqrt10 := requireSqrt(t, Int(10))

	left := Add(sqrt2, sqrt3)
	if got := left.Compare(sqrt10); got != -1 {
		t.Fatalf("Compare(%v, %v) = %d, want -1", left, sqrt10, got)
	}
	if got := sqrt10.Compare(left); got != 1 {
		t.Fatalf("Compare(%v, %v) = %d, want 1", sqrt10, left, got)
	}
}

func TestExactSignAndZero(t *testing.T) {
	sqrt2 := requireSqrt(t, Int(2))
	zeroExpression := Sub(Mul(sqrt2, sqrt2), Int(2))
	if got := zeroExpression.Sign(); got != 0 {
		t.Fatalf("Sign(%v) = %d, want 0", zeroExpression, got)
	}
	if got := Sub(sqrt2, Int(1)).Sign(); got != 1 {
		t.Fatalf("Sign(sqrt(2)-1) = %d, want 1", got)
	}
	if got := Sub(Int(1), sqrt2).Sign(); got != -1 {
		t.Fatalf("Sign(1-sqrt(2)) = %d, want -1", got)
	}
}

func TestRationalAndFieldOperations(t *testing.T) {
	rational := Add(Rat(3, 2), Rat(1, 4))
	if got := rational.Compare(Rat(7, 4)); got != 0 {
		t.Fatalf("Compare(3/2+1/4, 7/4) = %d, want 0", got)
	}

	sqrt2 := requireSqrt(t, Int(2))
	sqrt8 := requireSqrt(t, Int(8))
	quotient := requireDiv(t, sqrt8, sqrt2)
	if got := quotient.Compare(Int(2)); got != 0 {
		t.Fatalf("Compare(sqrt(8)/sqrt(2), 2) = %d, want 0", got)
	}

	product := Mul(Add(sqrt2, Int(1)), Sub(sqrt2, Int(1)))
	if got := product.Compare(Int(1)); got != 0 {
		t.Fatalf("Compare((sqrt(2)+1)(sqrt(2)-1), 1) = %d, want 0", got)
	}
}

func TestMinimalPolynomialCoefficients(t *testing.T) {
	sqrt2 := requireSqrt(t, Int(2))
	sqrt3 := requireSqrt(t, Int(3))
	p := Add(sqrt2, sqrt3).MinimalPolynomial()
	got := p.Coefficients()
	want := []*big.Rat{
		big.NewRat(1, 1),
		big.NewRat(0, 1),
		big.NewRat(-10, 1),
		big.NewRat(0, 1),
		big.NewRat(1, 1),
	}
	if len(got) != len(want) {
		t.Fatalf("coefficient count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Cmp(want[i]) != 0 {
			t.Fatalf("coefficient %d = %s, want %s", i, got[i], want[i])
		}
	}
}
