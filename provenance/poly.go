// Package provenance implements how-provenance (the provenance semiring
// N[X]) for multiset relational algebra.
//
// Every base row is annotated with its own symbol. Projection, union and
// join propagate provenance polynomials over those symbols: union and
// projection add polynomials, join multiplies them. Like terms are combined
// with arbitrary-precision integer coefficients, so duplicate contributions
// are counted, not collapsed into a set.
package provenance

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Monomial is a product of provenance symbols with positive exponents.
type Monomial struct {
	Vars map[string]int64
}

// NewMonomial builds a canonical monomial, dropping non-positive exponents.
func NewMonomial(vars map[string]int64) Monomial {
	v := make(map[string]int64, len(vars))
	for k, n := range vars {
		if n > 0 {
			v[k] = n
		}
	}
	return Monomial{Vars: v}
}

// Symbol returns the degree-1 monomial for a single symbol.
func Symbol(sym string) Monomial {
	return Monomial{Vars: map[string]int64{sym: 1}}
}

// Key is the canonical string form, e.g. "r1^2*s3". The empty monomial
// (the constant 1) has key "".
func (m Monomial) Key() string {
	if len(m.Vars) == 0 {
		return ""
	}
	names := make([]string, 0, len(m.Vars))
	for k := range m.Vars {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, k := range names {
		if i > 0 {
			b.WriteByte('*')
		}
		b.WriteString(k)
		if n := m.Vars[k]; n != 1 {
			fmt.Fprintf(&b, "^%d", n)
		}
	}
	return b.String()
}

// Mul multiplies two monomials by merging exponents.
func (m Monomial) Mul(o Monomial) Monomial {
	v := make(map[string]int64, len(m.Vars)+len(o.Vars))
	for k, n := range m.Vars {
		v[k] += n
	}
	for k, n := range o.Vars {
		v[k] += n
	}
	return NewMonomial(v)
}

// Has reports whether the monomial contains the given symbol.
func (m Monomial) Has(sym string) bool {
	_, ok := m.Vars[sym]
	return ok
}

type term struct {
	mon  Monomial
	coef *big.Int
}

// Poly is a provenance polynomial: a map from monomials to big integer
// coefficients. Identical monomials are always combined into one term.
type Poly struct {
	terms map[string]term
}

// Zero returns the zero polynomial.
func Zero() Poly {
	return Poly{terms: make(map[string]term)}
}

// Const returns a constant polynomial.
func Const(c int64) Poly {
	p := Zero()
	if c != 0 {
		p.terms[""] = term{mon: NewMonomial(nil), coef: big.NewInt(c)}
	}
	return p
}

// Var returns the polynomial 1*sym.
func Var(sym string) Poly {
	p := Zero()
	p.terms[Symbol(sym).Key()] = term{mon: Symbol(sym), coef: big.NewInt(1)}
	return p
}

func (p Poly) addTerm(m Monomial, c *big.Int) {
	if c.Sign() == 0 {
		return
	}
	k := m.Key()
	if t, ok := p.terms[k]; ok {
		t.coef = new(big.Int).Add(t.coef, c)
		if t.coef.Sign() == 0 {
			delete(p.terms, k)
			return
		}
		p.terms[k] = t
		return
	}
	p.terms[k] = term{mon: m, coef: new(big.Int).Set(c)}
}

// Add returns p+q, combining like terms.
func (p Poly) Add(q Poly) Poly {
	r := Zero()
	for _, t := range p.terms {
		r.addTerm(t.mon, t.coef)
	}
	for _, t := range q.terms {
		r.addTerm(t.mon, t.coef)
	}
	return r
}

// Mul returns p*q, combining like terms.
func (p Poly) Mul(q Poly) Poly {
	r := Zero()
	for _, a := range p.terms {
		for _, b := range q.terms {
			r.addTerm(a.mon.Mul(b.mon), new(big.Int).Mul(a.coef, b.coef))
		}
	}
	return r
}

// Neg returns -p.
func (p Poly) Neg() Poly {
	r := Zero()
	for _, t := range p.terms {
		r.addTerm(t.mon, new(big.Int).Neg(t.coef))
	}
	return r
}

// Sub returns p-q.
func (p Poly) Sub(q Poly) Poly {
	return p.Add(q.Neg())
}

// IsZero reports whether p has no terms.
func (p Poly) IsZero() bool {
	return len(p.terms) == 0
}

// Equal reports structural equality of p and q.
func (p Poly) Equal(q Poly) bool {
	if len(p.terms) != len(q.terms) {
		return false
	}
	for k, t := range p.terms {
		u, ok := q.terms[k]
		if !ok || t.coef.Cmp(u.coef) != 0 {
			return false
		}
	}
	return true
}

// DropSymbol deletes every term whose monomial contains sym. Substituting
// sym = 0 models removing the base row that sym annotates.
func (p Poly) DropSymbol(sym string) Poly {
	r := Zero()
	for _, t := range p.terms {
		if !t.mon.Has(sym) {
			r.addTerm(t.mon, t.coef)
		}
	}
	return r
}

// TermsContaining returns the part of p whose monomials contain sym, i.e.
// the contribution that disappears when the row annotated by sym is deleted.
func (p Poly) TermsContaining(sym string) Poly {
	r := Zero()
	for _, t := range p.terms {
		if t.mon.Has(sym) {
			r.addTerm(t.mon, t.coef)
		}
	}
	return r
}

// Coefficient returns the coefficient of the monomial with the given key
// (as produced by Monomial.Key), or 0 if absent.
func (p Poly) Coefficient(key string) *big.Int {
	if t, ok := p.terms[key]; ok {
		return new(big.Int).Set(t.coef)
	}
	return big.NewInt(0)
}

// String renders the polynomial in a stable, human-readable form.
func (p Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	keys := make([]string, 0, len(p.terms))
	for k := range p.terms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		t := p.terms[k]
		if k == "" {
			parts = append(parts, t.coef.String())
			continue
		}
		if t.coef.Cmp(big.NewInt(1)) == 0 {
			parts = append(parts, k)
		} else {
			parts = append(parts, t.coef.String()+"*"+k)
		}
	}
	return strings.Join(parts, " + ")
}
