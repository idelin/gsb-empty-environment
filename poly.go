// Package piecewise provides exact arithmetic on piecewise-polynomial
// functions with rational breakpoints and rational coefficients.
//
// All computations are exact (math/big.Rat). Piecewise values are
// immutable: every modification returns a new version and old versions
// remain usable. Distribution-sense features (Dirac impulses at jump
// discontinuities) are explicitly out of scope; integration is plain
// Riemann integration of each polynomial piece, so single endpoints
// never contribute to an integral.
package piecewise

import (
	"math/big"
	"strings"
)

// Poly is a polynomial with rational coefficients.
// coeffs[i] is the coefficient of x^i. The zero value is the zero
// polynomial. Poly is immutable after construction.
type Poly struct {
	coeffs []*big.Rat
}

// NewPoly builds a polynomial from coefficients of ascending degree:
// NewPoly(c0, c1, c2) = c0 + c1*x + c2*x^2. Nil entries are treated as 0.
// The coefficients are copied.
func NewPoly(coeffs ...*big.Rat) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, k := range coeffs {
		if k == nil {
			c[i] = new(big.Rat)
		} else {
			c[i] = new(big.Rat).Set(k)
		}
	}
	return Poly{coeffs: trim(c)}
}

// PolyOf is a convenience constructor for integer coefficients.
func PolyOf(coeffs ...int64) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, k := range coeffs {
		c[i] = big.NewRat(k, 1)
	}
	return Poly{coeffs: trim(c)}
}

// trim drops leading zero coefficients.
func trim(c []*big.Rat) []*big.Rat {
	n := len(c)
	for n > 0 && c[n-1].Sign() == 0 {
		n--
	}
	return c[:n]
}

// Degree returns the degree of the polynomial, or -1 for the zero
// polynomial.
func (p Poly) Degree() int { return len(p.coeffs) - 1 }

// Coeff returns the coefficient of x^i (0 when out of range).
// The returned value must not be mutated.
func (p Poly) Coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.coeffs) {
		return new(big.Rat)
	}
	return p.coeffs[i]
}

// Equal reports whether p and q are the same polynomial.
func (p Poly) Equal(q Poly) bool {
	if len(p.coeffs) != len(q.coeffs) {
		return false
	}
	for i := range p.coeffs {
		if p.coeffs[i].Cmp(q.coeffs[i]) != 0 {
			return false
		}
	}
	return true
}

// Eval evaluates p at x using Horner's scheme.
func (p Poly) Eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p.coeffs[i])
	}
	return acc
}

// Deriv returns the formal derivative of p.
func (p Poly) Deriv() Poly {
	if len(p.coeffs) <= 1 {
		return Poly{}
	}
	d := make([]*big.Rat, len(p.coeffs)-1)
	for i := 1; i < len(p.coeffs); i++ {
		d[i-1] = new(big.Rat).Mul(p.coeffs[i], big.NewRat(int64(i), 1))
	}
	return Poly{coeffs: d}
}

// Antideriv returns the antiderivative of p with zero constant term.
func (p Poly) Antideriv() Poly {
	a := make([]*big.Rat, len(p.coeffs)+1)
	a[0] = new(big.Rat)
	for i, c := range p.coeffs {
		a[i+1] = new(big.Rat).Quo(c, big.NewRat(int64(i+1), 1))
	}
	return Poly{coeffs: a}
}

// Integral returns the exact definite integral of p from a to b.
// Reversed bounds negate the result; equal bounds give 0.
func (p Poly) Integral(a, b *big.Rat) *big.Rat {
	F := p.Antideriv()
	return new(big.Rat).Sub(F.Eval(b), F.Eval(a))
}

func (p Poly) String() string {
	if len(p.coeffs) == 0 {
		return "0"
	}
	var sb strings.Builder
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		c := p.coeffs[i]
		if c.Sign() == 0 {
			continue
		}
		if sb.Len() > 0 {
			if c.Sign() > 0 {
				sb.WriteString(" + ")
			} else {
				sb.WriteString(" - ")
				c = new(big.Rat).Neg(c)
			}
		}
		switch i {
		case 0:
			sb.WriteString(c.RatString())
		case 1:
			sb.WriteString(c.RatString() + "*x")
		default:
			sb.WriteString(c.RatString())
			sb.WriteString("*x^")
			sb.WriteString(big.NewInt(int64(i)).String())
		}
	}
	return sb.String()
}
