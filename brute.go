package main

import "fmt"

// BruteForce enumerates all 2^n vertex subsets and returns the optimum under
// the same lexicographic objective as Solve: minimum cost, then minimum
// count, then smallest sorted ID sequence. It is the ground truth used to
// cross-check the branch-and-bound solver on small graphs.
func BruteForce(g *Graph) (*Solution, error) {
	n := len(g.IDs)
	if n > 25 {
		return nil, fmt.Errorf("brute force limited to 25 vertices, got %d", n)
	}
	var best *Solution
	for mask := 0; mask < (1 << n); mask++ {
		deleted := make([]bool, n)
		var cost int64
		count := 0
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				deleted[i] = true
				cost += g.Cost[i]
				count++
			}
		}
		if best != nil && (cost > best.Cost || (cost == best.Cost && count > best.Count)) {
			continue
		}
		if !g.AcyclicExcluding(deleted) {
			continue
		}
		sol := &Solution{Deleted: deleted, Cost: cost, Count: count}
		if best == nil || solutionLess(sol, best, g) {
			best = sol
		}
	}
	return best, nil
}
