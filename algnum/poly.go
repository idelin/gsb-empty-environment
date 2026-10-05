// Package algnum implements exact comparison of real algebraic numbers.
//
// A real algebraic number is represented by a non-zero integer-coefficient
// polynomial together with an open isolating interval with rational
// endpoints: the endpoints are not roots and the interval contains exactly
// one distinct real root of the polynomial. All arithmetic is exact
// (math/big rational arithmetic); no floating point approximation is ever
// used to decide equality or order.
package algnum

import (
	"errors"
	"fmt"
	"math/big"
)

// Poly is a univariate polynomial with rational coefficients.
// Coefficients are stored in ascending degree order with no trailing
// zeros; the zero polynomial has an empty slice.
type Poly struct {
	c []*big.Rat
}

// NewPoly builds a normalized polynomial from ascending-degree coefficients.
// Nil entries are treated as zero.
func NewPoly(coeffs ...*big.Rat) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, v := range coeffs {
		if v == nil {
			v = new(big.Rat)
		}
		c[i] = new(big.Rat).Set(v)
	}
	return normalize(c)
}

// Const returns the constant polynomial v.
func Const(v *big.Rat) Poly { return NewPoly(v) }

func normalize(c []*big.Rat) Poly {
	n := len(c)
	for n > 0 && c[n-1].Sign() == 0 {
		n--
	}
	return Poly{c: c[:n]}
}

func ratInt(v int64) *big.Rat { return big.NewRat(v, 1) }

// IsZero reports whether p is the zero polynomial.
func (p Poly) IsZero() bool { return len(p.c) == 0 }

// Deg returns the degree of p; the zero polynomial has degree -1.
func (p Poly) Deg() int { return len(p.c) - 1 }

// Lead returns the leading coefficient of p (0 for the zero polynomial).
func (p Poly) Lead() *big.Rat {
	if p.IsZero() {
		return new(big.Rat)
	}
	return new(big.Rat).Set(p.c[len(p.c)-1])
}

// At returns the coefficient of x^i (0 when out of range).
func (p Poly) At(i int) *big.Rat {
	if i < 0 || i >= len(p.c) {
		return new(big.Rat)
	}
	return new(big.Rat).Set(p.c[i])
}

// Coeffs returns a copy of the ascending-degree coefficients.
func (p Poly) Coeffs() []*big.Rat {
	out := make([]*big.Rat, len(p.c))
	for i, v := range p.c {
		out[i] = new(big.Rat).Set(v)
	}
	return out
}

// Eval evaluates p at x by Horner's rule.
func (p Poly) Eval(x *big.Rat) *big.Rat {
	r := new(big.Rat)
	for i := len(p.c) - 1; i >= 0; i-- {
		r.Mul(r, x)
		r.Add(r, p.c[i])
	}
	return r
}

// SignAt returns the sign of p(x): -1, 0 or +1.
func (p Poly) SignAt(x *big.Rat) int { return p.Eval(x).Sign() }

// Deriv returns the formal derivative of p.
func (p Poly) Deriv() Poly {
	if len(p.c) <= 1 {
		return Poly{}
	}
	c := make([]*big.Rat, len(p.c)-1)
	for i := 1; i < len(p.c); i++ {
		c[i-1] = new(big.Rat).Mul(p.c[i], ratInt(int64(i)))
	}
	return normalize(c)
}

// Neg returns -p.
func (p Poly) Neg() Poly {
	c := make([]*big.Rat, len(p.c))
	for i, v := range p.c {
		c[i] = new(big.Rat).Neg(v)
	}
	return Poly{c: c}
}

// Add returns a+b.
func Add(a, b Poly) Poly {
	n := len(a.c)
	if len(b.c) > n {
		n = len(b.c)
	}
	c := make([]*big.Rat, n)
	for i := range c {
		c[i] = new(big.Rat).Add(a.At(i), b.At(i))
	}
	return normalize(c)
}

// Sub returns a-b.
func Sub(a, b Poly) Poly { return Add(a, b.Neg()) }

