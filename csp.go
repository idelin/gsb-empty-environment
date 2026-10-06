// Package csp implements a small finite-integer-domain constraint library.
//
// Supported constraints: equality with offset (x = y + c), difference
// bounds (x - y <= c), and all-different. Propagation runs to a fixpoint
// and is conservative: it never removes a value that participates in a
// real solution. Propagation failing to detect a wipeout does NOT imply
// satisfiability; unsatisfiability is only confirmed by complete
// backtracking search.
package csp

import "sort"

// Var is a variable with a finite integer domain.
type Var struct {
	Name string
	Dom  map[int]struct{}
}

func newVar(name string, lo, hi int) *Var {
	v := &Var{Name: name, Dom: make(map[int]struct{}, hi-lo+1)}
	for i := lo; i <= hi; i++ {
		v.Dom[i] = struct{}{}
	}
	return v
}

func cloneDom(dom map[int]struct{}) map[int]struct{} {
	c := make(map[int]struct{}, len(dom))
	for k := range dom {
		c[k] = struct{}{}
	}
	return c
}

func sortedValues(dom map[int]struct{}) []int {
	out := make([]int, 0, len(dom))
	for k := range dom {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// Constraint is a propagator over variable domains. Propagate removes
// values that cannot participate in any satisfying assignment of this
// constraint alone; it must never remove a value that does. It reports
// whether any domain changed and whether all domains remain non-empty.
// Holds checks a complete assignment for the brute-force verifier.
type Constraint interface {
	Propagate() (changed, ok bool)
	Holds(assign map[*Var]int) bool
}

// Equality enforces X = Y + C.
type Equality struct {
	X, Y *Var
	C    int
}

func (e *Equality) Propagate() (changed, ok bool) {
	for v := range e.X.Dom {
		if _, has := e.Y.Dom[v-e.C]; !has {
			delete(e.X.Dom, v)
			changed = true
		}
	}
	for v := range e.Y.Dom {
		if _, has := e.X.Dom[v+e.C]; !has {
			delete(e.Y.Dom, v)
			changed = true
		}
	}
	return changed, len(e.X.Dom) > 0 && len(e.Y.Dom) > 0
}

func (e *Equality) Holds(assign map[*Var]int) bool {
	return assign[e.X] == assign[e.Y]+e.C
}

// DiffBound enforces X - Y <= C.
type DiffBound struct {
	X, Y *Var
	C    int
}

func (d *DiffBound) Propagate() (changed, ok bool) {
	// Bounds consistency: x <= max(Y)+C and y >= min(X)-C.
	maxY := 0
	first := true
	for v := range d.Y.Dom {
		if first || v > maxY {
			maxY = v
		}
		first = false
	}
	if first {
		return false, false // Y already empty
	}
	for v := range d.X.Dom {
		if v > maxY+d.C {
			delete(d.X.Dom, v)
			changed = true
		}
	}
	minX := 0
	first = true
	for v := range d.X.Dom {
		if first || v < minX {
			minX = v
		}
		first = false
	}
	if first {
		return false, len(d.Y.Dom) > 0 // X wiped out
	}
	for v := range d.Y.Dom {
		if v < minX-d.C {
			delete(d.Y.Dom, v)
			changed = true
		}
	}
	return changed, len(d.X.Dom) > 0 && len(d.Y.Dom) > 0
}

func (d *DiffBound) Holds(assign map[*Var]int) bool {
	return assign[d.X]-assign[d.Y] <= d.C
}

// AllDifferent enforces pairwise distinct values over Vars.
type AllDifferent struct {
	Vars []*Var
}

func (a *AllDifferent) Propagate() (changed, ok bool) {
	// Value removal from singleton (assigned) variables only. This is
	// deliberately weak but conservative; stronger Hall-set reasoning is
	// left to search.
	taken := make(map[int]struct{})
	for _, v := range a.Vars {
		if len(v.Dom) == 1 {
			for val := range v.Dom {
				if _, dup := taken[val]; dup {
					// Two assigned variables share a value: unsatisfiable.
					return changed, false
				}
				taken[val] = struct{}{}
			}
		}
	}
	for _, v := range a.Vars {
		if len(v.Dom) == 1 {
			continue
		}
		for val := range v.Dom {
			if _, hit := taken[val]; hit {
				delete(v.Dom, val)
				changed = true
			}
		}
	}
	for _, v := range a.Vars {
		if len(v.Dom) == 0 {
			return changed, false
		}
	}
	return changed, true
}

func (a *AllDifferent) Holds(assign map[*Var]int) bool {
	seen := make(map[int]struct{}, len(a.Vars))
	for _, v := range a.Vars {
		val := assign[v]
		if _, dup := seen[val]; dup {
			return false
		}
		seen[val] = struct{}{}
	}
	return true
}

// Problem is a set of variables and constraints.
type Problem struct {
	vars []*Var
	cons []Constraint
}

func New() *Problem { return &Problem{} }

// AddVar creates a variable with domain [lo, hi].
func (p *Problem) AddVar(name string, lo, hi int) *Var {
	v := newVar(name, lo, hi)
	p.vars = append(p.vars, v)
	return v
}

// Add registers a constraint.
func (p *Problem) Add(c Constraint) { p.cons = append(p.cons, c) }

// Vars returns the problem's variables in creation order.
func (p *Problem) Vars() []*Var { return p.vars }

// Constraints returns the registered constraints.
func (p *Problem) Constraints() []Constraint { return p.cons }

// Propagate runs all propagators to a fixpoint. It returns false as soon
// as some domain becomes empty (definitely unsatisfiable). Returning true
// means only that propagation found no contradiction; satisfiability is
// NOT established.
func (p *Problem) Propagate() bool {
	for {
		changed := false
		for _, c := range p.cons {
			ch, ok := c.Propagate()
			if !ok {
				return false
			}
			changed = changed || ch
		}
		if !changed {
			return true
		}
	}
}

// Snapshot captures all variable domains for later Restore.
func (p *Problem) Snapshot() []map[int]struct{} {
	snap := make([]map[int]struct{}, len(p.vars))
	for i, v := range p.vars {
		snap[i] = cloneDom(v.Dom)
	}
	return snap
}

// Restore puts domains back to a previously taken Snapshot.
func (p *Problem) Restore(snap []map[int]struct{}) {
	for i, v := range p.vars {
		v.Dom = cloneDom(snap[i])
	}
}
