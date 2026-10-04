// Package provenance implements how-provenance tracking for relational
// queries over multiset (bag) semantics.
//
// Every base row is annotated with an independent symbol (variable). Query
// operators propagate annotations through the provenance semiring N[X]:
//
//   - union and projection add polynomials (duplicate contributions are
//     accumulated into integer coefficients, never collapsed into a set),
//   - join multiplies polynomials.
//
// The annotation of each output row is therefore a polynomial with
// non-negative big-integer coefficients whose monomials record exactly which
// combinations of base rows contributed to it, and how many times.
//
// Deleting a base row tagged with variable x corresponds to evaluating the
// polynomials at x = 0, which yields an incremental update path that this
// package cross-checks against direct recomputation.
package provenance

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Monomial is a product of variables, represented as variable id -> exponent.
// The empty monomial is the constant 1.
type Monomial struct {
	Exps map[uint64]uint32
}

// VarMonomial returns the monomial consisting of a single variable.
func VarMonomial(id uint64) Monomial {
	return Monomial{Exps: map[uint64]uint32{id: 1}}
}

// Key returns the canonical string form of the monomial, e.g. "x2*x5^3".
// The constant monomial has key "1".
func (m Monomial) Key() string {
	if len(m.Exps) == 0 {
		return "1"
	}
	ids := make([]uint64, 0, len(m.Exps))
	for id := range m.Exps {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteByte('*')
		}
		fmt.Fprintf(&b, "x%d", id)
		if e := m.Exps[id]; e > 1 {
			fmt.Fprintf(&b, "^%d", e)
		}
	}
	return b.String()
}

// Degree is the total degree of the monomial.
func (m Monomial) Degree() uint64 {
	var d uint64
	for _, e := range m.Exps {
		d += uint64(e)
	}
	return d
}

// Mul returns the product of two monomials (exponents are added).
func (m Monomial) Mul(o Monomial) Monomial {
	r := Monomial{Exps: make(map[uint64]uint32, len(m.Exps)+len(o.Exps))}
	for id, e := range m.Exps {
		r.Exps[id] = e
	}
	for id, e := range o.Exps {
		r.Exps[id] += e
	}
	return r
}

// HasVar reports whether the variable occurs in the monomial.
func (m Monomial) HasVar(id uint64) bool {
	_, ok := m.Exps[id]
	return ok
}

// Term is a monomial together with its big-integer coefficient.
type Term struct {
	Mon  Monomial
	Coef *big.Int
}

// Poly is a multivariate polynomial with big-integer coefficients, stored in
// normal form: like terms are always combined, and zero coefficients are
// never stored.
type Poly struct {
	Terms map[string]*Term // monomial key -> term
}

// Zero returns the zero polynomial.
func Zero() *Poly { return &Poly{Terms: map[string]*Term{}} }

// VarPoly returns the polynomial 1*x_id.
func VarPoly(id uint64) *Poly {
	m := VarMonomial(id)
	p := Zero()
	p.Terms[m.Key()] = &Term{Mon: m, Coef: big.NewInt(1)}
	return p
}

// ConstPoly returns a constant polynomial.
func ConstPoly(c *big.Int) *Poly {
	p := Zero()
	if c.Sign() != 0 {
		m := Monomial{Exps: map[uint64]uint32{}}
		p.Terms[m.Key()] = &Term{Mon: m, Coef: new(big.Int).Set(c)}
	}
	return p
}

// Clone deep-copies the polynomial.
func (p *Poly) Clone() *Poly {
	q := Zero()
	for k, t := range p.Terms {
		q.Terms[k] = &Term{Mon: t.Mon, Coef: new(big.Int).Set(t.Coef)}
	}
	return q
}