// Mul returns a*b.
func Mul(a, b Poly) Poly {
	if a.IsZero() || b.IsZero() {
		return Poly{}
	}
	c := make([]*big.Rat, len(a.c)+len(b.c)-1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	for i, av := range a.c {
		for j, bv := range b.c {
			t := new(big.Rat).Mul(av, bv)
			c[i+j].Add(c[i+j], t)
		}
	}
	return normalize(c)
}

// Pow returns p^n for n >= 0.
func Pow(p Poly, n int) Poly {
	r := Const(ratInt(1))
	for i := 0; i < n; i++ {
		r = Mul(r, p)
	}
	return r
}

// Scale multiplies p by the scalar s.
func (p Poly) Scale(s *big.Rat) Poly {
	c := make([]*big.Rat, len(p.c))
	for i, v := range p.c {
		c[i] = new(big.Rat).Mul(v, s)
	}
	return normalize(c)
}

// Monic scales p so its leading coefficient is 1.
func (p Poly) Monic() Poly {
	if p.IsZero() {
		return p
	}
	return p.Scale(new(big.Rat).Inv(p.Lead()))
}

// DivMod returns quotient and remainder of p divided by d over the
// rationals. It panics if d is the zero polynomial.
func (p Poly) DivMod(d Poly) (Poly, Poly) {
	if d.IsZero() {
		panic("algnum: division by zero polynomial")
	}
	q := Poly{}
	r := p
	for !r.IsZero() && r.Deg() >= d.Deg() {
		k := r.Deg() - d.Deg()
		mono := make([]*big.Rat, k+1)
		mono[k] = new(big.Rat).Quo(r.Lead(), d.Lead())
		m := NewPoly(mono...)
		q = Add(q, m)
		r = Sub(r, Mul(d, m))
	}
	return q, r
}

// QuoExact returns p/d and whether the division is exact.
func (p Poly) QuoExact(d Poly) (Poly, bool) {
	q, r := p.DivMod(d)
	return q, r.IsZero()
}

// GCD returns the monic greatest common divisor of a and b over Q[x].
func GCD(a, b Poly) Poly {
	if a.IsZero() {
		return b.Monic()
	}
	if b.IsZero() {
		return a.Monic()
	}
	for !b.IsZero() {
		_, r := a.DivMod(b)
		a, b = b, r
	}
	return a.Monic()
}

// SquareFree returns p divided by gcd(p, p'): the product of the distinct
// irreducible factors of p. It has the same distinct (real) roots as p,
// each with multiplicity one.
func (p Poly) SquareFree() Poly {
	if p.Deg() <= 0 {
		return p
	}
	g := GCD(p, p.Deriv())
	q, _ := p.QuoExact(g)
	return q
}

// SFFactor is one factor of a square-free decomposition: F^Exp divides p.
type SFFactor struct {
	F   Poly
	Exp int
}

// SquareFreeFactors computes Yun's square-free factorization: p equals a
// rational constant times the product of F_i^i, where each F_i is
// square-free and the F_i are pairwise coprime. Constant factors are
// omitted from the result.
func SquareFreeFactors(p Poly) []SFFactor {
	if p.Deg() <= 0 {
		return nil
	}
	c := GCD(p, p.Deriv())
	w, _ := p.QuoExact(c)
	var out []SFFactor
	for i := 1; !w.IsZero() && w.Deg() > 0; i++ {
		y := GCD(w, c)
		z, _ := w.QuoExact(y)
		if z.Deg() > 0 {
			out = append(out, SFFactor{F: z, Exp: i})
		}
		w = y
		c, _ = c.QuoExact(y)
	}
	return out
}

// String renders p in a human-readable ascending form, for diagnostics.
func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	s := ""
	for i, v := range p.c {
		if i > 0 {
			s += " + "
		}
		s += fmt.Sprintf("(%s)x^%d", v.RatString(), i)
	}
	return s
}

var errNotInteger = errors.New("coefficient is not an integer")

// ParseIntPoly parses an ascending-degree integer coefficient list.
// Each entry must be an integer (decimal or "p/q" with q dividing p).
func ParseIntPoly(coeffs []string) (Poly, error) {
	if len(coeffs) == 0 {
		return Poly{}, errors.New("empty coefficient list")
	}
	c := make([]*big.Rat, len(coeffs))
	for i, s := range coeffs {
		v, ok := new(big.Rat).SetString(s)
		if !ok {
			return Poly{}, fmt.Errorf("coefficient %d: invalid number %q", i, s)
		}
		if v.Denom().Cmp(big.NewInt(1)) != 0 {
			return Poly{}, fmt.Errorf("coefficient %d: %q: %w", i, s, errNotInteger)
		}
		c[i] = v
	}
	return NewPoly(c...), nil
}

// ParseRatPoly parses an ascending-degree rational coefficient list.
func ParseRatPoly(coeffs []string) (Poly, error) {
	if len(coeffs) == 0 {
		return Poly{}, errors.New("empty coefficient list")
	}
	c := make([]*big.Rat, len(coeffs))
	for i, s := range coeffs {
		v, ok := new(big.Rat).SetString(s)
		if !ok {
			return Poly{}, fmt.Errorf("coefficient %d: invalid rational %q", i, s)
		}
		c[i] = v
	}
	return NewPoly(c...), nil
}

// RatStrings renders coefficients as exact "p/q" (or "p") strings.
func RatStrings(coeffs []*big.Rat) []string {
	out := make([]string, len(coeffs))
	for i, v := range coeffs {
		out[i] = v.RatString()
	}
	return out
}
