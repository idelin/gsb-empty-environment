// Package rational simplifies univariate rational functions over the
// rationals while preserving the domain of the original function:
// cancelling a common factor does not resurrect the function at the
// cancelled roots. All arithmetic is exact (big.Rat); real roots are
// reported as exact isolating intervals with rational endpoints.
package rational

import (
	"math/big"
	"strings"
)

// Poly is a univariate polynomial with exact rational coefficients.
// coeffs[i] holds the coefficient of x^i; the zero polynomial has no
// coefficients. Coefficient slices are never shared between Polys.
type Poly struct {
	coeffs []big.Rat
}

// NewPoly builds a polynomial from rational coefficients, constant term
// first. Nil entries are treated as zero.
func NewPoly(coeffs ...*big.Rat) *Poly {
	p := &Poly{coeffs: make([]big.Rat, len(coeffs))}
	for i, c := range coeffs {
		if c != nil {
			p.coeffs[i].Set(c)
		}
	}
	p.normalize()
	return p
}

// PolyFromInts builds a polynomial from integer coefficients, constant
// term first: PolyFromInts(c0, c1, c2) = c0 + c1*x + c2*x^2.
func PolyFromInts(coeffs ...int64) *Poly {
	p := &Poly{coeffs: make([]big.Rat, len(coeffs))}
	for i, c := range coeffs {
		p.coeffs[i].SetInt64(c)
	}
	p.normalize()
	return p
}

// Zero returns the zero polynomial.
func Zero() *Poly { return &Poly{} }

func (p *Poly) normalize() {
	n := len(p.coeffs)
	for n > 0 && p.coeffs[n-1].Sign() == 0 {
		n--
	}
	p.coeffs = p.coeffs[:n]
}

// Copy returns a deep copy of p.
func (p *Poly) Copy() *Poly {
	q := &Poly{coeffs: make([]big.Rat, len(p.coeffs))}
	for i := range p.coeffs {
		q.coeffs[i].Set(&p.coeffs[i])
	}
	return q
}

// IsZero reports whether p is the zero polynomial.
func (p *Poly) IsZero() bool { return len(p.coeffs) == 0 }

// IsConstant reports whether p has degree <= 0.
func (p *Poly) IsConstant() bool { return len(p.coeffs) <= 1 }

// IsOne reports whether p is the constant polynomial 1.
func (p *Poly) IsOne() bool {
	return len(p.coeffs) == 1 && p.coeffs[0].Cmp(big.NewRat(1, 1)) == 0
}

// Degree returns the degree of p, or -1 for the zero polynomial.
func (p *Poly) Degree() int { return len(p.coeffs) - 1 }

// Coeff returns a copy of the coefficient of x^i (zero when out of range).
func (p *Poly) Coeff(i int) *big.Rat {
	if i < 0 || i >= len(p.coeffs) {
		return new(big.Rat)
	}
	return new(big.Rat).Set(&p.coeffs[i])
}

// LeadingCoeff returns a copy of the leading coefficient, or 0.
func (p *Poly) LeadingCoeff() *big.Rat { return p.Coeff(p.Degree()) }

// Equal reports whether a and b are the same polynomial.
func (p *Poly) Equal(q *Poly) bool {
	if len(p.coeffs) != len(q.coeffs) {
		return false
	}
	for i := range p.coeffs {
		if p.coeffs[i].Cmp(&q.coeffs[i]) != 0 {
			return false
		}
	}
	return true
}

// Add returns a + b.
func Add(a, b *Poly) *Poly {
	n := len(a.coeffs)
	if len(b.coeffs) > n {
		n = len(b.coeffs)
	}
	c := &Poly{coeffs: make([]big.Rat, n)}
	for i := 0; i < n; i++ {
		c.coeffs[i].Add(a.Coeff(i), b.Coeff(i))
	}
	c.normalize()
	return c
}

// Neg returns -p.
func (p *Poly) Neg() *Poly {
	q := &Poly{coeffs: make([]big.Rat, len(p.coeffs))}
	for i := range p.coeffs {
		q.coeffs[i].Neg(&p.coeffs[i])
	}
	return q
}

// Sub returns a - b.
func Sub(a, b *Poly) *Poly { return Add(a, b.Neg()) }

// Mul returns a * b.
func Mul(a, b *Poly) *Poly {
	if a.IsZero() || b.IsZero() {
		return Zero()
	}
	c := &Poly{coeffs: make([]big.Rat, len(a.coeffs)+len(b.coeffs)-1)}
	for i := range a.coeffs {
		for j := range b.coeffs {
			t := new(big.Rat).Mul(&a.coeffs[i], &b.coeffs[j])
			c.coeffs[i+j].Add(&c.coeffs[i+j], t)
		}
	}
	c.normalize()
	return c
}

// Scale returns p multiplied by the scalar s.
func (p *Poly) Scale(s *big.Rat) *Poly {
	if s.Sign() == 0 {
		return Zero()
	}
	q := &Poly{coeffs: make([]big.Rat, len(p.coeffs))}
	for i := range p.coeffs {
		q.coeffs[i].Mul(&p.coeffs[i], s)
	}
	q.normalize()
	return q
}

