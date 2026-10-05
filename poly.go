// Package algcmp implements exact comparison of real algebraic numbers.
//
// A real algebraic number is represented by a nonzero integer-coefficient
// polynomial together with an open isolating interval with rational
// endpoints that contains exactly one real root of the polynomial (and
// whose endpoints are not roots).  All arithmetic is exact rational
// arithmetic (math/big); no floating-point approximation is ever used to
// decide an ordering.
package algcmp

import (
	"fmt"
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with rational coefficients.
// c[i] holds the coefficient of x^i.  Invariant: c is empty (the zero
// polynomial) or the leading coefficient c[len(c)-1] is nonzero.
type Poly struct {
	c []*big.Rat
}

// NewPoly builds a polynomial from coefficients in ascending degree order.
func NewPoly(coeffs ...*big.Rat) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, x := range coeffs {
		if x == nil {
			x = new(big.Rat)
		}
		c[i] = new(big.Rat).Set(x)
	}
	return Poly{c: c}.norm()
}

// PolyFromInts builds a polynomial from integer coefficients
// in ascending degree order.
func PolyFromInts(coeffs ...int64) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, v := range coeffs {
		c[i] = new(big.Rat).SetInt64(v)
	}
	return Poly{c: c}.norm()
}

// ParsePoly parses integer coefficients given as decimal strings
// in ascending degree order.  Non-integer coefficients are rejected.
func ParsePoly(tokens []string) (Poly, error) {
	if len(tokens) == 0 {
		return Poly{}, fmt.Errorf("polynomial must have at least one coefficient")
	}
	c := make([]*big.Rat, len(tokens))
	for i, s := range tokens {
		r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
		if !ok {
			return Poly{}, fmt.Errorf("coefficient %d: %q is not a valid rational", i, s)
		}
		if r.Denom().Cmp(big.NewInt(1)) != 0 {
			return Poly{}, fmt.Errorf("coefficient %d (%s) is not an integer", i, s)
		}
		c[i] = r
	}
	return Poly{c: c}.norm(), nil
}

func (p Poly) norm() Poly {
	n := len(p.c)
	for n > 0 && p.c[n-1].Sign() == 0 {
		n--
	}
	return Poly{c: p.c[:n]}
}

func (p Poly) copy() Poly {
	c := make([]*big.Rat, len(p.c))
	for i, x := range p.c {
		c[i] = new(big.Rat).Set(x)
	}
	return Poly{c: c}
}

// IsZero reports whether p is the zero polynomial.
func (p Poly) IsZero() bool { return len(p.c) == 0 }

// Deg returns the degree of p; the zero polynomial has degree -1.
func (p Poly) Deg() int { return len(p.c) - 1 }

func (p Poly) lead() *big.Rat { return p.c[len(p.c)-1] }

// Coeff returns the coefficient of x^i (zero when out of range).
func (p Poly) Coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.c) {
		return new(big.Rat)
	}
	return new(big.Rat).Set(p.c[i])
}

// Add returns p + q.
func (p Poly) Add(q Poly) Poly {
	n := len(p.c)
	if len(q.c) > n {
		n = len(q.c)
	}
	c := make([]*big.Rat, n)
	for i := range c {
		c[i] = new(big.Rat)
		if i < len(p.c) {
			c[i].Add(c[i], p.c[i])
		}
		if i < len(q.c) {
			c[i].Add(c[i], q.c[i])
		}
	}
	return Poly{c: c}.norm()
}

// Neg returns -p.
func (p Poly) Neg() Poly {
	c := make([]*big.Rat, len(p.c))
	for i, x := range p.c {
		c[i] = new(big.Rat).Neg(x)
	}
	return Poly{c: c}
}

// Sub returns p - q.
func (p Poly) Sub(q Poly) Poly { return p.Add(q.Neg()) }

// Scale multiplies every coefficient by s.
func (p Poly) Scale(s *big.Rat) Poly {
	c := make([]*big.Rat, len(p.c))
	for i, x := range p.c {
		c[i] = new(big.Rat).Mul(x, s)
	}
	return Poly{c: c}.norm()
}

