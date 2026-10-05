package ratpoly

import (
	"fmt"
	"math/big"
)

// ipoly is a polynomial with integer coefficients, p[i] = coeff of x^i,
// normalized with no trailing zeros; nil is the zero polynomial.
// It backs the fraction-free subresultant computation.
type ipoly []*big.Int

func inorm(p ipoly) ipoly {
	n := len(p)
	for n > 0 && p[n-1].Sign() == 0 {
		n--
	}
	return p[:n]
}

func ideg(p ipoly) int { return len(p) - 1 }

func icopy(p ipoly) ipoly {
	q := make(ipoly, len(p))
	for i, c := range p {
		q[i] = new(big.Int).Set(c)
	}
	return q
}

func iadd(a, b ipoly) ipoly {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	c := make(ipoly, n)
	for i := range c {
		c[i] = new(big.Int)
		if i < len(a) {
			c[i].Add(c[i], a[i])
		}
		if i < len(b) {
			c[i].Add(c[i], b[i])
		}
	}
	return inorm(c)
}

func isub(a, b ipoly) ipoly {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	c := make(ipoly, n)
	for i := range c {
		c[i] = new(big.Int)
		if i < len(a) {
			c[i].Add(c[i], a[i])
		}
		if i < len(b) {
			c[i].Sub(c[i], b[i])
		}
	}
	return inorm(c)
}

func iscale(a ipoly, s *big.Int) ipoly {
	if s.Sign() == 0 {
		return nil
	}
	c := make(ipoly, len(a))
	for i, x := range a {
		c[i] = new(big.Int).Mul(x, s)
	}
	return inorm(c)
}

func imul(a, b ipoly) ipoly {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	c := make(ipoly, len(a)+len(b)-1)
	for i := range c {
		c[i] = new(big.Int)
	}
	t := new(big.Int)
	for i, x := range a {
		for j, y := range b {
			t.Mul(x, y)
			c[i+j].Add(c[i+j], t)
		}
	}
	return inorm(c)
}

// ishift multiplies p by x^k.
func ishift(p ipoly, k int) ipoly {
	if len(p) == 0 {
		return nil
	}
	c := make(ipoly, len(p)+k)
	for i := range c {
		c[i] = new(big.Int)
	}
	for i, x := range p {
		c[i+k].Set(x)
	}
	return c
}

// iexactDiv divides every coefficient by d; it panics unless exact.
func iexactDiv(a ipoly, d *big.Int) ipoly {
	if d.Sign() == 0 {
		panic("ratpoly: exact division by zero")
	}
	c := make(ipoly, len(a))
	r := new(big.Int)
	for i, x := range a {
		c[i] = new(big.Int)
		c[i].QuoRem(x, d, r)
		if r.Sign() != 0 {
			panic(fmt.Sprintf("ratpoly: inexact division of %v by %v", x, d))
		}
	}
	return inorm(c)
}

// imaxBitLen returns the maximum bit length of |coeff| over p.
func imaxBitLen(p ipoly) int {
	max := 0
	for _, c := range p {
		if n := c.BitLen(); n > max {
			max = n
		}
	}
	return max
}

// toIntPoly clears denominators: it returns an integer polynomial ip and a
// positive scale d with d*p = ip (over Q).
func toIntPoly(p Poly) (ip ipoly, d *big.Int) {
	d = big.NewInt(1)
	t := new(big.Int)
	for _, c := range p.c {
		t.Mul(d, c.Denom())
		g := new(big.Int).GCD(nil, nil, d, c.Denom())
		d.Quo(t, g)
	}
	ip = make(ipoly, len(p.c))
	q := new(big.Rat)
	for i, c := range p.c {
		q.SetInt(d)
		q.Mul(q, c)
		if !q.IsInt() {
			panic("ratpoly: denominator clearing failed")
		}
		ip[i] = new(big.Int).Set(q.Num())
	}
	return inorm(ip), d
}

// ipolyToPoly converts an integer polynomial to a rational one scaled by s.
func ipolyToPoly(p ipoly, s *big.Rat) Poly {
	c := make([]*big.Rat, len(p))
	for i, x := range p {
		c[i] = new(big.Rat).SetInt(x)
		c[i].Mul(c[i], s)
	}
	return normalize(c)
}

// pseudoDivRem computes q, r with lc(b)^(deg(a)-deg(b)+1)*a = q*b + r,
// deg(r) < deg(b), all over Z. Requires b != 0 and deg(a) >= deg(b).
func pseudoDivRem(a, b ipoly) (q, r ipoly) {
	m, n := ideg(a), ideg(b)
	if n < 0 {
		panic("ratpoly: pseudo-division by zero")
	}
	if m < n {
		panic("ratpoly: pseudo-division requires deg(a) >= deg(b)")
	}
	lb := b[n]
	r = icopy(a)
	q = nil
	e := m - n + 1
	for ideg(r) >= n {
		s := ishift(ipoly{new(big.Int).Set(r[len(r)-1])}, ideg(r)-n)
		q = iadd(iscale(q, lb), s)
		r = isub(iscale(r, lb), imul(s, b))
		e--
	}
	if e > 0 {
		pow := new(big.Int).Exp(lb, big.NewInt(int64(e)), nil)
		q = iscale(q, pow)
		r = iscale(r, pow)
	}
	return q, r
}