// addTerm accumulates c*m into p, combining like terms and dropping zeros.
func (p *Poly) addTerm(m Monomial, c *big.Int) {
	k := m.Key()
	if t, ok := p.Terms[k]; ok {
		t.Coef.Add(t.Coef, c)
		if t.Coef.Sign() == 0 {
			delete(p.Terms, k)
		}
		return
	}
	if c.Sign() == 0 {
		return
	}
	p.Terms[k] = &Term{Mon: m, Coef: new(big.Int).Set(c)}
}

// AddInto adds q into p in place and returns p.
func (p *Poly) AddInto(q *Poly) *Poly {
	for _, t := range q.Terms {
		p.addTerm(t.Mon, t.Coef)
	}
	return p
}

// Add returns p + q as a new polynomial.
func (p *Poly) Add(q *Poly) *Poly { return p.Clone().AddInto(q) }

// Sub returns p - q as a new polynomial. Coefficients may become negative;
// this is used for delta computation, not for provenance annotations.
func (p *Poly) Sub(q *Poly) *Poly {
	r := p.Clone()
	for _, t := range q.Terms {
		r.addTerm(t.Mon, new(big.Int).Neg(t.Coef))
	}
	return r
}

// Mul returns p * q as a new polynomial, combining like terms.
func (p *Poly) Mul(q *Poly) *Poly {
	r := Zero()
	for _, a := range p.Terms {
		for _, b := range q.Terms {
			r.addTerm(a.Mon.Mul(b.Mon), new(big.Int).Mul(a.Coef, b.Coef))
		}
	}
	return r
}

// DropVar evaluates the polynomial at x_id = 0: every monomial containing
// the variable vanishes, the rest are kept unchanged. This models deleting
// the base row tagged with id.
func (p *Poly) DropVar(id uint64) *Poly {
	r := Zero()
	for _, t := range p.Terms {
		if !t.Mon.HasVar(id) {
			r.Terms[t.Mon.Key()] = &Term{Mon: t.Mon, Coef: new(big.Int).Set(t.Coef)}
		}
	}
	return r
}

// Vars returns the sorted set of variable ids occurring in p.
func (p *Poly) Vars() []uint64 {
	seen := map[uint64]bool{}
	for _, t := range p.Terms {
		for id := range t.Mon.Exps {
			seen[id] = true
		}
	}
	ids := make([]uint64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// IsZero reports whether p is the zero polynomial.
func (p *Poly) IsZero() bool { return len(p.Terms) == 0 }

// Equal reports whether p and q are the same normal-form polynomial.
func (p *Poly) Equal(q *Poly) bool {
	if len(p.Terms) != len(q.Terms) {
		return false
	}
	for k, t := range p.Terms {
		u, ok := q.Terms[k]
		if !ok || t.Coef.Cmp(u.Coef) != 0 {
			return false
		}
	}
	return true
}

// String renders the polynomial in a stable, human-readable form such as
// "2*x1^2*x2 + x3". Terms are ordered by degree, then by monomial key.
func (p *Poly) String() string {
	if p.IsZero() {
		return "0"
	}
	ts := make([]*Term, 0, len(p.Terms))
	for _, t := range p.Terms {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool {
		di, dj := ts[i].Mon.Degree(), ts[j].Mon.Degree()
		if di != dj {
			return di < dj
		}
		return ts[i].Mon.Key() < ts[j].Mon.Key()
	})
	parts := make([]string, 0, len(ts))
	for i, t := range ts {
		mon := t.Mon.Key()
		abs := new(big.Int).Abs(t.Coef)
		var body string
		switch {
		case mon == "1":
			body = abs.String()
		case abs.Cmp(big.NewInt(1)) == 0:
			body = mon
		default:
			body = abs.String() + "*" + mon
		}
		switch {
		case i == 0 && t.Coef.Sign() < 0:
			parts = append(parts, "-"+body)
		case i == 0:
			parts = append(parts, body)
		case t.Coef.Sign() < 0:
			parts = append(parts, "- "+body)
		default:
			parts = append(parts, "+ "+body)
		}
	}
	return strings.Join(parts, " ")
}
