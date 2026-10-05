package main

import (
	"fmt"
	"math/big"
)

// Poly is a quadratic polynomial a*t^2 + b*t + c with rational coefficients.
// Fields are never mutated after construction.
type Poly struct {
	a, b, c *big.Rat
}

func mkPoly(a, b, c *big.Rat) Poly {
	return Poly{a: new(big.Rat).Set(a), b: new(big.Rat).Set(b), c: new(big.Rat).Set(c)}
}

func (p Poly) IsZero() bool {
	return p.a.Sign() == 0 && p.b.Sign() == 0 && p.c.Sign() == 0
}

func (p Poly) Sub(q Poly) Poly {
	return Poly{
		a: new(big.Rat).Sub(p.a, q.a),
		b: new(big.Rat).Sub(p.b, q.b),
		c: new(big.Rat).Sub(p.c, q.c),
	}
}

// EvalRat evaluates p at a rational point, exactly.
func (p Poly) EvalRat(t *big.Rat) *big.Rat {
	r := new(big.Rat).Mul(p.a, t)
	r.Add(r, p.b)
	r.Mul(r, t)
	r.Add(r, p.c)
	return r
}

// EvalAlg evaluates p at t = u + v*sqrt(m). The result stays in Q(sqrt(m)):
// t^2 = (u^2 + v^2 m) + 2uv*sqrt(m).
func (p Poly) EvalAlg(t *AlgNum) *AlgNum {
	u2 := new(big.Rat).Mul(t.u, t.u)
	v2m := new(big.Rat).Mul(t.v, t.v)
	v2m.Mul(v2m, new(big.Rat).SetInt(t.m))
	two := big.NewRat(2, 1)
	twoUv := new(big.Rat).Mul(two, new(big.Rat).Mul(t.u, t.v))

	re := new(big.Rat).Add(u2, v2m)
	re.Mul(re, p.a)
	re.Add(re, new(big.Rat).Mul(p.b, t.u))
	re.Add(re, p.c)

	im := new(big.Rat).Mul(p.a, twoUv)
	im.Add(im, new(big.Rat).Mul(p.b, t.v))

	return mkAlg(re, im, t.m)
}

// Roots returns all real roots of p, exactly, in increasing order.
// A zero polynomial (coincident distance functions) has no roots reported:
// it never changes any pairwise order.
func (p Poly) Roots() []*AlgNum {
	if p.a.Sign() == 0 {
		if p.b.Sign() == 0 {
			return nil
		}
		// Linear: t = -c/b.
		return []*AlgNum{ratAlg(new(big.Rat).Neg(new(big.Rat).Quo(p.c, p.b)))}
	}
	// disc = b^2 - 4ac
	disc := new(big.Rat).Mul(p.b, p.b)
	ac := new(big.Rat).Mul(p.a, p.c)
	disc.Sub(disc, ac.Mul(ac, big.NewRat(4, 1)))
	if disc.Sign() < 0 {
		return nil
	}
	twoA := new(big.Rat).Mul(p.a, big.NewRat(2, 1))
	u := new(big.Rat).Quo(new(big.Rat).Neg(p.b), twoA)
	if disc.Sign() == 0 {
		// Tangency: a double root; the order does not swap but the exact
		// ranking at this instant may still change (ties broken by ID).
		return []*AlgNum{ratAlg(u)}
	}
	coeff, m := sqrtDecomp(disc) // sqrt(disc) = coeff*sqrt(m)
	v := new(big.Rat).Quo(coeff, twoA)
	r1 := mkAlg(u, new(big.Rat).Set(v), m)
	r2 := mkAlg(u, new(big.Rat).Neg(v), m)
	if r1.Cmp(r2) > 0 {
		r1, r2 = r2, r1
	}
	return []*AlgNum{r1, r2}
}

func (p Poly) String() string {
	return fmt.Sprintf("(%s)t^2 + (%s)t + (%s)", p.a.RatString(), p.b.RatString(), p.c.RatString())
}
