package csp

import "sort"

// Domain is a finite set of integer values.
type Domain map[int]struct{}

// NewDomain builds a domain containing every integer in [lo, hi].
func NewDomain(lo, hi int) Domain {
	d := make(Domain, hi-lo+1)
	for v := lo; v <= hi; v++ {
		d[v] = struct{}{}
	}
	return d
}

// Clone returns a deep copy of the domain.
func (d Domain) Clone() Domain {
	c := make(Domain, len(d))
	for v := range d {
		c[v] = struct{}{}
	}
	return c
}

// Values returns the domain members in ascending order.
func (d Domain) Values() []int {
	vs := make([]int, 0, len(d))
	for v := range d {
		vs = append(vs, v)
	}
	sort.Ints(vs)
	return vs
}

// Equal reports whether both domains contain exactly the same values.
func (d Domain) Equal(other Domain) bool {
	if len(d) != len(other) {
		return false
	}
	for v := range d {
		if _, ok := other[v]; !ok {
			return false
		}
	}
	return true
}

func (d Domain) min() int {
	first := true
	m := 0
	for v := range d {
		if first || v < m {
			m = v
			first = false
		}
	}
	return m
}

func (d Domain) max() int {
	first := true
	m := 0
	for v := range d {
		if first || v > m {
			m = v
			first = false
		}
	}
	return m
}

// Constraint prunes variable domains and checks complete assignments.
//
// Prune must be conservative: it may only remove values that cannot
// appear in any solution of the constraint given the current domains.
type Constraint interface {
	// Vars returns the indices of the constrained variables.
	Vars() []int
	// Prune removes impossible values from doms. It reports whether any
	// domain changed and whether every domain is still non-empty.
	Prune(doms []Domain) (changed, ok bool)
	// Check reports whether a complete assignment satisfies the
	// constraint. Every variable in Vars must be assigned.
	Check(assign []int) bool
}

// Equal enforces x == y.
type Equal struct{ X, Y int }

func (c Equal) Vars() []int { return []int{c.X, c.Y} }

func (c Equal) Prune(doms []Domain) (bool, bool) {
	changed := false
	for v := range doms[c.X] {
		if _, ok := doms[c.Y][v]; !ok {
			delete(doms[c.X], v)
			changed = true
		}
	}
	for v := range doms[c.Y] {
		if _, ok := doms[c.X][v]; !ok {
			delete(doms[c.Y], v)
			changed = true
		}
	}
	return changed, len(doms[c.X]) > 0 && len(doms[c.Y]) > 0
}

func (c Equal) Check(assign []int) bool { return assign[c.X] == assign[c.Y] }

// EqualConst enforces x == k.
type EqualConst struct {
	X int
	K int
}

func (c EqualConst) Vars() []int { return []int{c.X} }

func (c EqualConst) Prune(doms []Domain) (bool, bool) {
	changed := false
	for v := range doms[c.X] {
		if v != c.K {
			delete(doms[c.X], v)
			changed = true
		}
	}
	return changed, len(doms[c.X]) > 0
}

func (c EqualConst) Check(assign []int) bool { return assign[c.X] == c.K }

// DiffLe enforces x - y <= k (a difference bound).
type DiffLe struct {
	X, Y int
	K    int
}

func (c DiffLe) Vars() []int { return []int{c.X, c.Y} }

// Prune applies bounds consistency: x <= max(y)+k and y >= min(x)-k.
func (c DiffLe) Prune(doms []Domain) (bool, bool) {
	changed := false
	hiX := doms[c.Y].max() + c.K
	for v := range doms[c.X] {
		if v > hiX {
			delete(doms[c.X], v)
			changed = true
		}
	}
	loY := doms[c.X].min() - c.K
	for v := range doms[c.Y] {
		if v < loY {
			delete(doms[c.Y], v)
			changed = true
		}
	}
	return changed, len(doms[c.X]) > 0 && len(doms[c.Y]) > 0
}

func (c DiffLe) Check(assign []int) bool { return assign[c.X]-assign[c.Y] <= c.K }

// AllDifferent enforces pairwise distinct values on the given variables.
type AllDifferent struct{ Set []int }

func (c AllDifferent) Vars() []int { return append([]int(nil), c.Set...) }

// Prune removes the value of every singleton variable from all other
// variables. This is sound but incomplete: two singletons sharing a
// value are not detected here, only by search.
func (c AllDifferent) Prune(doms []Domain) (bool, bool) {
	changed := false
	for _, i := range c.Set {
		if len(doms[i]) != 1 {
			continue
		}
		single := doms[i].min()
		for _, j := range c.Set {
			if j == i || len(doms[j]) == 1 {
				continue
			}
			if _, ok := doms[j][single]; ok {
				delete(doms[j], single)
				changed = true
			}
		}
	}
	ok := true
	for _, i := range c.Set {
		if len(doms[i]) == 0 {
			ok = false
			break
		}
	}
	return changed, ok
}

