// Package rootfind implements a bounded root finder for univariate
// polynomials with rational coefficients. All arithmetic is exact
// (math/big.Rat); completeness of root isolation is guaranteed by Sturm
// sequences, never by sampling or sign-change heuristics.
package rootfind

import (
	"fmt"
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with rational coefficients.
// c[i] is the coefficient of x^i. The zero polynomial has no coefficients.
// The representation is normalized: no trailing zero coefficients.
type Poly struct {
	c []*big.Rat
}

// Rat builds a rational from num/den.
func Rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

// Zero returns the zero polynomial.
func Zero() Poly { return Poly{} }

// Const returns the constant polynomial r.
func Const(r *big.Rat) Poly {
	if r.Sign() == 0 {
		return Poly{}
	}
	return Poly{c: []*big.Rat{new(big.Rat).Set(r)}}
}

// X returns the monomial x.
func X() Poly { return Poly{c: []*big.Rat{new(big.Rat), big.NewRat(1, 1)}} }

// Linear returns x - r.
func Linear(r *big.Rat) Poly {
	return Poly{c: []*big.Rat{new(big.Rat).Neg(r), big.NewRat(1, 1)}}
}

func norm(c []*big.Rat) []*big.Rat {
	n := len(c)
	for n > 0 && c[n-1].Sign() == 0 {
		n--
	}
	return c[:n]
}

// Deg returns the degree, or -1 for the zero polynomial.
func (p Poly) Deg() int { return len(p.c) - 1 }

// IsZero reports whether p is the zero polynomial.
func (p Poly) IsZero() bool { return len(p.c) == 0 }

// IsConst reports whether p is constant (degree <= 0).
func (p Poly) IsConst() bool { return len(p.c) <= 1 }

// Coeff returns the coefficient of x^i (zero when out of range).
func (p Poly) Coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.c) {
		return new(big.Rat)
	}
	return p.c[i]
}

// Lead returns the leading coefficient. Panics on the zero polynomial.
func (p Poly) Lead() *big.Rat { return p.c[len(p.c)-1] }

// Add returns p + q.
func (p Poly) Add(q Poly) Poly {
	n := max(len(p.c), len(q.c))
	c := make([]*big.Rat, n)
	for i := range c {
		c[i] = new(big.Rat).Add(p.Coeff(i), q.Coeff(i))
	}
	return Poly{c: norm(c)}
}

// Neg returns -p.
func (p Poly) Neg() Poly {
	c := make([]*big.Rat, len(p.c))
	for i, a := range p.c {
		c[i] = new(big.Rat).Neg(a)
	}
	return Poly{c: c}
}

// Sub returns p - q.
func (p Poly) Sub(q Poly) Poly { return p.Add(q.Neg()) }

// Scale returns s * p for a rational scalar s.
func (p Poly) Scale(s *big.Rat) Poly {
	if s.Sign() == 0 {
		return Poly{}
	}
	c := make([]*big.Rat, len(p.c))
	for i, a := range p.c {
		c[i] = new(big.Rat).Mul(a, s)
	}
	return Poly{c: c}
}

// Mul returns p * q.
func (p Poly) Mul(q Poly) Poly {
	if p.IsZero() || q.IsZero() {
		return Poly{}
	}
	c := make([]*big.Rat, len(p.c)+len(q.c)-1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	for i, a := range p.c {
		if a.Sign() == 0 {
			continue
		}
		for j, b := range q.c {
			if b.Sign() != 0 {
				c[i+j].Add(c[i+j], new(big.Rat).Mul(a, b))
			}
		}
	}
	return Poly{c: norm(c)}
}

// Pow returns p^n (n >= 0).
func (p Poly) Pow(n int) Poly {
	if n < 0 {
		panic("rootfind: negative exponent")
	}
	r := Const(big.NewRat(1, 1))
	base := p
	for n > 0 {
		if n&1 == 1 {
			r = r.Mul(base)
		}
		base = base.Mul(base)
		n >>= 1
	}
	return r
}

// Deriv returns the formal derivative of p.
func (p Poly) Deriv() Poly {
	if p.IsConst() {
		return Poly{}
	}
	c := make([]*big.Rat, len(p.c)-1)
	for i := 1; i < len(p.c); i++ {
		c[i-1] = new(big.Rat).Mul(p.c[i], big.NewRat(int64(i), 1))
	}
	return Poly{c: norm(c)}
}

// Eval evaluates p at x by Horner's rule, exactly.
func (p Poly) Eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p.c) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, p.c[i])
	}
	return acc
}

// QuoRem returns quotient and remainder of p divided by d over Q.
// Division is exact in the sense of rational arithmetic: p = q*d + r
// with deg r < deg d, no rounding anywhere.
func (p Poly) QuoRem(d Poly) (quo, rem Poly) {
	if d.IsZero() {
		panic("rootfind: division by zero polynomial")
	}
	rem = p
	if rem.Deg() < d.Deg() {
		return Poly{}, rem
	}
	q := make([]*big.Rat, rem.Deg()-d.Deg()+1)
	for i := range q {
		q[i] = new(big.Rat)
	}
	dl := d.Lead()
	for rem.Deg() >= d.Deg() && !rem.IsZero() {
		k := rem.Deg() - d.Deg()
		t := new(big.Rat).Quo(rem.Lead(), dl)
		q[k] = t
		// rem -= t * x^k * d
		c := make([]*big.Rat, len(rem.c))
		for i := range c {
			c[i] = new(big.Rat).Set(rem.c[i])
		}
		for j, b := range d.c {
			c[k+j].Sub(c[k+j], new(big.Rat).Mul(t, b))
		}
		rem = Poly{c: norm(c)}
	}
	return Poly{c: norm(q)}, rem
}

// Quo returns p / d (quotient only).
func (p Poly) Quo(d Poly) Poly { q, _ := p.QuoRem(d); return q }

// Rem returns p mod d (remainder only).
func (p Poly) Rem(d Poly) Poly { _, r := p.QuoRem(d); return r }

// Monic returns p scaled to have leading coefficient 1.
func (p Poly) Monic() Poly {
	if p.IsZero() {
		return p
	}
	return p.Scale(new(big.Rat).Inv(p.Lead()))
}

// GCD returns the monic greatest common divisor of a and b.
func GCD(a, b Poly) Poly {
	if a.IsZero() {
		return b.Monic()
	}
	if b.IsZero() {
		return a.Monic()
	}
	for !b.IsZero() {
		a, b = b, a.Rem(b)
	}
	return a.Monic()
}

// String renders p in a compact human-readable form.
func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var terms []string
	for i := len(p.c) - 1; i >= 0; i-- {
		a := p.c[i]
		if a.Sign() == 0 {
			continue
		}
		s := a.RatString()
		switch i {
		case 0:
			terms = append(terms, s)
		case 1:
			if a.Cmp(big.NewRat(1, 1)) == 0 {
				terms = append(terms, "x")
			} else {
				terms = append(terms, s+"*x")
			}
		default:
			if a.Cmp(big.NewRat(1, 1)) == 0 {
				terms = append(terms, fmt.Sprintf("x^%d", i))
			} else {
				terms = append(terms, fmt.Sprintf("%s*x^%d", s, i))
			}
		}
	}
	return strings.Join(terms, " + ")
}
