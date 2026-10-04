// Package ratbezier implements an exact extremum locator for 1D rational
// Bezier curves with weights. All arithmetic is exact rational arithmetic
// (math/big), so results are free of floating-point error.
package ratbezier

import (
	"fmt"
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with exact rational coefficients.
// c[i] holds the coefficient of t^i. The zero polynomial has len(c) == 0.
type Poly struct {
	c []*big.Rat
}

func rat(x int64) *big.Rat { return big.NewRat(x, 1) }

// NewPoly builds a polynomial from integer coefficients, lowest degree first.
func NewPoly(coeffs ...int64) *Poly {
	p := &Poly{c: make([]*big.Rat, len(coeffs))}
	for i, c := range coeffs {
		p.c[i] = rat(c)
	}
	return p.normalized()
}

func polyFromRats(coeffs []*big.Rat) *Poly {
	p := &Poly{c: make([]*big.Rat, len(coeffs))}
	for i, c := range coeffs {
		p.c[i] = new(big.Rat).Set(c)
	}
	return p.normalized()
}

func (p *Poly) normalized() *Poly {
	d := len(p.c) - 1
	for d >= 0 && p.c[d].Sign() == 0 {
		d--
	}
	p.c = p.c[:d+1]
	return p
}

// Degree returns the polynomial degree, or -1 for the zero polynomial.
func (p *Poly) Degree() int { return len(p.c) - 1 }

// IsZero reports whether p is the zero polynomial.
func (p *Poly) IsZero() bool { return len(p.c) == 0 }

// Coeff returns the coefficient of t^i (zero when out of range).
func (p *Poly) Coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.c) {
		return rat(0)
	}
	return p.c[i]
}

// Eval evaluates p at t exactly (Horner's scheme).
func (p *Poly) Eval(t *big.Rat) *big.Rat {
	res := rat(0)
	for i := len(p.c) - 1; i >= 0; i-- {
		res.Mul(res, t)
		res.Add(res, p.c[i])
	}
	return res
}

// Sign returns the sign of p(t): -1, 0 or +1.
func (p *Poly) Sign(t *big.Rat) int { return p.Eval(t).Sign() }

// Add returns p + q.
func (p *Poly) Add(q *Poly) *Poly {
	n := len(p.c)
	if len(q.c) > n {
		n = len(q.c)
	}
	r := make([]*big.Rat, n)
	for i := range r {
		r[i] = new(big.Rat).Add(p.Coeff(i), q.Coeff(i))
	}
	return polyFromRats(r)
}

// Neg returns -p.
func (p *Poly) Neg() *Poly {
	r := make([]*big.Rat, len(p.c))
	for i, c := range p.c {
		r[i] = new(big.Rat).Neg(c)
	}
	return polyFromRats(r)
}

// Mul returns p * q.
func (p *Poly) Mul(q *Poly) *Poly {
	if p.IsZero() || q.IsZero() {
		return &Poly{}
	}
	r := make([]*big.Rat, len(p.c)+len(q.c)-1)
	for i := range r {
		r[i] = rat(0)
	}
	for i, a := range p.c {
		for j, b := range q.c {
			r[i+j].Add(r[i+j], new(big.Rat).Mul(a, b))
		}
	}
	return polyFromRats(r)
}

// Scale returns s * p.
func (p *Poly) Scale(s *big.Rat) *Poly {
	r := make([]*big.Rat, len(p.c))
	for i, c := range p.c {
		r[i] = new(big.Rat).Mul(c, s)
	}
	return polyFromRats(r)
}

// Derivative returns p'.
func (p *Poly) Derivative() *Poly {
	if len(p.c) <= 1 {
		return &Poly{}
	}
	r := make([]*big.Rat, len(p.c)-1)
	for i := 1; i < len(p.c); i++ {
		r[i-1] = new(big.Rat).Mul(p.c[i], rat(int64(i)))
	}
	return polyFromRats(r)
}