// Monic returns p scaled so its leading coefficient is 1.
// The zero polynomial is returned unchanged.
func (p *Poly) Monic() *Poly {
	if p.IsZero() {
		return Zero()
	}
	return p.Scale(new(big.Rat).Inv(p.LeadingCoeff()))
}

// DivMod returns q and r with a = q*b + r and deg(r) < deg(b).
// It panics if b is zero.
func DivMod(a, b *Poly) (q, r *Poly) {
	if b.IsZero() {
		panic("rational: polynomial division by zero")
	}
	r = a.Copy()
	if r.Degree() < b.Degree() {
		return Zero(), r
	}
	qc := make([]big.Rat, r.Degree()-b.Degree()+1)
	blc := b.LeadingCoeff()
	for !r.IsZero() && r.Degree() >= b.Degree() {
		d := r.Degree() - b.Degree()
		c := new(big.Rat).Quo(r.LeadingCoeff(), blc)
		qc[d] = *new(big.Rat).Set(c)
		for i := range b.coeffs {
			t := new(big.Rat).Mul(c, &b.coeffs[i])
			r.coeffs[i+d].Sub(&r.coeffs[i+d], t)
		}
		r.normalize()
	}
	q = &Poly{coeffs: qc}
	q.normalize()
	return q, r
}

// GCD returns the monic greatest common divisor of a and b.
// GCD(0, b) is the monic form of b; GCD(0, 0) is zero.
func GCD(a, b *Poly) *Poly {
	if a.IsZero() && b.IsZero() {
		return Zero()
	}
	if a.IsZero() {
		return b.Monic()
	}
	if b.IsZero() {
		return a.Monic()
	}
	for !b.IsZero() {
		_, r := DivMod(a, b)
		a, b = b, r
	}
	return a.Monic()
}

// Derivative returns the formal derivative of p.
func (p *Poly) Derivative() *Poly {
	if p.IsZero() {
		return Zero()
	}
	q := &Poly{coeffs: make([]big.Rat, len(p.coeffs)-1)}
	for i := 1; i < len(p.coeffs); i++ {
		q.coeffs[i-1].Mul(&p.coeffs[i], new(big.Rat).SetInt64(int64(i)))
	}
	q.normalize()
	return q
}

// Eval evaluates p at the exact rational point x.
func (p *Poly) Eval(x *big.Rat) *big.Rat {
	acc := new(big.Rat)
	for i := len(p.coeffs) - 1; i >= 0; i-- {
		acc.Mul(acc, x)
		acc.Add(acc, &p.coeffs[i])
	}
	return acc
}

// SignAt returns the sign of p(x): -1, 0 or +1.
func (p *Poly) SignAt(x *big.Rat) int { return p.Eval(x).Sign() }

// SFactor is one factor of a square-free factorization: Factor is a
// non-constant square-free polynomial appearing with multiplicity Mult.
type SFactor struct {
	Factor *Poly
	Mult   int
}

// SquareFreeFactorization returns Yun's square-free factorization of p:
// p equals (up to a scalar) the product of Factor^Mult. Factors are
// monic, pairwise coprime, square-free and non-constant. It returns nil
// for constant or zero polynomials.
func (p *Poly) SquareFreeFactorization() []SFactor {
	if p.IsZero() || p.IsConstant() {
		return nil
	}
	g := GCD(p, p.Derivative())
	w, _ := DivMod(p, g)
	var out []SFactor
	for m := 1; !w.IsOne(); m++ {
		y := GCD(w, g)
		z, _ := DivMod(w, y)
		if !z.IsOne() {
			out = append(out, SFactor{Factor: z.Monic(), Mult: m})
		}
		w = y
		g, _ = DivMod(g, y)
	}
	return out
}

// SquareFreePart returns the monic product of the distinct irreducible
// factors of p (p divided by gcd(p, p')). Constant inputs yield 1.
func (p *Poly) SquareFreePart() *Poly {
	if p.IsZero() {
		return Zero()
	}
	q, _ := DivMod(p, GCD(p, p.Derivative()))
	return q.Monic()
}

// MultiplicityIn returns how many times factor divides f exactly.
// factor must be non-constant and f non-zero.
func MultiplicityIn(f, factor *Poly) int {
	m := 0
	for !f.IsZero() {
		q, r := DivMod(f, factor)
		if !r.IsZero() {
			break
		}
		f = q
		m++
	}
	return m
}

// String renders p in a compact human-readable form, e.g. "x^2-3x+2".
func (p *Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	var terms []string
	for i := p.Degree(); i >= 0; i-- {
		c := &p.coeffs[i]
		if c.Sign() == 0 {
			continue
		}
		cs := c.RatString()
		switch i {
		case 0:
			terms = append(terms, cs)
		case 1:
			if c.Cmp(big.NewRat(1, 1)) == 0 {
				terms = append(terms, "x")
			} else {
				terms = append(terms, cs+"x")
			}
		default:
			if c.Cmp(big.NewRat(1, 1)) == 0 {
				terms = append(terms, "x^"+itoa(i))
			} else {
				terms = append(terms, cs+"x^"+itoa(i))
			}
		}
	}
	s := terms[0]
	for _, t := range terms[1:] {
		if strings.HasPrefix(t, "-") {
			s += t
		} else {
			s += "+" + t
		}
	}
	return s
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
