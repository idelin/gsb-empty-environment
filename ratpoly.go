package ratbezier

import "math/big"

// ratPoly is a univariate polynomial with exact rational coefficients.
// c[i] holds the coefficient of x^i; the zero polynomial has no coefficients.
type ratPoly struct {
	c []*big.Rat
}

func ratPolyFromPoly(p *Poly) *ratPoly {
	c := make([]*big.Rat, len(p.c))
	for i, a := range p.c {
		c[i] = new(big.Rat).SetInt(a)
	}
	return &ratPoly{c: c}
}

func (q *ratPoly) normalize() {
	n := len(q.c)
	for n > 0 && q.c[n-1].Sign() == 0 {
		n--
	}
	q.c = q.c[:n]
}

func (q *ratPoly) degree() int { return len(q.c) - 1 }

func (q *ratPoly) isZero() bool { return len(q.c) == 0 }

func (q *ratPoly) coeff(i int) *big.Rat {
	if i < 0 || i >= len(q.c) {
		return new(big.Rat)
	}
	return q.c[i]
}

func (q *ratPoly) clone() *ratPoly {
	c := make([]*big.Rat, len(q.c))
	for i, a := range q.c {
		c[i] = new(big.Rat).Set(a)
	}
	return &ratPoly{c: c}
}

func (q *ratPoly) add(r *ratPoly) *ratPoly {
	n := len(q.c)
	if len(r.c) > n {
		n = len(r.c)
	}
	c := make([]*big.Rat, n)
	for i := range c {
		c[i] = new(big.Rat).Add(q.coeff(i), r.coeff(i))
	}
	out := &ratPoly{c: c}
	out.normalize()
	return out
}

func (q *ratPoly) sub(r *ratPoly) *ratPoly {
	return q.add(r.neg())
}

func (q *ratPoly) mul(r *ratPoly) *ratPoly {
	if q.isZero() || r.isZero() {
		return &ratPoly{}
	}
	c := make([]*big.Rat, len(q.c)+len(r.c)-1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	for i, a := range q.c {
		for j, b := range r.c {
			c[i+j].Add(c[i+j], new(big.Rat).Mul(a, b))
		}
	}
	out := &ratPoly{c: c}
	out.normalize()
	return out
}

func (q *ratPoly) scale(s *big.Rat) *ratPoly {
	c := make([]*big.Rat, len(q.c))
	for i, a := range q.c {
		c[i] = new(big.Rat).Mul(a, s)
	}
	out := &ratPoly{c: c}
	out.normalize()
	return out
}

func (q *ratPoly) neg() *ratPoly {
	return q.scale(big.NewRat(-1, 1))
}

func (q *ratPoly) deriv() *ratPoly {
	if len(q.c) <= 1 {
		return &ratPoly{}
	}
	c := make([]*big.Rat, len(q.c)-1)
	for i := 1; i < len(q.c); i++ {
		c[i-1] = new(big.Rat).Mul(q.c[i], big.NewRat(int64(i), 1))
	}
	out := &ratPoly{c: c}
	out.normalize()
	return out
}

// rem returns the remainder of q divided by r over the rationals.
func (q *ratPoly) rem(r *ratPoly) *ratPoly {
	rem := q.clone()
	rd := r.degree()
	if rd < 0 {
		return rem
	}
	rlc := r.c[rd]
	for !rem.isZero() && rem.degree() >= rd {
		shift := rem.degree() - rd
		factor := new(big.Rat).Quo(rem.c[rem.degree()], rlc)
		for j := 0; j <= rd; j++ {
			idx := j + shift
			rem.c[idx].Sub(rem.c[idx], new(big.Rat).Mul(factor, r.c[j]))
		}
		rem.normalize()
	}
	return rem
}

func (q *ratPoly) eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(q.c) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, q.c[i])
	}
	return acc
}

// toPoly clears denominators (multiply by the LCM of all coefficient
// denominators) and divides by the content. The sign is preserved, so
// the result has the same sign as q at every point.
func (q *ratPoly) toPoly() *Poly {
	if q.isZero() {
		return &Poly{}
	}
	lcm := big.NewInt(1)
	for _, c := range q.c {
		d := c.Denom()
		g := new(big.Int).GCD(nil, nil, lcm, d)
		lcm.Quo(lcm, g).Mul(lcm, d)
	}
	c := make([]*big.Int, len(q.c))
	for i, a := range q.c {
		t := new(big.Rat).Mul(a, new(big.Rat).SetInt(lcm))
		c[i] = new(big.Int).Set(t.Num())
	}
	return polyFromBig(c).primpart()
}
