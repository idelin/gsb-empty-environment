package updater

import "sort"

// Solve finds an optimal solution by backtracking search over the reachable
// dependency closure, with pruning. It returns nil when no feasible
// selection exists. The result is guaranteed to be objective-optimal;
// BruteForce verifies this by full enumeration.
//
// Only packages reachable from the locked roots are assigned: requirements
// of packages outside the active closure impose nothing, so assigning them
// would prune valid solutions.
func Solve(p *Problem) *Solution {
	names := p.universe()
	cands := make(map[string][]Version, len(names))
	for _, name := range names {
		vs := append([]Version(nil), p.Candidates[name]...)
		sort.Slice(vs, func(i, j int) bool { return vs[i].Compare(vs[j]) > 0 })
		cands[name] = vs
	}

	var best *Solution
	assign := make(map[string]Version, len(names))
	imposed := map[string][]Requirement{} // dependency target -> active requirements
	reachable := map[string]int{}         // refcount of incoming requirement edges
	for name := range p.Locked {
		reachable[name] = 1
	}

	// viable reports whether every package constrained so far can still
	// (or already does) satisfy all requirements imposed on it.
	viable := func() bool {
		for target, reqs := range imposed {
			if v, ok := assign[target]; ok {
				for _, r := range reqs {
					if !r.Allows(v) {
						return false
					}
				}
				continue
			}
			ok := false
			for _, v := range cands[target] {
				if p.isBanned(target, v) {
					continue
				}
				good := true
				for _, r := range reqs {
					if !r.Allows(v) {
						good = false
						break
					}
				}
				if good {
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

	var rec func(changes int)
	rec = func(changes int) {
		if best != nil && changes > best.Changes {
			return // changes only grow; the primary objective is already worse
		}
		// Pick the next reachable but unassigned package, in sorted order.
		next := ""
		for _, name := range names {
			if reachable[name] > 0 {
				if _, done := assign[name]; !done {
					next = name
					break
				}
			}
		}
		if next == "" {
			// Every reachable package is assigned: the closure is complete.
			if sol, ok := p.buildSolution(assign); ok && (best == nil || less(sol, best)) {
				best = sol
			}
			return
		}
		for _, v := range cands[next] {
			if p.isBanned(next, v) {
				continue
			}
			ok := true
			for _, r := range imposed[next] {
				if !r.Allows(v) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			assign[next] = v
			reqs := p.requirements(next, v)
			for _, r := range reqs {
				imposed[r.Pkg] = append(imposed[r.Pkg], r)
				reachable[r.Pkg]++
			}
			step := changes
			if lv, locked := p.Locked[next]; locked && lv != v {
				step++
			}
			if viable() {
				rec(step)
			}
			for _, r := range reqs {
				l := imposed[r.Pkg]
				if len(l) == 1 {
					delete(imposed, r.Pkg)
				} else {
					imposed[r.Pkg] = l[:len(l)-1]
				}
				reachable[r.Pkg]--
			}
			delete(assign, next)
		}
	}
	rec(0)
	return best
}
