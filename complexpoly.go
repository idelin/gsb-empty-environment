// Package complexpoly implements sparse univariate polynomials over the
// complex numbers whose real and imaginary parts are rational numbers.
//
// Polynomials are stored sparsely as a map from degree to coefficient, so
// high-degree terms never force a dense allocation. No operation truncates
// implicitly; truncation is only available through MulTrunc with an
// explicitly supplied maximum degree.
package complexpoly

import (
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Coeff is a complex coefficient with rational real and imaginary parts.
// Values are always kept in lowest terms by big.Rat.
type Coeff struct {
	Re *big.Rat
	Im *big.Rat
}

// C builds a Coeff from rational strings such as "2/3", "-4" or "0.5".
// It panics on malformed input.
func C(re, im string) Coeff {
	r, ok := new(big.Rat).SetString(re)
	if !ok {
		panic("complexpoly: bad rational " + strconv.Quote(re))
	}
	i, ok := new(big.Rat).SetString(im)
	if !ok {
		panic("complexpoly: bad rational " + strconv.Quote(im))
	}
	return Coeff{Re: r, Im: i}
}

func (c Coeff) isZero() bool {
	return c.Re.Sign() == 0 && c.Im.Sign() == 0
}

func (c Coeff) clone() Coeff {
	return Coeff{Re: new(big.Rat).Set(c.Re), Im: new(big.Rat).Set(c.Im)}
}

func (c Coeff) eq(o Coeff) bool {
	return c.Re.Cmp(o.Re) == 0 && c.Im.Cmp(o.Im) == 0
}

func addCoeff(a, b Coeff) Coeff {
	return Coeff{
		Re: new(big.Rat).Add(a.Re, b.Re),
		Im: new(big.Rat).Add(a.Im, b.Im),
	}
}

// mulCoeff computes (a.Re+a.Im*i)*(b.Re+b.Im*i).
func mulCoeff(a, b Coeff) Coeff {
	ac := new(big.Rat).Mul(a.Re, b.Re)
	bd := new(big.Rat).Mul(a.Im, b.Im)
	ad := new(big.Rat).Mul(a.Re, b.Im)
	bc := new(big.Rat).Mul(a.Im, b.Re)
	return Coeff{
		Re: new(big.Rat).Sub(ac, bd),
		Im: new(big.Rat).Add(ad, bc),
	}
}

func conjCoeff(c Coeff) Coeff {
	return Coeff{Re: new(big.Rat).Set(c.Re), Im: new(big.Rat).Neg(c.Im)}
}

// Poly is a sparse polynomial. The zero polynomial has no terms.
// Poly values must not be shared across goroutines while being mutated.
type Poly struct {
	terms map[int]Coeff
}

// New returns the zero polynomial.
func New() *Poly {
	return &Poly{terms: make(map[int]Coeff)}
}

// SetTerm sets the coefficient of x^deg, defensively copying c. A zero
// coefficient removes the term. It returns p for chaining.
func (p *Poly) SetTerm(deg int, c Coeff) *Poly {
	if deg < 0 {
		panic("complexpoly: negative degree")
	}
	if c.isZero() {
		delete(p.terms, deg)
		return p
	}
	p.terms[deg] = c.clone()
	return p
}

// Term returns the coefficient of x^deg (zero if absent). The result is a
// copy; mutating it does not affect p.
func (p *Poly) Term(deg int) Coeff {
	if c, ok := p.terms[deg]; ok {
		return c.clone()
	}
	return C("0", "0")
}

// Clone returns a deep copy of p.
func (p *Poly) Clone() *Poly {
	q := New()
	for d, c := range p.terms {
		q.terms[d] = c.clone()
	}
	return q
}

// IsZero reports whether p is the zero polynomial.
func (p *Poly) IsZero() bool {
	return len(p.terms) == 0
}

// Degree returns the highest degree with a nonzero coefficient, or -1 for
// the zero polynomial.
func (p *Poly) Degree() int {
	max := -1
	for d := range p.terms {
		if d > max {
			max = d
		}
	}
	return max
}

// Degrees returns the degrees of all nonzero terms in ascending order.
func (p *Poly) Degrees() []int {
	ds := make([]int, 0, len(p.terms))
	for d := range p.terms {
		ds = append(ds, d)
	}
	sort.Ints(ds)
	return ds
}

func (p *Poly) addInto(deg int, c Coeff) {
	if c.isZero() {
		return
	}
	if cur, ok := p.terms[deg]; ok {
		sum := addCoeff(cur, c)
		if sum.isZero() {
			delete(p.terms, deg)
			return
		}
		p.terms[deg] = sum
		return
	}
	p.terms[deg] = c.clone()
}

// Add returns a+b as a new polynomial; neither operand is modified.
func Add(a, b *Poly) *Poly {
	res := a.Clone()
	for d, c := range b.terms {
		res.addInto(d, c)
	}
	return res
}

// Mul returns a*b as a new polynomial; neither operand is modified. Only
// pairs of existing terms are visited, so sparse high-degree inputs stay
// cheap.
func Mul(a, b *Poly) *Poly {
	return mul(a, b, -1)
}

// MulTrunc returns a*b with all terms of degree greater than maxDeg
// discarded. maxDeg must be non-negative; truncation happens only here and
// only because the caller asked for it explicitly.
func MulTrunc(a, b *Poly, maxDeg int) *Poly {
	if maxDeg < 0 {
		panic("complexpoly: negative truncation degree")
	}
	return mul(a, b, maxDeg)
}

func mul(a, b *Poly, maxDeg int) *Poly {
	res := New()
	for da, ca := range a.terms {
		if maxDeg >= 0 && da > maxDeg {
			continue
		}
		for db, cb := range b.terms {
			d := da + db
			if maxDeg >= 0 && d > maxDeg {
				continue
			}
			res.addInto(d, mulCoeff(ca, cb))
		}
	}
	return res
}

// Conj returns the coefficient-wise complex conjugate of p.
func Conj(p *Poly) *Poly {
	res := New()
	for d, c := range p.terms {
		res.terms[d] = conjCoeff(c)
	}
	return res
}

// Equal reports whether a and b have identical terms.
func Equal(a, b *Poly) bool {
	if len(a.terms) != len(b.terms) {
		return false
	}
	for d, ca := range a.terms {
		cb, ok := b.terms[d]
		if !ok || !ca.eq(cb) {
			return false
		}
	}
	return true
}

// String renders p with coefficients in lowest terms, e.g.
// "(1/2-3/4i)*x^3 + (2)". The zero polynomial renders as "0".
func (p *Poly) String() string {
	if len(p.terms) == 0 {
		return "0"
	}
	parts := make([]string, 0, len(p.terms))
	for _, d := range p.Degrees() {
		c := p.terms[d]
		var b strings.Builder
		b.WriteByte('(')
		b.WriteString(c.Re.RatString())
		if c.Im.Sign() != 0 {
			if c.Im.Sign() > 0 {
				b.WriteByte('+')
			}
			b.WriteString(c.Im.RatString())
			b.WriteByte('i')
		}
		b.WriteByte(')')
		if d > 0 {
			b.WriteString("*x^")
			b.WriteString(strconv.Itoa(d))
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, " + ")
}

// key is a canonical serialization used for cache lookups.
func (p *Poly) key() string {
	var b strings.Builder
	for _, d := range p.Degrees() {
		c := p.terms[d]
		b.WriteString(strconv.Itoa(d))
		b.WriteByte(':')
		b.WriteString(c.Re.RatString())
		b.WriteByte(',')
		b.WriteString(c.Im.RatString())
		b.WriteByte(';')
	}
	return b.String()
}

// Cache memoizes products. Every call returns a fresh deep copy, so a
// caller mutating a returned polynomial can never corrupt cached entries.
type Cache struct {
	mu  sync.Mutex
	mul map[string]*Poly
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{mul: make(map[string]*Poly)}
}

// Mul returns a*b, reusing a previously computed product when possible.
func (c *Cache) Mul(a, b *Poly) *Poly {
	k := a.key() + "*" + b.key()
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.mul[k]; ok {
		return r.Clone()
	}
	r := Mul(a, b)
	c.mul[k] = r.Clone()
	return r
}
