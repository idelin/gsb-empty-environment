package main

import (
	"fmt"
	"math/big"
	"strings"
)

// poly is a univariate polynomial with exact rational coefficients in
// ascending order: p[i] is the coefficient of x^i. The zero polynomial is
// represented by an empty slice. Coefficients are never mutated in place;
// every operation allocates fresh *big.Rat values.
type poly []*big.Rat

func parseRat(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
	if !ok {
		return nil, fmt.Errorf("invalid rational %q", s)
	}
	return r, nil
}

func mustRat(s string) *big.Rat {
	r, err := parseRat(s)
	if err != nil {
		panic(err)
	}
	return r
}

// parsePoly parses ascending coefficients, e.g. ["1/2", "-2", "3"] is
// 1/2 - 2x + 3x^2.
func parsePoly(coeffs []string) (poly, error) {
	p := make(poly, len(coeffs))
	for i, c := range coeffs {
		r, err := parseRat(c)
		if err != nil {
			return nil, err
		}
		p[i] = r
	}
	return p.norm(), nil
}

func (p poly) norm() poly {
	n := len(p)
	for n > 0 && p[n-1].Sign() == 0 {
		n--
	}
	return p[:n]
}

func (p poly) deg() int { return len(p) - 1 }

func (p poly) isZero() bool { return len(p) == 0 }

func (p poly) lead() *big.Rat { return p[len(p)-1] }

func addPoly(a, b poly) poly {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make(poly, n)
	for i := 0; i < n; i++ {
		out[i] = new(big.Rat)
		if i < len(a) {
			out[i].Add(out[i], a[i])
		}
		if i < len(b) {
			out[i].Add(out[i], b[i])
		}
	}
	return out.norm()
}

func negPoly(a poly) poly {
	out := make(poly, len(a))
	for i, c := range a {
		out[i] = new(big.Rat).Neg(c)
	}
	return out
}

func subPoly(a, b poly) poly { return addPoly(a, negPoly(b)) }

func mulPoly(a, b poly) poly {
	if a.isZero() || b.isZero() {
		return nil
	}
	out := make(poly, len(a)+len(b)-1)
	for i := range out {
		out[i] = new(big.Rat)
	}
	for i, ca := range a {
		if ca.Sign() == 0 {
			continue
		}
		for j, cb := range b {
			if cb.Sign() == 0 {
				continue
			}
			t := new(big.Rat).Mul(ca, cb)
			out[i+j].Add(out[i+j], t)
		}
	}
	return out.norm()
}

func scalePoly(a poly, s *big.Rat) poly {
	out := make(poly, len(a))
	for i, c := range a {
		out[i] = new(big.Rat).Mul(c, s)
	}
	return out.norm()
}

func derivPoly(a poly) poly {
	if len(a) <= 1 {
		return nil
	}
	out := make(poly, len(a)-1)
	for i := 1; i < len(a); i++ {
		out[i-1] = new(big.Rat).Mul(a[i], big.NewRat(int64(i), 1))
	}
	return out.norm()
}

func divmodPoly(a, b poly) (poly, poly) {
	if b.isZero() {
		panic("polynomial division by zero")
	}
	rem := make(poly, len(a))
	copy(rem, a)
	rem = rem.norm()
	var quo poly
	if !rem.isZero() && rem.deg() >= b.deg() {
		quo = make(poly, rem.deg()-b.deg()+1)
		for i := range quo {
			quo[i] = new(big.Rat)
		}
	}
	for !rem.isZero() && rem.deg() >= b.deg() {
		d := rem.deg() - b.deg()
		c := new(big.Rat).Quo(rem.lead(), b.lead())
		quo[d] = c
		for j := 0; j < len(b); j++ {
			t := new(big.Rat).Mul(c, b[j])
			rem[j+d] = new(big.Rat).Sub(rem[j+d], t)
		}
		rem = rem.norm()
	}
	return quo.norm(), rem
}

func quoExact(a, b poly) poly {
	q, r := divmodPoly(a, b)
	if !r.isZero() {
		panic("inexact polynomial division")
	}
	return q
}

func monic(p poly) poly {
	if p.isZero() {
		return p
	}
	l := p.lead()
	out := make(poly, len(p))
	for i, c := range p {
		out[i] = new(big.Rat).Quo(c, l)
	}
	return out
}

func gcdPoly(a, b poly) poly {
	for !b.isZero() {
		_, r := divmodPoly(a, b)
		a, b = b, r
	}
	return monic(a)
}

func evalPoly(p poly, x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p[i])
	}
	return acc
}

// squarefreeFactors runs Yun's algorithm (characteristic 0) and returns
// factors a_1, a_2, ... such that f = c * prod a_i^i with each non-constant
// a_i squarefree and pairwise coprime. Constant factors may appear as 1.
func squarefreeFactors(f poly) []poly {
	if f.deg() < 1 {
		return nil
	}
	c := gcdPoly(f, derivPoly(f))
	w := quoExact(f, c)
	var factors []poly
	for w.deg() > 0 {
		y := gcdPoly(w, c)
		z := quoExact(w, y)
		factors = append(factors, monic(z))
		w = y
		c = quoExact(c, y)
	}
	return factors
}

func ratStr(r *big.Rat) string { return r.RatString() }

func (p poly) String() string {
	if p.isZero() {
		return "0"
	}
	var sb strings.Builder
	one := big.NewRat(1, 1)
	for i := p.deg(); i >= 0; i-- {
		c := p[i]
		if c.Sign() == 0 {
			continue
		}
		neg := c.Sign() < 0
		abs := new(big.Rat).Abs(c)
		var term string
		if i == 0 {
			term = ratStr(abs)
		} else {
			xs := "x"
			if i > 1 {
				xs = fmt.Sprintf("x^%d", i)
			}
			if abs.Cmp(one) == 0 {
				term = xs
			} else {
				term = ratStr(abs) + "*" + xs
			}
		}
		if sb.Len() == 0 {
			if neg {
				sb.WriteString("-")
			}
			sb.WriteString(term)
		} else {
			if neg {
				sb.WriteString(" - ")
			} else {
				sb.WriteString(" + ")
			}
			sb.WriteString(term)
		}
	}
	return sb.String()
}