// ComposeLinear returns the polynomial q(u) = p(a + b*u).
func (p *Poly) ComposeLinear(a, b *big.Rat) *Poly {
	res := &Poly{}
	lin := polyFromRats([]*big.Rat{a, b})
	pow := NewPoly(1)
	for i := 0; i <= p.Degree(); i++ {
		res = res.Add(pow.Scale(p.Coeff(i)))
		pow = pow.Mul(lin)
	}
	return res
}

// DescartesTransform returns r(s) = (1+s)^d * p(1/(1+s)) with d = deg(p).
// Roots of p in the open interval (0,1) correspond bijectively to positive
// roots of r, so Descartes' rule of signs applied to r bounds the number of
// roots of p in (0,1).
func (p *Poly) DescartesTransform() *Poly {
	d := p.Degree()
	res := &Poly{}
	onePlusS := polyFromRats([]*big.Rat{rat(1), rat(1)})
	for i := 0; i <= d; i++ {
		pow := NewPoly(1)
		for k := 0; k < d-i; k++ {
			pow = pow.Mul(onePlusS)
		}
		res = res.Add(pow.Scale(p.Coeff(i)))
	}
	return res
}

// SignVariations counts sign changes of the coefficient sequence,
// skipping zero coefficients.
func (p *Poly) SignVariations() int {
	v := 0
	prev := 0
	for _, c := range p.c {
		s := c.Sign()
		if s == 0 {
			continue
		}
		if prev != 0 && s != prev {
			v++
		}
		prev = s
	}
	return v
}

// ToIntPoly converts p to an integer-coefficient polynomial representing the
// same roots: coefficients are scaled by the LCM of denominators and divided
// by their content (gcd). Returned slice is lowest degree first.
func (p *Poly) ToIntPoly() []*big.Int {
	if p.IsZero() {
		return []*big.Int{big.NewInt(0)}
	}
	lcm := big.NewInt(1)
	tmp := new(big.Int)
	for _, c := range p.c {
		d := c.Denom()
		// lcm = lcm * d / gcd(lcm, d)
		g := tmp.GCD(nil, nil, lcm, d)
		lcm.Mul(lcm, d)
		lcm.Quo(lcm, g)
	}
	out := make([]*big.Int, len(p.c))
	content := new(big.Int)
	for i, c := range p.c {
		num := c.Num()
		out[i] = new(big.Int).Quo(new(big.Int).Mul(num, lcm), c.Denom())
		if i == 0 {
			content.Abs(out[i])
		} else {
			content.GCD(nil, nil, content, out[i])
		}
	}
	if content.Sign() != 0 {
		for i := range out {
			out[i].Quo(out[i], content)
		}
	}
	return out
}

func termBody(coeff string, i int) string {
	var v string
	switch i {
	case 0:
		v = ""
	case 1:
		v = "t"
	default:
		v = fmt.Sprintf("t^%d", i)
	}
	if coeff == "" {
		return v
	}
	if v == "" {
		return coeff
	}
	return coeff + "*" + v
}

// String renders the polynomial in ascending-degree exact form.
func (p *Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var sb strings.Builder
	first := true
	for i := 0; i <= p.Degree(); i++ {
		c := p.c[i]
		if c.Sign() == 0 {
			continue
		}
		cs := c.RatString()
		neg := c.Sign() < 0
		if neg {
			cs = cs[1:]
		}
		if cs == "1" && i > 0 {
			cs = ""
		}
		switch {
		case first && neg:
			sb.WriteString("-")
		case first:
		case neg:
			sb.WriteString(" - ")
		default:
			sb.WriteString(" + ")
		}
		sb.WriteString(termBody(cs, i))
		first = false
	}
	return sb.String()
}

// IntPolyString renders an integer coefficient slice as a polynomial string.
func IntPolyString(coeffs []*big.Int) string {
	rats := make([]*big.Rat, len(coeffs))
	for i, c := range coeffs {
		rats[i] = new(big.Rat).SetInt(c)
	}
	return polyFromRats(rats).String()
}
