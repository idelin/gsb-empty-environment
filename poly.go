package ratbezier

import (
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with integer coefficients.
// c[i] holds the coefficient of x^i; the zero polynomial has no coefficients.
type Poly struct {
	c []*big.Int
}

// NewPoly builds a polynomial from int64 coefficients, lowest degree first.
func NewPoly(coeffs ...int64) *Poly {
	c := make([]*big.Int, len(coeffs))
	for i, v := range coeffs {
		c[i] = big.NewInt(v)
	}
	return polyFromBig(c)
}

func polyFromBig(c []*big.Int) *Poly {
	p := &Poly{c: c}
	p.normalize()
	return p
}

func (p *Poly) normalize() {
	n := len(p.c)
	for n > 0 && p.c[n-1].Sign() == 0 {
		n--
	}
	p.c = p.c[:n]
}

// Degree returns the polynomial degree, or -1 for the zero polynomial.
func (p *Poly) Degree() int { return len(p.c) - 1 }

// IsZero reports whether p is the zero polynomial.
func (p *Poly) IsZero() bool { return len(p.c) == 0 }

// Coeff returns a copy of the coefficient of x^i (zero when out of range).
func (p *Poly) Coeff(i int) *big.Int {
	if i < 0 || i >= len(p.c) {
		return new(big.Int)
	}
	return new(big.Int).Set(p.c[i])
}

// Coeffs returns a copy of all coefficients, lowest degree first.
func (p *Poly) Coeffs() []*big.Int {
	out := make([]*big.Int, len(p.c))
	for i, c := range p.c {
		out[i] = new(big.Int).Set(c)
	}
	return out
}

func (p *Poly) Add(q *Poly) *Poly {
	n := len(p.c)
	if len(q.c) > n {
		n = len(q.c)
	}
	c := make([]*big.Int, n)
	for i := range c {
		c[i] = new(big.Int).Add(p.Coeff(i), q.Coeff(i))
	}
	return polyFromBig(c)
}

func (p *Poly) Sub(q *Poly) *Poly {
	n := len(p.c)
	if len(q.c) > n {
		n = len(q.c)
	}
	c := make([]*big.Int, n)
	for i := range c {
		c[i] = new(big.Int).Sub(p.Coeff(i), q.Coeff(i))
	}
	return polyFromBig(c)
}

func (p *Poly) Mul(q *Poly) *Poly {
	if p.IsZero() || q.IsZero() {
		return &Poly{}
	}
	c := make([]*big.Int, len(p.c)+len(q.c)-1)
	for i := range c {
		c[i] = new(big.Int)
	}
	for i, a := range p.c {
		for j, b := range q.c {
			c[i+j].Add(c[i+j], new(big.Int).Mul(a, b))
		}
	}
	return polyFromBig(c)
}

func (p *Poly) Scale(s *big.Int) *Poly {
	c := make([]*big.Int, len(p.c))
	for i, a := range p.c {
		c[i] = new(big.Int).Mul(a, s)
	}
	return polyFromBig(c)
}

func (p *Poly) Neg() *Poly { return p.Scale(big.NewInt(-1)) }

// Deriv returns the formal derivative of p.
func (p *Poly) Deriv() *Poly {
	if len(p.c) <= 1 {
		return &Poly{}
	}
	c := make([]*big.Int, len(p.c)-1)
	for i := 1; i < len(p.c); i++ {
		c[i-1] = new(big.Int).Mul(p.c[i], big.NewInt(int64(i)))
	}
	return polyFromBig(c)
}

// EvalRat evaluates p at a rational point using Horner's rule.
func (p *Poly) EvalRat(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p.c) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, new(big.Rat).SetInt(p.c[i]))
	}
	return acc
}

// Content returns the greatest common divisor of all coefficients.
func (p *Poly) Content() *big.Int {
	g := new(big.Int)
	for _, c := range p.c {
		if c.Sign() == 0 {
			continue
		}
		if g.Sign() == 0 {
			g.Abs(c)
			continue
		}
		g.GCD(nil, nil, g, new(big.Int).Abs(c))
	}
	return g
}

// primpart divides p by its content, preserving the sign of p.
func (p *Poly) primpart() *Poly {
	if p.IsZero() {
		return &Poly{}
	}
	g := p.Content()
	if g.Sign() == 0 {
		g.SetInt64(1)
	}
	c := make([]*big.Int, len(p.c))
	for i, a := range p.c {
		c[i] = new(big.Int).Quo(a, g)
	}
	return polyFromBig(c)
}

// Primitive divides p by its content and makes the leading coefficient
// positive, yielding the canonical primitive representative.
func (p *Poly) Primitive() *Poly {
	if p.IsZero() {
		return &Poly{}
	}
	q := p.primpart()
	if q.Coeff(q.Degree()).Sign() < 0 {
		q = q.Neg()
	}
	return q
}

func (p *Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var terms []string
	for i := len(p.c) - 1; i >= 0; i-- {
		c := p.c[i]
		if c.Sign() == 0 {
			continue
		}
		abs := new(big.Int).Abs(c)
		var b strings.Builder
		if c.Sign() < 0 {
			b.WriteByte('-')
		}
		if i == 0 || abs.Cmp(big.NewInt(1)) != 0 {
			b.WriteString(abs.String())
		}
		if i > 0 {
			b.WriteByte('t')
			if i > 1 {
				b.WriteByte('^')
				b.WriteString(big.NewInt(int64(i)).String())
			}
		}
		terms = append(terms, b.String())
	}
	out := terms[0]
	for _, t := range terms[1:] {
		if strings.HasPrefix(t, "-") {
			out += " - " + t[1:]
		} else {
			out += " + " + t
		}
	}
	return out
}