// Mul returns p * q.
func (p Poly) Mul(q Poly) Poly {
	if p.IsZero() || q.IsZero() {
		return Poly{}
	}
	c := make([]*big.Rat, p.Deg()+q.Deg()+1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	for i, x := range p.c {
		if x.Sign() == 0 {
			continue
		}
		for j, y := range q.c {
			if y.Sign() == 0 {
				continue
			}
			prod := new(big.Rat).Mul(x, y)
			c[i+j].Add(c[i+j], prod)
		}
	}
	return Poly{c: c}.norm()
}

// DivMod returns quotient and remainder of p divided by d.
// It panics if d is the zero polynomial.
func DivMod(p, d Poly) (Poly, Poly) {
	if d.IsZero() {
		panic("polynomial division by zero")
	}
	if p.Deg() < d.Deg() {
		return Poly{}, p.copy()
	}
	q := make([]*big.Rat, p.Deg()-d.Deg()+1)
	for i := range q {
		q[i] = new(big.Rat)
	}
	r := p.copy()
	for !r.IsZero() && r.Deg() >= d.Deg() {
		k := r.Deg() - d.Deg()
		t := new(big.Rat).Quo(r.lead(), d.lead())
		q[k].Add(q[k], t)
		for j := 0; j <= d.Deg(); j++ {
			prod := new(big.Rat).Mul(t, d.c[j])
			r.c[j+k].Sub(r.c[j+k], prod)
		}
		r = r.norm()
	}
	return Poly{c: q}.norm(), r
}

// Deriv returns the formal derivative of p.
func (p Poly) Deriv() Poly {
	if len(p.c) <= 1 {
		return Poly{}
	}
	c := make([]*big.Rat, len(p.c)-1)
	for i := 1; i < len(p.c); i++ {
		c[i-1] = new(big.Rat).Mul(p.c[i], new(big.Rat).SetInt64(int64(i)))
	}
	return Poly{c: c}.norm()
}

// Monic returns p scaled so its leading coefficient is 1.
func (p Poly) Monic() Poly {
	if p.IsZero() {
		return p
	}
	return p.Scale(new(big.Rat).Inv(p.lead()))
}

// GCD returns the monic greatest common divisor of a and b.
func GCD(a, b Poly) Poly {
	for !b.IsZero() {
		_, r := DivMod(a, b)
		a, b = b, r
	}
	return a.Monic()
}

// SquareFree returns p / gcd(p, p'): a square-free polynomial with
// exactly the same distinct real roots as p.
func SquareFree(p Poly) Poly {
	d := p.Deriv()
	if d.IsZero() {
		return p.Monic()
	}
	g := GCD(p, d)
	q, r := DivMod(p, g)
	if !r.IsZero() {
		panic("square-free reduction: inexact division")
	}
	return q.Monic()
}

// Eval evaluates p at the rational point x using Horner's rule.
func (p Poly) Eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p.c) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p.c[i])
	}
	return acc
}

// SignAt returns the sign of p(x): -1, 0 or +1.
func (p Poly) SignAt(x *big.Rat) int { return p.Eval(x).Sign() }

// gcdInts returns the greatest common divisor of a and b as a
// non-negative integer.  Arguments are taken by absolute value first:
// Int.GCD in older Go releases mishandles zero and negative inputs.
func gcdInts(a, b *big.Int) *big.Int {
	x := new(big.Int).Abs(a)
	y := new(big.Int).Abs(b)
	if x.Sign() == 0 {
		return y
	}
	if y.Sign() == 0 {
		return x
	}
	return x.GCD(nil, nil, x, y)
}

// IntCoeffs returns primitive integer coefficients for p (ascending
// degree, content 1, positive leading coefficient) as decimal strings.
func (p Poly) IntCoeffs() []string {
	if p.IsZero() {
		return []string{"0"}
	}
	lcm := big.NewInt(1)
	for _, c := range p.c {
		d := c.Denom()
		lcm.Div(lcm, gcdInts(lcm, d)).Mul(lcm, d)
	}
	ints := make([]*big.Int, len(p.c))
	content := new(big.Int)
	tmp := new(big.Int)
	for i, c := range p.c {
		ints[i] = new(big.Int).Mul(c.Num(), tmp.Div(lcm, c.Denom()))
		content = gcdInts(content, ints[i])
	}
	out := make([]string, len(ints))
	neg := ints[len(ints)-1].Sign() < 0
	for i, v := range ints {
		v.Div(v, content)
		if neg {
			v.Neg(v)
		}
		out[i] = v.String()
	}
	return out
}

// String renders the polynomial for debugging.
func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	parts := make([]string, 0, len(p.c))
	for i := len(p.c) - 1; i >= 0; i-- {
		if p.c[i].Sign() == 0 {
			continue
		}
		term := p.c[i].RatString()
		switch i {
		case 0:
		case 1:
			term += "*x"
		default:
			term += fmt.Sprintf("*x^%d", i)
		}
		parts = append(parts, term)
	}
	return strings.Join(parts, " + ")
}
