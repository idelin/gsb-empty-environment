package unitinfer

import "sort"

// MinimalConflict extracts a minimal unsatisfiable subset (MUS) of an
// inconsistent constraint system using deletion-based minimization: every
// constraint in the returned set is necessary for the conflict.
func MinimalConflict(dims int, cons []Constraint, vars []int) []Constraint {
	core := make([]Constraint, len(cons))
	copy(core, cons)
	i := 0
	for i < len(core) {
		cand := make([]Constraint, 0, len(core)-1)
		cand = append(cand, core[:i]...)
		cand = append(cand, core[i+1:]...)
		if !Consistent(dims, cand, vars) {
			core = cand
		} else {
			i++
		}
	}
	return core
}

// ConflictNodes returns the sorted set of expression node ids involved in a
// (minimal) conflict — the minimal conflicting expression set.
func ConflictNodes(conflict []Constraint) []int {
	set := map[int]bool{}
	for _, c := range conflict {
		set[c.Node] = true
	}
	nodes := make([]int, 0, len(set))
	for n := range set {
		nodes = append(nodes, n)
	}
	sort.Ints(nodes)
	return nodes
}
