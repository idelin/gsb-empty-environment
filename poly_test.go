package polygcd

import (
	"math/big"
	"testing"
)

func TestArithmetic(t *testing.T) {
	p := PolyFromInts(1, 2, 3) // 3x^2+2x+1
	q := PolyFromInts(-1, 1)   // x-1
	if got := Add(p, q); !got.Equal(PolyFromInts(0, 3, 3)) {
		t.Fatalf("add: %v", got)
	}
	if got := Sub(p, q); !got.Equal(PolyFromInts(2, 1, 3)) {
		t.Fatalf("sub: %v", got)
	}
	if got := Mul(p, q); !got.Equal(PolyFromInts(-1, -1, -1, 3)) {
		t.Fatalf("mul: %v", got)
	}
	quo, rem := DivMod(p, q)
	if !rem.Equal(PolyFromInts(6)) || !quo.Equal(PolyFromInts(5, 3)) {
		t.Fatalf("divmod: quo=%v rem=%v", quo, rem)
	}
	if !Add(Mul(quo, q), rem).Equal(p) {
		t.Fatal("division identity failed")
	}
}

func TestRationalsReduced(t *testing.T) {
	p := PolyFromRats("2/4", "-6/9")
	if p.Coeff(0).Cmp(big.NewRat(1, 2)) != 0 || p.Coeff(1).Cmp(big.NewRat(-2, 3)) != 0 {
		t.Fatalf("coefficients not reduced: %v", p)
	}
	// Arithmetic keeps fractions in lowest terms.
	r := Mul(PolyFromRats("1/2", "1/3"), PolyFromRats("1/5"))
	if !r.Equal(PolyFromRats("1/10", "1/15")) {
		t.Fatalf("mul: %v", r)
	}
}

func TestMonicAndZero(t *testing.T) {
	if !Zero().IsZero() || Zero().Deg() != -1 {
		t.Fatal("zero polynomial conventions broken")
	}
	p := PolyFromInts(2, 0, 4)
	if !p.Monic().Equal(PolyFromRats("1/2", "0", "1")) {
		t.Fatalf("monic: %v", p.Monic())
	}
	if !Zero().Monic().IsZero() {
		t.Fatal("monic of zero must be zero")
	}
}