func (c AllDifferent) Check(assign []int) bool {
	seen := make(map[int]struct{}, len(c.Set))
	for _, i := range c.Set {
		if _, dup := seen[assign[i]]; dup {
			return false
		}
		seen[assign[i]] = struct{}{}
	}
	return true
}

// Solver holds variable domains and constraints, and propagates every
// update to a fixpoint.
type Solver struct {
	doms []Domain
	cons []Constraint
}

// NewSolver creates n variables, each with domain [lo, hi].
func NewSolver(n, lo, hi int) *Solver {
	doms := make([]Domain, n)
	for i := range doms {
		doms[i] = NewDomain(lo, hi)
	}
	return &Solver{doms: doms}
}

// NumVars returns the number of variables.
func (s *Solver) NumVars() int { return len(s.doms) }

// AddConstraint registers a constraint. It does not propagate by
// itself; call Propagate (or any update method) to reach a fixpoint.
func (s *Solver) AddConstraint(c Constraint) {
	s.cons = append(s.cons, c)
}

// Domain returns a copy of variable i's current domain.
func (s *Solver) Domain(i int) Domain { return s.doms[i].Clone() }

// Propagate prunes domains until no constraint can remove anything
// further. It returns false as soon as some domain becomes empty,
// which proves the current constraints have no solution.
func (s *Solver) Propagate() bool {
	for {
		any := false
		for _, c := range s.cons {
			changed, ok := c.Prune(s.doms)
			if !ok {
				return false
			}
			any = any || changed
		}
		if !any {
			return true
		}
	}
}

// Restrict intersects variable i's domain with the given values and
// propagates to a fixpoint. Returns false if a domain empties out.
func (s *Solver) Restrict(i int, values ...int) bool {
	keep := make(map[int]struct{}, len(values))
	for _, v := range values {
		keep[v] = struct{}{}
	}
	for v := range s.doms[i] {
		if _, ok := keep[v]; !ok {
			delete(s.doms[i], v)
		}
	}
	if len(s.doms[i]) == 0 {
		return false
	}
	return s.Propagate()
}

// Assign fixes variable i to a single value and propagates.
func (s *Solver) Assign(i, v int) bool { return s.Restrict(i, v) }

// Snapshot returns a deep copy of all current domains, suitable for
// later Restore.
func (s *Solver) Snapshot() []Domain {
	snap := make([]Domain, len(s.doms))
	for i, d := range s.doms {
		snap[i] = d.Clone()
	}
	return snap
}

// Restore rolls the domains back to a previous Snapshot. Constraints
// are unaffected. The snapshot is copied again so the caller may reuse
// it for further restores.
func (s *Solver) Restore(snap []Domain) {
	for i := range s.doms {
		s.doms[i] = snap[i].Clone()
	}
}

// consistent reports whether a complete assignment satisfies every
// constraint.
func (s *Solver) consistent(assign []int) bool {
	for _, c := range s.cons {
		if !c.Check(assign) {
			return false
		}
	}
	return true
}

// Solve finds one solution by depth-first backtracking with fixpoint
// propagation at every node. It returns the assignment and true, or
// nil and false when the constraints are unsatisfiable.
func (s *Solver) Solve() ([]int, bool) {
	sols := s.enumerate(1)
	if len(sols) == 0 {
		return nil, false
	}
	return sols[0], true
}

// Solutions returns every solution, in lexicographic order of the
// assignment, found by complete backtracking search.
func (s *Solver) Solutions() [][]int { return s.enumerate(-1) }

// enumerate runs the backtracking search. limit < 0 collects all
// solutions. The solver's domains are left exactly as they were.
func (s *Solver) enumerate(limit int) [][]int {
	base := s.Snapshot()
	var sols [][]int
	s.search(&sols, limit)
	s.Restore(base)
	sort.Slice(sols, func(i, j int) bool {
		for k := range sols[i] {
			if sols[i][k] != sols[j][k] {
				return sols[i][k] < sols[j][k]
			}
		}
		return false
	})
	return sols
}

func (s *Solver) search(sols *[][]int, limit int) bool {
	if limit >= 0 && len(*sols) >= limit {
		return true
	}
	// Pick the unassigned variable with the smallest domain.
	idx := -1
	for i, d := range s.doms {
		if len(d) > 1 && (idx == -1 || len(d) < len(s.doms[idx])) {
			idx = i
		}
	}
	if idx == -1 {
		assign := make([]int, len(s.doms))
		for i, d := range s.doms {
			assign[i] = d.min()
		}
		// Propagation is sound but incomplete, so verify the leaf.
		if s.consistent(assign) {
			*sols = append(*sols, assign)
		}
		return limit >= 0 && len(*sols) >= limit
	}
	snap := s.Snapshot()
	for _, v := range snap[idx].Values() {
		s.Restore(snap)
		if !s.Assign(idx, v) {
			continue
		}
		if s.search(sols, limit) {
			s.Restore(snap)
			return true
		}
	}
	s.Restore(snap)
	return false
}
