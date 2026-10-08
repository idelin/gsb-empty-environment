package updater

// Solution is a feasible selection of versions for the active dependency
// closure, together with its objective values.
//
// Objective ordering (lexicographic, most significant first):
//  1. Feasibility: no banned version, every dependency requirement met.
//  2. Changes: number of locked packages whose version differs from the
//     lock file. Lower is better. Newly added dependencies never count.
//  3. Vector: versions of the locked packages in sorted-name order.
//     Higher (lexicographically) is better.
//  4. Total: number of packages in the active closure, including newly
//     added dependencies. Lower is better (secondary objective).
type Solution struct {
	Selected map[string]Version `json:"selected"`
	Changes  int                `json:"changes"`
	Total    int                `json:"total"`
	vector   []Version
}

// Vector returns the objective version vector: versions of locked packages
// in sorted-name order.
func (s *Solution) Vector() []Version {
	out := make([]Version, len(s.vector))
	copy(out, s.vector)
	return out
}

// less reports whether a is strictly better than b under the objective
// ordering.
func less(a, b *Solution) bool {
	if a.Changes != b.Changes {
		return a.Changes < b.Changes
	}
	for i := range a.vector {
		if c := a.vector[i].Compare(b.vector[i]); c != 0 {
			return c > 0
		}
	}
	return a.Total < b.Total
}

// equivalent reports whether two solutions tie on every objective.
func equivalent(a, b *Solution) bool {
	return !less(a, b) && !less(b, a)
}

// buildSolution evaluates a complete assignment (a candidate version for
// every package in the universe). It computes the active closure from the
// locked roots and reports whether the assignment is feasible.
func (p *Problem) buildSolution(assign map[string]Version) (*Solution, bool) {
	active := map[string]bool{}
	queue := p.lockedNames()
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		if active[pkg] {
			continue
		}
		v, ok := assign[pkg]
		if !ok {
			return nil, false // required package has no selectable version
		}
		active[pkg] = true
		for _, r := range p.requirements(pkg, v) {
			queue = append(queue, r.Pkg)
		}
	}

	selected := make(map[string]Version, len(active))
	for pkg := range active {
		v := assign[pkg]
		if p.isBanned(pkg, v) {
			return nil, false
		}
		for _, r := range p.requirements(pkg, v) {
			tv, ok := assign[r.Pkg]
			if !ok || !r.Allows(tv) {
				return nil, false
			}
		}
		selected[pkg] = v
	}

	sol := &Solution{Selected: selected, Total: len(selected)}
	names := p.lockedNames()
	sol.vector = make([]Version, len(names))
	for i, name := range names {
		sol.vector[i] = selected[name]
		if selected[name] != p.Locked[name] {
			sol.Changes++
		}
	}
	return sol, true
}

// Check verifies that s is a feasible, self-consistent solution: its
// selected set equals the dependency closure of the locked roots, no
// selected version is banned, and every requirement is satisfied.
func (p *Problem) Check(s *Solution) bool {
	if s == nil {
		return false
	}
	rebuilt, ok := p.buildSolution(s.Selected)
	if !ok {
		return false
	}
	if len(rebuilt.Selected) != len(s.Selected) {
		return false
	}
	for pkg, v := range rebuilt.Selected {
		if s.Selected[pkg] != v {
			return false
		}
	}
	return rebuilt.Changes == s.Changes && rebuilt.Total == s.Total
}
