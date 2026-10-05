package main

import (
	"fmt"
	"math/big"
	"strings"
)

// ProvTerm records one source constraint and the positive multiplier that
// was applied to it when deriving a new constraint.
type ProvTerm struct {
	ID   string
	Mult *big.Rat
}

// Ineq represents Coeffs . x <= RHS, or Coeffs . x < RHS when Strict.
type Ineq struct {
	Coeffs []*big.Rat
	RHS    *big.Rat
	Strict bool
	Prov   [][]ProvTerm
}

func newIneq(coeffs []*big.Rat, rhs *big.Rat, strict bool, id string) *Ineq {
	return &Ineq{
		Coeffs: coeffs,
		RHS:    rhs,
		Strict: strict,
		Prov:   [][]ProvTerm{{{ID: id, Mult: big.NewRat(1, 1)}}},
	}
}

func (in *Ineq) isZero() bool {
	for _, c := range in.Coeffs {
		if c.Sign() != 0 {
			return false
		}
	}
	return true
}

// triviallyTrue reports whether a zero-coefficient constraint always holds.
func (in *Ineq) triviallyTrue() bool {
	if in.Strict {
		return in.RHS.Sign() > 0
	}
	return in.RHS.Sign() >= 0
}

func (in *Ineq) opString() string {
	if in.Strict {
		return "<"
	}
	return "<="
}

func (in *Ineq) String() string {
	parts := make([]string, len(in.Coeffs))
	for i, c := range in.Coeffs {
		parts[i] = c.RatString()
	}
	return fmt.Sprintf("(%s) . x %s %s", strings.Join(parts, ","), in.opString(), in.RHS.RatString())
}

func lcm(a, b *big.Int) *big.Int {
	if a.Sign() == 0 || b.Sign() == 0 {
		return big.NewInt(0)
	}
	g := new(big.Int).GCD(nil, nil, a, b)
	q := new(big.Int).Quo(a, g)
	return q.Mul(q, b)
}

func scaleRatToInt(r *big.Rat, l *big.Int) *big.Int {
	q := new(big.Int).Quo(l, r.Denom())
	return q.Mul(q, r.Num())
}

// normKey scales (coeffs, rhs) by a positive factor so all numbers become
// integers with gcd 1. The <= direction is preserved, so the key is a
// canonical representative of positive rescaling equivalence classes.
func normKey(coeffs []*big.Rat, rhs *big.Rat) string {
	l := big.NewInt(1)
	for _, c := range coeffs {
		l = lcm(l, c.Denom())
	}
	l = lcm(l, rhs.Denom())
	ints := make([]*big.Int, 0, len(coeffs)+1)
	for _, c := range coeffs {
		ints = append(ints, scaleRatToInt(c, l))
	}
	ints = append(ints, scaleRatToInt(rhs, l))
	g := big.NewInt(0)
	for _, v := range ints {
		a := new(big.Int).Abs(v)
		g.GCD(nil, nil, g, a)
	}
	if g.Sign() == 0 {
		g.SetInt64(1)
	}
	parts := make([]string, len(ints))
	for i, v := range ints {
		parts[i] = new(big.Int).Quo(v, g).String()
	}
	return strings.Join(parts, ",")
}

func canonicalKey(in *Ineq) string {
	s := "n"
	if in.Strict {
		s = "s"
	}
	return normKey(in.Coeffs, in.RHS) + "|" + s
}

func coeffKey(coeffs []*big.Rat) string {
	return normKey(coeffs, new(big.Rat))
}

func negCoeffs(coeffs []*big.Rat) []*big.Rat {
	out := make([]*big.Rat, len(coeffs))
	for i, c := range coeffs {
		out[i] = new(big.Rat).Neg(c)
	}
	return out
}

func zeroVector(n int) []*big.Rat {
	v := make([]*big.Rat, n)
	for i := range v {
		v[i] = new(big.Rat)
	}
	return v
}

func unitVector(n, j int) []*big.Rat {
	v := zeroVector(n)
	v[j] = big.NewRat(1, 1)
	return v
}
