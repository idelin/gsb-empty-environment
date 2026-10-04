package calib

// MaxCompatibleSubset finds a largest subset of the calibration constraints
// that is simultaneously feasible (with integer offsets). The returned
// dropped constraints are the incompatible calibration points: removing them
// restores feasibility, and no smaller removal does.
//
// The search is exact (iterative deepening on the number of removals) up to a
// feasibility-test budget; beyond that it falls back to a deterministic
// greedy removal so it always terminates.
func MaxCompatibleSubset(cs []Constraint, aBounds Interval) (kept, dropped []Constraint) {
	feasible := func(sub []Constraint) bool {
		return Solve(sub, aBounds).IntFeasible
	}
	if feasible(cs) {
		return cs, nil
	}

	const budget = int64(200000)
	tests := int64(0)
	limited := func(sub []Constraint) bool {
		tests++
		return feasible(sub)
	}

	for k := 1; k < len(cs); k++ {
		if combCount(len(cs), k) > budget-tests {
			break
		}
		if idx, ok := searchRemoval(cs, k, limited); ok {
			removed := make(map[int]bool, k)
			for _, i := range idx {
				removed[i] = true
			}
			kept := make([]Constraint, 0, len(cs)-k)
			dropped := make([]Constraint, 0, k)
			for i, c := range cs {
				if removed[i] {
					dropped = append(dropped, c)
				} else {
					kept = append(kept, c)
				}
			}
			return kept, dropped
		}
	}
	return greedyRemoval(cs, feasible)
}

// searchRemoval looks for a set of exactly k constraints whose removal makes
// the remainder feasible. It returns the removed indices.
func searchRemoval(cs []Constraint, k int, feasible func([]Constraint) bool) ([]int, bool) {
	n := len(cs)
	idx := make([]int, k)
	var walk func(start, depth int) ([]int, bool)
	walk = func(start, depth int) ([]int, bool) {
		if depth == k {
			removed := make(map[int]bool, k)
			for _, i := range idx {
				removed[i] = true
			}
			sub := make([]Constraint, 0, n-k)
			for i, c := range cs {
				if !removed[i] {
					sub = append(sub, c)
				}
			}
			if feasible(sub) {
				return append([]int(nil), idx...), true
			}
			return nil, false
		}
		for i := start; i <= n-(k-depth); i++ {
			idx[depth] = i
			if got, ok := walk(i+1, depth+1); ok {
				return got, true
			}
		}
		return nil, false
	}
	return walk(0, 0)
}

// greedyRemoval deterministically removes constraints one at a time, always
// preferring a removal that immediately restores feasibility; otherwise it
// removes the constraint involved in the most pairwise conflicts.
func greedyRemoval(cs []Constraint, feasible func([]Constraint) bool) (kept, dropped []Constraint) {
	rest := append([]Constraint(nil), cs...)
	for !feasible(rest) {
		best := -1
		for i := range rest {
			cand := append(append([]Constraint(nil), rest[:i]...), rest[i+1:]...)
			if feasible(cand) {
				best = i
				break
			}
		}
		if best < 0 {
			best = mostConflicting(rest)
		}
		dropped = append(dropped, rest[best])
		rest = append(rest[:best], rest[best+1:]...)
	}
	return rest, dropped
}

// mostConflicting returns the index of the constraint participating in the
// most pairwise-infeasible pairs (ties broken by lowest index).
func mostConflicting(cs []Constraint) int {
	score := make([]int, len(cs))
	for i := 0; i < len(cs); i++ {
		for j := i + 1; j < len(cs); j++ {
			if !Solve([]Constraint{cs[i], cs[j]}, Interval{}).Feasible {
				score[i]++
				score[j]++
			}
		}
	}
	best, bestScore := 0, -1
	for i, s := range score {
		if s > bestScore {
			best, bestScore = i, s
		}
	}
	return best
}

func combCount(n, k int) int64 {
	if k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	r := int64(1)
	for i := 0; i < k; i++ {
		r = r * int64(n-i) / int64(i+1)
		if r > 1<<62 {
			return 1 << 62
		}
	}
	return r
}
