package updater

// BruteForce enumerates the full cartesian product of candidate versions
// over the whole package universe and returns the objective-optimal
// feasible solution. It is the reference verifier for Solve: because it
// inspects every combination, it cannot keep an old locked version when
// doing so would violate a requirement imposed by a new dependency, nor
// miss a strictly better objective.
func BruteForce(p *Problem) *Solution {
	names := p.universe()
	lists := make([][]Version, len(names))
	for i, name := range names {
		lists[i] = p.Candidates[name]
	}

	var best *Solution
	assign := make(map[string]Version, len(names))
	index := make([]int, len(names))
	for {
		for i, name := range names {
			if len(lists[i]) > 0 {
				assign[name] = lists[i][index[i]]
			} else {
				delete(assign, name)
			}
		}
		if sol, ok := p.buildSolution(assign); ok && (best == nil || less(sol, best)) {
			best = sol
		}
		// Odometer increment; packages without candidates are skipped.
		i := 0
		for ; i < len(names); i++ {
			if len(lists[i]) == 0 {
				continue
			}
			index[i]++
			if index[i] < len(lists[i]) {
				break
			}
			index[i] = 0
		}
		if i == len(names) {
			return best
		}
	}
}
