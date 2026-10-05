package ratsimp

import (
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with integer coefficients.
// coeffs[i] is the coefficient of x^i; the coefficient list is kept
// normalized (no trailing zeros), so the zero polynomial has an empty
// list. Poly values are treated as immutable.
type Poly struct {
	coeffs []*big.Int
}

func norm(c []*big.Int) []*big.Int {
	n := len(c)
	for n > 0 && c[n-1].Sign() == 0 {
		n--
	}
	return c[:n]
}

// PolyFromInts builds a polynomial from int64 coefficients, lowest
// degree first: PolyFromInts(a, b, c) = a + b*x + c*x^2.
func PolyFromInts(coeffs ...int64) *Poly {
	c := make([]*big.Int, len(coeffs))
	for i, v := range coeffs {
		c[i] = big.NewInt(v)
	}
	return &Poly{coeffs: norm(c)}
}

// NewPoly builds a polynomial from big.Int coefficients (copied),
// lowest degree first.
func NewPoly(coeffs ...*big.Int) *Poly {
	c := make([]*big.Int, len(coeffs))
	for i, v := range coeffs {
		c[i] = new(big.Int).Set(v)
	}
	return &Poly{coeffs: norm(c)}
}

// Degree returns the degree of p, or -1 for the zero polynomial.
func (p *Poly) Degree() int { return len(p.coeffs) - 1 }

// IsZero reports whether p is the zero polynomial.
func (p *Poly) IsZero() bool { return len(p.coeffs) == 0 }

// Leading returns the leading coefficient (0 for the zero polynomial).
func (p *Poly) Leading() *big.Int {
	if p.IsZero() {
		return big.NewInt(0)
	}
	return new(big.Int).Set(p.coeffs[len(p.coeffs)-1])
}

// Coeff returns the coefficient of x^i (0 when out of range).
func (p *Poly) Coeff(i int) *big.Int {
	if i < 0 || i >= len(p.coeffs) {
		return big.NewInt(0)
	}
	return new(big.Int).Set(p.coeffs[i])
}

// Equal reports whether p and q are the same polynomial.
func (p *Poly) Equal(q *Poly) bool {
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

// Neg returns -p.
func (p *Poly) Neg() *Poly {
	c := make([]*big.Int, len(p.coeffs))
	for i, v := range p.coeffs {
		c[i] = new(big.Int).Neg(v)
	}
	return &Poly{coeffs: c}
}

// Add returns p + q.
func (p *Poly) Add(q *Poly) *Poly {
	n := len(p.coeffs)
	if len(q.coeffs) > n {
		n = len(q.coeffs)
	}
	c := make([]*big.Int, n)
	for i := 0; i < n; i++ {
		c[i] = new(big.Int)
		if i < len(p.coeffs) {
			c[i].Add(c[i], p.coeffs[i])
		}
		if i < len(q.coeffs) {
			c[i].Add(c[i], q.coeffs[i])
		}
	}
	return &Poly{coeffs: norm(c)}
}

// Sub returns p - q.
func (p *Poly) Sub(q *Poly) *Poly { return p.Add(q.Neg()) }

// Mul returns p * q.
func (p *Poly) Mul(q *Poly) *Poly {
	if p.IsZero() || q.IsZero() {
		return &Poly{}
	}
	c := make([]*big.Int, len(p.coeffs)+len(q.coeffs)-1)
	for i := range c {
		c[i] = new(big.Int)
	}
	for i, a := range p.coeffs {
		for j, b := range q.coeffs {
			c[i+j].Add(c[i+j], new(big.Int).Mul(a, b))
		}
	}
	return &Poly{coeffs: norm(c)}
}

// Pow returns p^n for n >= 0.
func (p *Poly) Pow(n int) *Poly {
	if n < 0 {
		panic("ratsimp: negative exponent")
	}
	r := PolyFromInts(1)
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

// Derivative returns the formal derivative of p.
func (p *Poly) Derivative() *Poly {
	if len(p.coeffs) <= 1 {
		return &Poly{}
	}
	c := make([]*big.Int, len(p.coeffs)-1)
	for i := 1; i < len(p.coeffs); i++ {
		c[i-1] = new(big.Int).Mul(p.coeffs[i], big.NewInt(int64(i)))
	}
	return &Poly{coeffs: norm(c)}
}

// Content returns the gcd of the coefficients (0 for the zero
// polynomial).
func (p *Poly) Content() *big.Int {
	g := new(big.Int)
	for _, v := range p.coeffs {
		g.GCD(nil, nil, g, v)
	}
	return g
}

// Primitive returns p divided by its content, with a positive leading
// coefficient.
func (p *Poly) Primitive() *Poly {
	if p.IsZero() {
		return &Poly{}
	}
	q := p.divByInt(p.Content())
	if q.coeffs[len(q.coeffs)-1].Sign() < 0 {
		q = q.Neg()
	}
	return q
}

// divByInt divides every coefficient by d; d must divide them all.
func (p *Poly) divByInt(d *big.Int) *Poly {
	c := make([]*big.Int, len(p.coeffs))
	for i, v := range p.coeffs {
		c[i] = new(big.Int).Quo(v, d)
	}
	return &Poly{coeffs: norm(c)}
}

// GCD returns the greatest common divisor of p and q as a primitive
// polynomial with positive leading coefficient. GCD(0, q) is the
// primitive part of q.
func (p *Poly) GCD(q *Poly) *Poly {
	if p.IsZero() {
		return q.Primitive()
	}
	if q.IsZero() {
		return p.Primitive()
	}
	return rgcd(rfromInts(p), rfromInts(q)).toIntPoly()
}

// ExactDiv returns p / q; it panics if q does not divide p.
func (p *Poly) ExactDiv(q *Poly) *Poly {
	if q.IsZero() {
		panic("ratsimp: ExactDiv by zero polynomial")
	}
	quo, rem := rfromInts(p).quoRem(rfromInts(q))
	if !rem.isZero() {
		panic("ratsimp: ExactDiv: not divisible")
	}
	return quo.toIntPoly()
}

func (p *Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var b strings.Builder
	first := true
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		c := p.coeffs[i]
		if c.Sign() == 0 {
			continue
		}
		neg := c.Sign() < 0
		switch {
		case first && neg:
			b.WriteString("-")
		case neg:
			b.WriteString(" - ")
		case !first:
			b.WriteString(" + ")
		}
		abs := new(big.Int).Abs(c)
		if i == 0 || abs.Cmp(big.NewInt(1)) != 0 {
			b.WriteString(abs.String())
		}
		if i == 1 {
			b.WriteString("x")
		} else if i > 1 {
			b.WriteString("x^")
			b.WriteString(big.NewInt(int64(i)).String())
		}
		first = false
	}
	return b.String()
}
