package main

import (
	"fmt"
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with exact rational coefficients.
// coeffs[i] is the coefficient of x^i. Canonical form has no trailing zeros,
// so the zero polynomial has len(coeffs) == 0.
type Poly struct {
	coeffs []*big.Rat
}

func NewPoly(coeffs ...*big.Rat) Poly {
	p := Poly{coeffs: coeffs}
	p.normalize()
	return p
}

// PolyFromInts builds a polynomial from integer coefficients, lowest degree first.
func PolyFromInts(coeffs ...int64) Poly {
	cs := make([]*big.Rat, len(coeffs))
	for i, c := range coeffs {
		cs[i] = big.NewRat(c, 1)
	}
	return NewPoly(cs...)
}

func (p *Poly) normalize() {
	n := len(p.coeffs)
	for n > 0 && p.coeffs[n-1].Sign() == 0 {
		n--
	}
	p.coeffs = p.coeffs[:n]
}

func (p Poly) IsZero() bool { return len(p.coeffs) == 0 }

// Degree returns -1 for the zero polynomial.
func (p Poly) Degree() int { return len(p.coeffs) - 1 }

func (p Poly) coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.coeffs) {
		return new(big.Rat)
	}
	return p.coeffs[i]
}

func (p Poly) LeadingCoeff() *big.Rat {
	if p.IsZero() {
		return new(big.Rat)
	}
	return p.coeffs[len(p.coeffs)-1]
}

func (p Poly) Add(q Poly) Poly {
	n := max(len(p.coeffs), len(q.coeffs))
	cs := make([]*big.Rat, n)
	for i := range cs {
		cs[i] = new(big.Rat).Add(p.coeff(i), q.coeff(i))
	}
	return NewPoly(cs...)
}

func (p Poly) Neg() Poly {
	cs := make([]*big.Rat, len(p.coeffs))
	for i, c := range p.coeffs {
		cs[i] = new(big.Rat).Neg(c)
	}
	return NewPoly(cs...)
}

func (p Poly) Sub(q Poly) Poly { return p.Add(q.Neg()) }

func (p Poly) Mul(q Poly) Poly {
	if p.IsZero() || q.IsZero() {
		return Poly{}
	}
	cs := make([]*big.Rat, len(p.coeffs)+len(q.coeffs)-1)
	for i := range cs {
		cs[i] = new(big.Rat)
	}
	for i, a := range p.coeffs {
		for j, b := range q.coeffs {
			t := new(big.Rat).Mul(a, b)
			cs[i+j].Add(cs[i+j], t)
		}
	}
	return NewPoly(cs...)
}

// Scale multiplies p by a rational constant.
func (p Poly) Scale(c *big.Rat) Poly {
	cs := make([]*big.Rat, len(p.coeffs))
	for i, a := range p.coeffs {
		cs[i] = new(big.Rat).Mul(a, c)
	}
	return NewPoly(cs...)
}

// DivMod returns quotient and remainder of p / d over Q[x].
func (p Poly) DivMod(d Poly) (Poly, Poly) {
	if d.IsZero() {
		panic("polynomial division by zero")
	}
	rem := make([]*big.Rat, len(p.coeffs))
	for i, c := range p.coeffs {
		rem[i] = new(big.Rat).Set(c)
	}
	degOf := func(cs []*big.Rat) int {
		for i := len(cs) - 1; i >= 0; i-- {
			if cs[i].Sign() != 0 {
				return i
			}
		}
		return -1
	}
	quot := make([]*big.Rat, 0)
	lcInv := new(big.Rat).Inv(d.LeadingCoeff())
	for dr := degOf(rem); dr >= d.Degree(); dr = degOf(rem) {
		shift := dr - d.Degree()
		factor := new(big.Rat).Mul(rem[dr], lcInv)
		for len(quot) <= shift {
			quot = append(quot, new(big.Rat))
		}
		quot[shift] = new(big.Rat).Set(factor)
		for j, c := range d.coeffs {
			t := new(big.Rat).Mul(factor, c)
			rem[shift+j].Sub(rem[shift+j], t)
		}
	}
	return NewPoly(quot...), NewPoly(rem...)
}

// GCD returns the monic greatest common divisor of p and q.
func (p Poly) GCD(q Poly) Poly {
	a, b := p, q
	for !b.IsZero() {
		_, r := a.DivMod(b)
		a, b = b, r
	}
	return a.Monic()
}

// Monic rescales p so its leading coefficient is 1.
func (p Poly) Monic() Poly {
	if p.IsZero() {
		return p
	}
	return p.Scale(new(big.Rat).Inv(p.LeadingCoeff()))
}

func (p Poly) Derivative() Poly {
	if p.Degree() < 1 {
		return Poly{}
	}
	cs := make([]*big.Rat, p.Degree())
	for i := 1; i < len(p.coeffs); i++ {
		cs[i-1] = new(big.Rat).Mul(p.coeffs[i], big.NewRat(int64(i), 1))
	}
	return NewPoly(cs...)
}

// SquareFree returns p / gcd(p, p'): the product of the distinct irreducible
// factors of p. It has exactly the same distinct roots as p, each simple.
func (p Poly) SquareFree() Poly {
	g := p.GCD(p.Derivative())
	q, _ := p.DivMod(g)
	return q
}

// Eval evaluates p at x exactly (Horner's scheme over big.Rat).
func (p Poly) Eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p.coeffs[i])
	}
	return acc
}

func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var terms []string
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		c := p.coeffs[i]
		if c.Sign() == 0 {
			continue
		}
		sign := "+"
		mag := new(big.Rat).Abs(c)
		if c.Sign() < 0 {
			sign = "-"
		}
		var body string
		switch i {
		case 0:
			body = ratStr(mag)
		case 1:
			if mag.Cmp(big.NewRat(1, 1)) == 0 {
				body = "x"
			} else {
				body = ratStr(mag) + "*x"
			}
		default:
			if mag.Cmp(big.NewRat(1, 1)) == 0 {
				body = fmt.Sprintf("x^%d", i)
			} else {
				body = fmt.Sprintf("%s*x^%d", ratStr(mag), i)
			}
		}
		terms = append(terms, sign+" "+body)
	}
	s := strings.Join(terms, " ")
	if strings.HasPrefix(s, "+ ") {
		s = s[2:]
	}
	return s
}

// ratStr formats a rational without a "/1" denominator.
func ratStr(r *big.Rat) string { return r.RatString() }
