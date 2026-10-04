package domain

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat: " + s)
	}
	return r
}

func TestRatPowBound(t *testing.T) {
	// sqrt(2) is bracketed by exact rationals, never floats.
	lo := ratPowBound(rat("2"), 1, 2, false)
	hi := ratPowBound(rat("2"), 1, 2, true)
	if lo.Cmp(rat("1")) <= 0 || lo.Cmp(rat("3/2")) >= 0 {
		t.Errorf("lower bound of sqrt(2) = %s, want in (1, 3/2)", lo)
	}
	if hi.Cmp(lo) < 0 || hi.Cmp(rat("3/2")) >= 0 {
		t.Errorf("upper bound of sqrt(2) = %s, want in [lo, 3/2)", hi)
	}
	// lo^2 <= 2 <= hi^2 must hold exactly.
	if ratPowInt(lo, 2).Cmp(rat("2")) > 0 {
		t.Errorf("lo^2 = %s > 2", ratPowInt(lo, 2))
	}
	if ratPowInt(hi, 2).Cmp(rat("2")) < 0 {
		t.Errorf("hi^2 = %s < 2", ratPowInt(hi, 2))
	}
	// Perfect powers are (nearly) exact: 8^(1/3) bounds bracket 2.
	lo = ratPowBound(rat("8"), 1, 3, false)
	hi = ratPowBound(rat("8"), 1, 3, true)
	if lo.Cmp(rat("2")) > 0 || hi.Cmp(rat("2")) < 0 {
		t.Errorf("8^(1/3) bounds [%s, %s] do not bracket 2", lo, hi)
	}
	// Cube root of a negative via signedPowBound: (-27)^(1/3) = -3.
	neg := signedPowBound(rat("-27"), 1, 3, false)
	if neg.Cmp(rat("-3")) > 0 || neg.Cmp(rat("-4")) <= 0 {
		t.Errorf("(-27)^(1/3) lower bound = %s, want in (-4, -3]", neg)
	}
	// Even numerator with odd denominator: (-8)^(2/3) = 4.
	pos := signedPowBound(rat("-8"), 2, 3, true)
	if pos.Cmp(rat("4")) < 0 || pos.Cmp(rat("5")) >= 0 {
		t.Errorf("(-8)^(2/3) upper bound = %s, want in [4, 5)", pos)
	}
}

func TestEvalInterval(t *testing.T) {
	iv := Iv(rat("-2"), rat("3"))
	x := Var("x")

	b, ok := evalInterval(Mul(x, x), iv)
	if !ok || b.lo.Sign() != 0 || b.hi.Cmp(rat("9")) != 0 {
		t.Errorf("x*x on [-2,3] = [%s, %s], want [0, 9]", b.lo, b.hi)
	}

	b, ok = evalInterval(Pow(x, 2, 1), iv)
	if !ok || b.lo.Sign() != 0 || b.hi.Cmp(rat("9")) != 0 {
		t.Errorf("x^2 on [-2,3] = [%s, %s], want [0, 9]", b.lo, b.hi)
	}

	b, ok = evalInterval(Pow(x, 3, 1), iv)
	if !ok || b.lo.Cmp(rat("-8")) != 0 || b.hi.Cmp(rat("27")) != 0 {
		t.Errorf("x^3 on [-2,3] = [%s, %s], want [-8, 27]", b.lo, b.hi)
	}

	// 1/x straddles zero: no finite enclosure.
	if _, ok = evalInterval(Div(ConstInt(1), x), iv); ok {
		t.Error("1/x on [-2,3] should not have a finite enclosure")
	}

	// x^(1/2) on [-1, 4]: negative base possible, not ok.
	if _, ok = evalInterval(Pow(x, 1, 2), iv); ok {
		t.Error("x^(1/2) on [-2,3] should fail (negative base)")
	}

	// x^(1/2) on [0, 4] = [0, 2] exactly.
	b, ok = evalInterval(Pow(x, 1, 2), Iv(rat("0"), rat("4")))
	if !ok || b.lo.Sign() != 0 || b.hi.Cmp(rat("2")) != 0 {
		t.Errorf("x^(1/2) on [0,4] = [%s, %s], want [0, 2]", b.lo, b.hi)
	}

	// x^(-1) on [1, 2] = [1/2, 1].
	b, ok = evalInterval(Pow(x, -1, 1), Iv(rat("1"), rat("2")))
	if !ok || b.lo.Cmp(rat("1/2")) != 0 || b.hi.Cmp(rat("1")) != 0 {
		t.Errorf("x^(-1) on [1,2] = [%s, %s], want [1/2, 1]", b.lo, b.hi)
	}
}
