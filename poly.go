package piecewise

import "math/big"

// Poly is a polynomial with rational coefficients.
// It is immutable; all operations return new values.
// The zero value is the zero polynomial.
type Poly struct {
	// coeffs[i] is the coefficient of x^i, with no trailing zeros.
	coeffs []*big.Rat
}

// NewPoly builds a polynomial from coefficients in ascending order
// (coeffs[i] multiplies x^i). Nil entries are treated as zero.
func NewPoly(coeffs ...*big.Rat) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, v := range coeffs {
		if v == nil {
			c[i] = new(big.Rat)
		} else {
			c[i] = new(big.Rat).Set(v)
		}
	}
	return Poly{normalize(c)}
}

// PolyInts is a convenience constructor with integer coefficients
// in ascending order.
func PolyInts(coeffs ...int64) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, v := range coeffs {
		c[i] = big.NewRat(v, 1)
	}
	return Poly{normalize(c)}
}

func normalize(c []*big.Rat) []*big.Rat {
	n := len(c)
	for n > 0 && c[n-1].Sign() == 0 {
		n--
	}
	return c[:n]
}

// IsZero reports whether p is the zero polynomial.
func (p Poly) IsZero() bool { return len(p.coeffs) == 0 }

// Degree returns the degree of p, or -1 for the zero polynomial.
func (p Poly) Degree() int { return len(p.coeffs) - 1 }

// Coeff returns the coefficient of x^i (zero when out of range).
func (p Poly) Coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.coeffs) {
		return new(big.Rat)
	}
	return new(big.Rat).Set(p.coeffs[i])
}

// Eval evaluates p at x using Horner's method.
func (p Poly) Eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p.coeffs[i])
	}
	return acc
}

// Add returns p + q.
func (p Poly) Add(q Poly) Poly {
	n := len(p.coeffs)
	if len(q.coeffs) > n {
		n = len(q.coeffs)
	}
	c := make([]*big.Rat, n)
	for i := range c {
		c[i] = new(big.Rat)
		if i < len(p.coeffs) {
			c[i].Add(c[i], p.coeffs[i])
		}
		if i < len(q.coeffs) {
			c[i].Add(c[i], q.coeffs[i])
		}
	}
	return Poly{normalize(c)}
}

// Sub returns p - q.
func (p Poly) Sub(q Poly) Poly { return p.Add(q.Scale(big.NewRat(-1, 1))) }

// Scale returns s * p.
func (p Poly) Scale(s *big.Rat) Poly {
	c := make([]*big.Rat, len(p.coeffs))
	for i, v := range p.coeffs {
		c[i] = new(big.Rat).Mul(v, s)
	}
	return Poly{normalize(c)}
}

// Deriv returns the formal derivative of p.
func (p Poly) Deriv() Poly {
	if len(p.coeffs) <= 1 {
		return Poly{}
	}
	c := make([]*big.Rat, len(p.coeffs)-1)
	for i := 1; i < len(p.coeffs); i++ {
		c[i-1] = new(big.Rat).Mul(p.coeffs[i], big.NewRat(int64(i), 1))
	}
	return Poly{normalize(c)}
}

// Antideriv returns the antiderivative of p with zero constant term.
func (p Poly) Antideriv() Poly {
	c := make([]*big.Rat, len(p.coeffs)+1)
	c[0] = new(big.Rat)
	for i, v := range p.coeffs {
		c[i+1] = new(big.Rat).Quo(v, big.NewRat(int64(i+1), 1))
	}
	return Poly{normalize(c)}
}

// Integrate returns the exact integral of p from a to b.
// Reversed bounds negate the result; equal bounds give zero.
func (p Poly) Integrate(a, b *big.Rat) *big.Rat {
	F := p.Antideriv()
	return new(big.Rat).Sub(F.Eval(b), F.Eval(a))
}
