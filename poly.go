package main

import "math/big"

// Poly is a univariate polynomial with rational coefficients,
// little-endian: p[i] is the coefficient of t^i. Normalized form
// has no trailing zeros; the zero polynomial is the empty slice.
type Poly []*big.Rat

func ratI(i int64) *big.Rat { return big.NewRat(i, 1) }

func parseRat(s string) (*big.Rat, bool) { return new(big.Rat).SetString(s) }

func mustRat(s string) *big.Rat {
	r, ok := parseRat(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func norm(p Poly) Poly {
	n := len(p)
	for n > 0 && p[n-1].Sign() == 0 {
		n--
	}
	return p[:n]
}

func polyInts(c ...int64) Poly {
	p := make(Poly, len(c))
	for i, v := range c {
		p[i] = ratI(v)
	}
	return norm(p)
}

func (p Poly) Deg() int   { return len(p) - 1 }
func (p Poly) Zero() bool { return len(p) == 0 }
func (p Poly) LC() *big.Rat {
	if len(p) == 0 {
		return ratI(0)
	}
	return p[len(p)-1]
}

func (p Poly) Get(i int) *big.Rat {
	if i < 0 || i >= len(p) {
		return ratI(0)
	}
	return p[i]
}

func (p Poly) clone() Poly {
	r := make(Poly, len(p))
	for i, c := range p {
		r[i] = new(big.Rat).Set(c)
	}
	return r
}

func addP(a, b Poly) Poly {
	n := max(len(a), len(b))
	r := make(Poly, n)
	for i := range r {
		r[i] = new(big.Rat).Add(a.Get(i), b.Get(i))
	}
	return norm(r)
}

func negP(a Poly) Poly {
	r := make(Poly, len(a))
	for i, c := range a {
		r[i] = new(big.Rat).Neg(c)
	}
	return r
}

func subP(a, b Poly) Poly { return addP(a, negP(b)) }

func mulP(a, b Poly) Poly {
	if a.Zero() || b.Zero() {
		return Poly{}
	}
	r := make(Poly, len(a)+len(b)-1)
	for i := range r {
		r[i] = ratI(0)
	}
	for i, ca := range a {
		if ca.Sign() == 0 {
			continue
		}
		for j, cb := range b {
			if cb.Sign() == 0 {
				continue
			}
			r[i+j].Add(r[i+j], new(big.Rat).Mul(ca, cb))
		}
	}
	return norm(r)
}

func scaleP(a Poly, s *big.Rat) Poly {
	r := make(Poly, len(a))
	for i, c := range a {
		r[i] = new(big.Rat).Mul(c, s)
	}
	return norm(r)
}

func derivP(a Poly) Poly {
	if len(a) <= 1 {
		return Poly{}
	}
	r := make(Poly, len(a)-1)
	for i := 1; i < len(a); i++ {
		r[i-1] = new(big.Rat).Mul(a[i], ratI(int64(i)))
	}
	return norm(r)
}

// divModP returns quotient and remainder of a/b over the rationals.
func divModP(a, b Poly) (q, r Poly) {
	if b.Zero() {
		panic("polynomial division by zero")
	}
	r = a.clone()
	q = make(Poly, max(0, len(a)-len(b)+1))
	for i := range q {
		q[i] = ratI(0)
	}
	blc := b.LC()
	for !r.Zero() && len(r) >= len(b) {
		k := len(r) - len(b)
		c := new(big.Rat).Quo(r.LC(), blc)
		q[k] = new(big.Rat).Set(c)
		sub := make(Poly, k+len(b))
		for i := range sub {
			sub[i] = ratI(0)
		}
		for j, cb := range b {
			sub[k+j] = new(big.Rat).Mul(c, cb)
		}
		r = norm(subP(r, sub))
	}
	return norm(q), r
}

// gcdP returns the monic gcd of a and b.
func gcdP(a, b Poly) Poly {
	for !b.Zero() {
		_, r := divModP(a, b)
		a, b = b, r
	}
	if a.Zero() {
		return a
	}
	return scaleP(a, new(big.Rat).Inv(a.LC()))
}

func (p Poly) eval(x *big.Rat) *big.Rat {
	r := ratI(0)
	for i := len(p) - 1; i >= 0; i-- {
		r.Mul(r, x)
		r.Add(r, p[i])
	}
	return r
}

// squareFreeP returns p / gcd(p, p'): same distinct roots, all simple.
func squareFreeP(p Poly) Poly {
	q, _ := divModP(p, gcdP(p, derivP(p)))
	return q
}

// clearDenoms scales to primitive integer coefficients with positive
// leading coefficient (canonical representative for certificates).
func clearDenoms(p Poly) Poly {
	if p.Zero() {
		return Poly{}
	}
	lcm := big.NewInt(1)
	for _, c := range p {
		d := c.Denom()
		g := new(big.Int).GCD(nil, nil, lcm, d)
		lcm.Mul(lcm, new(big.Int).Quo(d, g))
	}
	ints := make([]*big.Int, len(p))
	for i, c := range p {
		ints[i] = new(big.Int).Mul(c.Num(), new(big.Int).Quo(lcm, c.Denom()))
	}
	g := new(big.Int)
	for _, v := range ints {
		g.GCD(nil, nil, g, v)
	}
	if g.Sign() == 0 {
		g.SetInt64(1)
	}
	g.Abs(g)
	r := make(Poly, len(p))
	for i, v := range ints {
		r[i] = new(big.Rat).SetInt(new(big.Int).Quo(v, g))
	}
	r = norm(r)
	if r.LC().Sign() < 0 {
		r = negP(r)
	}
	return r
}

func polyStrings(p Poly) []string {
	s := make([]string, len(p))
	for i, c := range p {
		s[i] = c.RatString()
	}
	return s
}
