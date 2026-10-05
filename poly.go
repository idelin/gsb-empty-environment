package cpoly

import (
	"fmt"
	"sort"
	"strings"
)

// Poly is a sparse univariate polynomial with complex rational
// coefficients. Only non-zero terms are stored, so polynomials like
// x^1000000 + 1 occupy constant memory and never expand into dense
// arrays. All operations return new polynomials; a Poly is never
// mutated after construction, which makes cached derived values safe.
type Poly struct {
	coeffs map[int]Cplx // degree -> coefficient, no zero entries

	// Lazily filled caches. Validity is tracked separately so that
	// the zero polynomial's degree (-1) can be cached too.
	deg      int
	degValid bool
	str      string
	strValid bool
}

// NewPoly builds a polynomial from a degree -> coefficient map.
// The map and its values are copied; zero coefficients are dropped.
func NewPoly(terms map[int]Cplx) *Poly {
	coeffs := make(map[int]Cplx, len(terms))
	for d, c := range terms {
		if d < 0 {
			panic("cpoly: negative degree")
		}
		if c.IsZero() {
			continue
		}
		coeffs[d] = c.Copy()
	}
	return &Poly{coeffs: coeffs, deg: -1}
}

// Zero returns the zero polynomial.
func Zero() *Poly { return NewPoly(nil) }

// Mono returns the monomial c * x^deg.
func Mono(deg int, c Cplx) *Poly {
	return NewPoly(map[int]Cplx{deg: c})
}

// Const returns the constant polynomial c.
func Const(c Cplx) *Poly { return Mono(0, c) }

// X returns the polynomial x.
func X() *Poly { return Mono(1, NewCplxInt(1, 0)) }

// NumTerms reports the number of stored (non-zero) terms.
func (p *Poly) NumTerms() int { return len(p.coeffs) }

// IsZero reports whether p is the zero polynomial.
func (p *Poly) IsZero() bool { return len(p.coeffs) == 0 }

// Degree returns the highest degree with a non-zero coefficient,
// or -1 for the zero polynomial. The result is cached.
func (p *Poly) Degree() int {
	if p.degValid {
		return p.deg
	}
	d := -1
	for k := range p.coeffs {
		if k > d {
			d = k
		}
	}
	p.deg, p.degValid = d, true
	return p.deg
}

// Coeff returns a deep copy of the coefficient of x^deg, or zero if
// the term is absent. Mutating the returned value cannot affect p.
func (p *Poly) Coeff(deg int) Cplx {
	if c, ok := p.coeffs[deg]; ok {
		return c.Copy()
	}
	return ZeroCplx()
}

// Degrees returns the degrees of non-zero terms in ascending order.
func (p *Poly) Degrees() []int {
	ds := make([]int, 0, len(p.coeffs))
	for d := range p.coeffs {
		ds = append(ds, d)
	}
	sort.Ints(ds)
	return ds
}

// Equal reports exact coefficient-wise equality.
func (p *Poly) Equal(o *Poly) bool {
	if len(p.coeffs) != len(o.coeffs) {
		return false
	}
	for d, c := range p.coeffs {
		oc, ok := o.coeffs[d]
		if !ok || !c.Equal(oc) {
			return false
		}
	}
	return true
}

// Add returns p + o.
func (p *Poly) Add(o *Poly) *Poly {
	out := make(map[int]Cplx, max(len(p.coeffs), len(o.coeffs)))
	for d, c := range p.coeffs {
		out[d] = c.Copy()
	}
	for d, c := range o.coeffs {
		if cur, ok := out[d]; ok {
			sum := cur.Add(c)
			if sum.IsZero() {
				delete(out, d)
			} else {
				out[d] = sum
			}
		} else {
			out[d] = c.Copy()
		}
	}
	return &Poly{coeffs: out, deg: -1}
}

// Neg returns -p.
func (p *Poly) Neg() *Poly {
	out := make(map[int]Cplx, len(p.coeffs))
	for d, c := range p.coeffs {
		out[d] = c.Neg()
	}
	return &Poly{coeffs: out, deg: -1}
}

// Sub returns p - o.
func (p *Poly) Sub(o *Poly) *Poly { return p.Add(o.Neg()) }

// Mul returns p * o. The result is computed sparsely: work is
// proportional to len(p)*len(o) non-zero term pairs, independent of
// the degrees involved. No truncation is applied; use Truncate to
// discard high-degree terms explicitly.
func (p *Poly) Mul(o *Poly) *Poly {
	if p.IsZero() || o.IsZero() {
		return Zero()
	}
	out := make(map[int]Cplx)
	for dp, cp := range p.coeffs {
		for do, co := range o.coeffs {
			d := dp + do
			prod := cp.Mul(co)
			if cur, ok := out[d]; ok {
				sum := cur.Add(prod)
				if sum.IsZero() {
					delete(out, d)
				} else {
					out[d] = sum
				}
			} else if !prod.IsZero() {
				out[d] = prod
			}
		}
	}
	return &Poly{coeffs: out, deg: -1}
}

// Conj returns the polynomial with every coefficient conjugated.
func (p *Poly) Conj() *Poly {
	out := make(map[int]Cplx, len(p.coeffs))
	for d, c := range p.coeffs {
		out[d] = c.Conj()
	}
	return &Poly{coeffs: out, deg: -1}
}

// Truncate returns p with all terms of degree > maxDeg dropped.
// Truncation is never implicit: it happens only through this method.
func (p *Poly) Truncate(maxDeg int) *Poly {
	out := make(map[int]Cplx, len(p.coeffs))
	for d, c := range p.coeffs {
		if d <= maxDeg {
			out[d] = c.Copy()
		}
	}
	return &Poly{coeffs: out, deg: -1}
}

// AbsSqPoly returns p * Conj(p), whose coefficients are all real.
func (p *Poly) AbsSqPoly() *Poly { return p.Mul(p.Conj()) }

// String renders p with terms in descending degree order and all
// coefficients in lowest terms. The result is cached; strings are
// immutable so sharing the cached value is safe.
func (p *Poly) String() string {
	if p.strValid {
		return p.str
	}
	if p.IsZero() {
		p.str, p.strValid = "0", true
		return p.str
	}
	ds := p.Degrees()
	var b strings.Builder
	one := NewCplxInt(1, 0)
	negOne := NewCplxInt(-1, 0)
	for i := len(ds) - 1; i >= 0; i-- {
		d := ds[i]
		c := p.coeffs[d]
		cs := c.String()
		if d > 0 {
			if c.Equal(one) {
				cs = ""
			} else if c.Equal(negOne) {
				cs = "-"
			}
		}
		if cs != "" && cs != "-" && c.needsParens() {
			cs = "(" + cs + ")"
		}
		var term string
		switch d {
		case 0:
			term = cs
		case 1:
			term = cs + "x"
		default:
			term = fmt.Sprintf("%sx^%d", cs, d)
		}
		if b.Len() == 0 {
			b.WriteString(term)
		} else if strings.HasPrefix(term, "-") {
			b.WriteString(" - " + term[1:])
		} else {
			b.WriteString(" + " + term)
		}
	}
	p.str = b.String()
	p.strValid = true
	return p.str
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
