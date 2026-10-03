package ratbezier

import (
	"math/big"
	"testing"
)

func polyEq(p *Poly, coeffs ...int64) bool {
	q := NewPoly(coeffs...)
	if p.Degree() != q.Degree() {
		return false
	}
	for i := 0; i <= p.Degree(); i++ {
		if p.Coeff(i).Cmp(q.Coeff(i)) != 0 {
			return false
		}
	}
	return true
}

func TestPolyArithmetic(t *testing.T) {
	// (1 + 2x) * (3 + x) = 3 + 7x + 2x^2
	p := NewPoly(1, 2)
	q := NewPoly(3, 1)
	if got := p.Mul(q); !polyEq(got, 3, 7, 2) {
		t.Errorf("mul = %s, want 3 + 7t + 2t^2", got)
	}
	// (3 + 7x + 2x^2) - (1 + 2x) = 2 + 5x + 2x^2
	if got := NewPoly(3, 7, 2).Sub(NewPoly(1, 2)); !polyEq(got, 2, 5, 2) {
		t.Errorf("sub = %s", got)
	}
	if got := NewPoly(1, 2).Add(NewPoly(3, 1)); !polyEq(got, 4, 3) {
		t.Errorf("add = %s", got)
	}
}

func TestPolyDeriv(t *testing.T) {
	// d/dx (5 - 2x + 3x^2 + 4x^3) = -2 + 6x + 12x^2
	if got := NewPoly(5, -2, 3, 4).Deriv(); !polyEq(got, -2, 6, 12) {
		t.Errorf("deriv = %s", got)
	}
	if got := NewPoly(7).Deriv(); !got.IsZero() {
		t.Errorf("deriv of constant = %s, want 0", got)
	}
}

func TestPolyEvalRat(t *testing.T) {
	// p = 2 + 3x + x^2, p(1/2) = 2 + 3/2 + 1/4 = 15/4
	p := NewPoly(2, 3, 1)
	got := p.EvalRat(big.NewRat(1, 2))
	want := big.NewRat(15, 4)
	if got.Cmp(want) != 0 {
		t.Errorf("p(1/2) = %s, want %s", got.RatString(), want.RatString())
	}
}

func TestPolyPrimitive(t *testing.T) {
	// content 2, leading sign flip
	p := NewPoly(-8, 14, -2)
	if got := p.Primitive(); !polyEq(got, 4, -7, 1) {
		t.Errorf("primitive = %s, want 4 - 7t + t^2", got)
	}
	if got := NewPoly(0).Primitive(); !got.IsZero() {
		t.Errorf("primitive of zero = %s", got)
	}
}

func TestPolyString(t *testing.T) {
	if got := NewPoly(4, -7, 1).String(); got != "t^2 - 7t + 4" {
		t.Errorf("String = %q", got)
	}
	if got := NewPoly(0).String(); got != "0" {
		t.Errorf("String of zero = %q", got)
	}
}
