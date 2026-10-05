// Package unitinfer implements a unit inference engine.
//
// Units are represented as integer exponent vectors over a fixed set of
// base quantities (e.g. L, M, T). Unknown units of expression variables are
// represented by unit variables; a unit term is a linear combination
//
//	base exponent vector + sum_i c_i * u_i
//
// with integer coefficients c_i over unit variables u_i. Unification of
// unit terms therefore reduces to solving a system of linear Diophantine
// equations, which is solved exactly over Q with an integer null-space
// basis, yielding the most general (parametric) constraint solution.
package unitinfer

import (
	"fmt"
	"sort"
	"strings"
)

// Term is a linear unit expression: a constant base-dimension exponent
// vector plus integer coefficients on unit variables.
type Term struct {
	Base   []int       // constant exponent vector, length = number of base dims
	Coeffs map[int]int // unit variable id -> coefficient (non-zero entries only)
}

// NewTerm returns the zero (dimensionless) term over dims base quantities.
func NewTerm(dims int) Term {
	return Term{Base: make([]int, dims), Coeffs: map[int]int{}}
}

// BaseTerm returns a constant term with the given exponents.
func BaseTerm(exps ...int) Term {
	b := make([]int, len(exps))
	copy(b, exps)
	return Term{Base: b, Coeffs: map[int]int{}}
}

// VarTerm returns the term consisting of the single unit variable v.
func VarTerm(dims, v int) Term {
	t := NewTerm(dims)
	t.Coeffs[v] = 1
	return t
}

// Dims returns the number of base quantities.
func (t Term) Dims() int { return len(t.Base) }

func (t Term) clone() Term {
	b := make([]int, len(t.Base))
	copy(b, t.Base)
	c := make(map[int]int, len(t.Coeffs))
	for v, k := range t.Coeffs {
		c[v] = k
	}
	return Term{Base: b, Coeffs: c}
}

// Add returns t+o.
func (t Term) Add(o Term) Term {
	r := t.clone()
	for i, x := range o.Base {
		r.Base[i] += x
	}
	for v, k := range o.Coeffs {
		r.Coeffs[v] += k
		if r.Coeffs[v] == 0 {
			delete(r.Coeffs, v)
		}
	}
	return r
}

// Neg returns -t.
func (t Term) Neg() Term { return t.Scale(-1) }

// Sub returns t-o.
func (t Term) Sub(o Term) Term { return t.Add(o.Neg()) }

// Scale returns k*t.
func (t Term) Scale(k int) Term {
	r := Term{Base: make([]int, len(t.Base)), Coeffs: map[int]int{}}
	for i, x := range t.Base {
		r.Base[i] = k * x
	}
	for v, c := range t.Coeffs {
		r.Coeffs[v] = k * c
	}
	return r
}

// IsZero reports whether t is the identically zero term.
func (t Term) IsZero() bool {
	for _, x := range t.Base {
		if x != 0 {
			return false
		}
	}
	return len(t.Coeffs) == 0
}

// Subst replaces unit variables according to mapping (variables absent from
// the mapping are kept).
func (t Term) Subst(mapping map[int]int) Term {
	r := Term{Base: make([]int, len(t.Base)), Coeffs: map[int]int{}}
	copy(r.Base, t.Base)
	for v, c := range t.Coeffs {
		nv := v
		if m, ok := mapping[v]; ok {
			nv = m
		}
		r.Coeffs[nv] += c
		if r.Coeffs[nv] == 0 {
			delete(r.Coeffs, nv)
		}
	}
	return r
}

// Vars returns the sorted unit variable ids occurring in t.
func (t Term) Vars() []int {
	vs := make([]int, 0, len(t.Coeffs))
	for v := range t.Coeffs {
		vs = append(vs, v)
	}
	sort.Ints(vs)
	return vs
}

// String renders the term symbolically.
func (t Term) String(dimNames []string, varName func(int) string) string {
	var parts []string
	for i, e := range t.Base {
		if e == 0 {
			continue
		}
		name := "d" + itoa(i)
		if i < len(dimNames) && dimNames[i] != "" {
			name = dimNames[i]
		}
		parts = append(parts, pow(name, e))
	}
	for _, v := range t.Vars() {
		parts = append(parts, pow(varName(v), t.Coeffs[v]))
	}
	if len(parts) == 0 {
		return "1"
	}
	return strings.Join(parts, "*")
}

func pow(name string, e int) string {
	if e == 1 {
		return name
	}
	return fmt.Sprintf("%s^%d", name, e)
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
