// Package ratpoly implements univariate polynomials over the rationals
// with a coefficient-blowup-controlled (subresultant) extended GCD.
//
// Conventions:
//   - The zero polynomial is the empty (nil) coefficient slice, Degree() == -1.
//   - All coefficients are reduced rationals (math/big.Rat keeps lowest terms).
//   - GCD results are monic; gcd(0,0) = 0, gcd(0,f) = gcd(f,0) = Monic(f).
//   - Constants: gcd of two nonzero constants is 1 (the monic constant).
//   - Repeated roots are ordinary factors; the GCD keeps the minimum
//     multiplicity of every shared irreducible factor.
package ratpoly

import (
	"errors"
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with rational coefficients.
// c[i] is the coefficient of x^i. The representation is normalized:
// no trailing zero coefficients, and the zero polynomial has len(c) == 0.
type Poly struct{ c []*big.Rat }

// Zero is the zero polynomial.
var Zero = Poly{}

// ErrZeroDivisor is returned by DivMod when dividing by the zero polynomial.
var ErrZeroDivisor = errors.New("ratpoly: division by zero polynomial")

// NewPoly builds a normalized polynomial; coeffs[i] is the coefficient of x^i.
func NewPoly(coeffs ...*big.Rat) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, x := range coeffs {
		if x == nil {
			c[i] = new(big.Rat)
		} else {
			c[i] = new(big.Rat).Set(x)
		}
	}
	return normalize(c)
}

// PolyFromInts builds a polynomial from integer coefficients,
// coeffs[i] being the coefficient of x^i.
func PolyFromInts(coeffs ...int64) Poly {
	c := make([]*big.Rat, len(coeffs))
	for i, v := range coeffs {
		c[i] = big.NewRat(v, 1)
	}
	return normalize(c)
}

func normalize(c []*big.Rat) Poly {
	n := len(c)
	for n > 0 && c[n-1].Sign() == 0 {
		n--
	}
	return Poly{c: c[:n]}
}

// Degree returns the degree of p; the zero polynomial has degree -1.
func (p Poly) Degree() int { return len(p.c) - 1 }

// IsZero reports whether p is the zero polynomial.
func (p Poly) IsZero() bool { return len(p.c) == 0 }

// LC returns the leading coefficient (0 for the zero polynomial).
func (p Poly) LC() *big.Rat {
	if p.IsZero() {
		return new(big.Rat)
	}
	return new(big.Rat).Set(p.c[len(p.c)-1])
}

// Coeff returns the coefficient of x^i (0 when out of range).
func (p Poly) Coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.c) {
		return new(big.Rat)
	}
	return new(big.Rat).Set(p.c[i])
}

// Monic returns p scaled so its leading coefficient is 1.
// The zero polynomial maps to itself.
func (p Poly) Monic() Poly {
	if p.IsZero() {
		return Zero
	}
	return Scale(p, new(big.Rat).Inv(p.LC()))
}

// Equal reports whether p and q are the same polynomial.
func (p Poly) Equal(q Poly) bool {
	if len(p.c) != len(q.c) {
		return false
	}
	for i := range p.c {
		if p.c[i].Cmp(q.c[i]) != 0 {
			return false
		}
	}
	return true
}

// BitLen returns the maximum, over all coefficients, of
// num.BitLen()+den.BitLen(); it is a size measure for coefficient blowup.
func BitLen(p Poly) int {
	max := 0
	for _, c := range p.c {
		n := c.Num().BitLen() + c.Denom().BitLen()
		if n > max {
			max = n
		}
	}
	return max
}

// Add returns a+b.
func Add(a, b Poly) Poly {
	n := len(a.c)
	if len(b.c) > n {
		n = len(b.c)
	}
	c := make([]*big.Rat, n)
	for i := range c {
		c[i] = new(big.Rat).Add(a.Coeff(i), b.Coeff(i))
	}
	return normalize(c)
}

// Neg returns -a.
func Neg(a Poly) Poly {
	c := make([]*big.Rat, len(a.c))
	for i, x := range a.c {
		c[i] = new(big.Rat).Neg(x)
	}
	return Poly{c: c}
}

// Sub returns a-b.
func Sub(a, b Poly) Poly { return Add(a, Neg(b)) }

// Scale returns s*a.
func Scale(a Poly, s *big.Rat) Poly {
	if s.Sign() == 0 {
		return Zero
	}
	c := make([]*big.Rat, len(a.c))
	for i, x := range a.c {
		c[i] = new(big.Rat).Mul(x, s)
	}
	return normalize(c)
}

// Mul returns a*b (schoolbook).
func Mul(a, b Poly) Poly {
	if a.IsZero() || b.IsZero() {
		return Zero
	}
	c := make([]*big.Rat, len(a.c)+len(b.c)-1)
	for i := range c {
		c[i] = new(big.Rat)
	}
	t := new(big.Rat)
	for i, x := range a.c {
		for j, y := range b.c {
			t.Mul(x, y)
			c[i+j].Add(c[i+j], t)
		}
	}
	return normalize(c)
}

// DivMod returns q, r with a = q*b + r and deg(r) < deg(b), over Q.
func DivMod(a, b Poly) (q, r Poly, err error) {
	if b.IsZero() {
		return Zero, Zero, ErrZeroDivisor
	}
	r = a
	q = Zero
	if r.Degree() < b.Degree() {
		return q, r, nil
	}
	// Work on a copy: never mutate the caller's coefficients.
	rc := make([]*big.Rat, len(r.c))
	for i, x := range r.c {
		rc[i] = new(big.Rat).Set(x)
	}
	r = Poly{c: rc}
	qc := make([]*big.Rat, r.Degree()-b.Degree()+1)
	for i := range qc {
		qc[i] = new(big.Rat)
	}
	blc := b.LC()
	for !r.IsZero() && r.Degree() >= b.Degree() {
		s := new(big.Rat).Quo(r.LC(), blc)
		d := r.Degree() - b.Degree()
		qc[d] = s
		// r -= s * x^d * b
		rc := r.c
		for j, y := range b.c {
			t := new(big.Rat).Mul(s, y)
			rc[d+j] = new(big.Rat).Sub(rc[d+j], t)
		}
		r = normalize(rc)
	}
	return normalize(qc), r, nil
}

func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var sb strings.Builder
	for i := len(p.c) - 1; i >= 0; i-- {
		c := p.c[i]
		if c.Sign() == 0 {
			continue
		}
		sign := ""
		abs := c
		if c.Sign() < 0 {
			sign = "-"
			abs = new(big.Rat).Neg(c)
		}
		if sb.Len() > 0 {
			if c.Sign() < 0 {
				sb.WriteString(" - ")
			} else {
				sb.WriteString(" + ")
			}
		} else {
			sb.WriteString(sign)
		}
		one := abs.Cmp(big.NewRat(1, 1)) == 0
		if !one || i == 0 {
			sb.WriteString(abs.RatString())
		}
		switch {
		case i > 1:
			sb.WriteString("x^")
			sb.WriteString(itoa(i))
		case i == 1:
			sb.WriteString("x")
		}
	}
	return sb.String()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
