package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// ErrTooManyShared is returned when more than MaxShared shared variables are
// supplied. The enumeration bound of the package depends on that limit.
var ErrTooManyShared = errors.New("netsec: more than 12 shared variables")

// ErrInvalidInput is returned for malformed inputs: non-positive or duplicate
// shared variables, the zero literal, and similar structural violations.
var ErrInvalidInput = errors.New("netsec: invalid input")

// Literal is a propositional literal encoded DIMACS-style. Variable v (v > 0)
// is represented by +v, its negation by -v. The zero literal is not valid.
type Literal int

// Lit returns the positive literal of variable v. It panics when v is not a
// positive variable id.
func Lit(v int) Literal {
	if v <= 0 {
		panic("netsec: variable ids must be positive")
	}
	return Literal(v)
}

// Not returns the negation of the literal.
func (l Literal) Not() Literal {
	if l == 0 {
		panic("netsec: literal 0 is not a valid literal")
	}
	return -l
}

// Var returns the variable of the literal.
func (l Literal) Var() int {
	if l == 0 {
		panic("netsec: literal 0 is not a valid literal")
	}
	if l < 0 {
		return int(-l)
	}
	return int(l)
}

// IsPositive reports whether the literal is a positive one.
func (l Literal) IsPositive() bool {
	if l == 0 {
		panic("netsec: literal 0 is not a valid literal")
	}
	return l > 0
}

func (l Literal) String() string {
	if l < 0 {
		return fmt.Sprintf("¬x%d", int(-l))
	}
	return fmt.Sprintf("x%d", int(l))
}

// Clause is a disjunction of literals. An empty Clause is unsatisfiable.
type Clause []Literal

// CNF is a conjunction of clauses.
type CNF []Clause

// Vars returns the sorted, deduplicated variable ids occurring in f.
func (f CNF) Vars() []int {
	seen := make(map[int]struct{})
	for _, c := range f {
		for _, l := range c {
			seen[l.Var()] = struct{}{}
		}
	}
	out := make([]int, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// MaxVar returns the largest variable id occurring in f, or 0 when f is empty.
func (f CNF) MaxVar() int {
	m := 0
	for _, c := range f {
		for _, l := range c {
			if v := l.Var(); v > m {
				m = v
			}
		}
	}
	return m
}
