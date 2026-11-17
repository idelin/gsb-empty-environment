package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Term is a conjunction of literals, i.e. one minterm of the DNF interpolant.
type Term []Literal

// Eval reports whether the term is satisfied by assignment a. A variable
// missing from a evaluates to false; an empty term is the constant true.
func (t Term) Eval(a map[int]bool) bool {
	for _, l := range t {
		v, ok := a[l.Var()]
		if !ok {
			return false
		}
		if v != l.IsPositive() {
			return false
		}
	}
	return true
}

// Interpolant is a formula over the shared variables, stored in disjunctive
// normal form. The constants are represented explicitly: True means the term
// slice contains one empty Term, False means an empty term slice.
type Interpolant struct {
	Terms []Term
	// Shared is the sorted vocabulary the interpolant is built from.
	Shared []int
}

// Eval reports whether the interpolant holds under assignment a.
func (it *Interpolant) Eval(a map[int]bool) bool {
	for _, t := range it.Terms {
		if t.Eval(a) {
			return true
		}
	}
	return false
}

func (it *Interpolant) String() string {
	if len(it.Terms) == 0 {
		return "false"
	}
	if len(it.Terms) == 1 && len(it.Terms[0]) == 0 {
		return "true"
	}
	parts := make([]string, 0, len(it.Terms))
	for _, t := range it.Terms {
		if len(t) == 0 {
			parts = append(parts, "true")
			continue
		}
		ls := make([]string, len(t))
		for i, l := range t {
			ls[i] = l.String()
		}
		parts = append(parts, "("+strings.Join(ls, " ∧ ")+")")
	}
	return strings.Join(parts, " ∨ ")
}

// Witness is a satisfying assignment for the conjunction of two CNFs. It is
// only returned when the conjunction is satisfiable, so the caller never
// receives a fabricated safety proof.
type Witness struct {
	Assignment map[int]bool
}

// Eval reads the value of literal l under the witness assignment. An
// unassigned variable reads as false.
func (w *Witness) Eval(l Literal) bool {
	v, ok := w.Assignment[l.Var()]
	return ok && v == l.IsPositive()
}

// Satisfies reports whether the witness satisfies every clause of f.
func (w *Witness) Satisfies(f CNF) bool {
	for _, c := range f {
		ok := false
		for _, l := range c {
			if w.Eval(l) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func (w *Witness) String() string {
	keys := make([]int, 0, len(w.Assignment))
	for v := range w.Assignment {
		keys = append(keys, v)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, v := range keys {
		if !w.Assignment[v] {
			parts = append(parts, fmt.Sprintf("¬x%d", v))
		} else {
			parts = append(parts, fmt.Sprintf("x%d", v))
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
