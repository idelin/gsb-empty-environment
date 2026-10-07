package dectree

import (
	"math/big"
	"math/bits"
)

// exhaustiveFrontier independently enumerates every strategy tree for a
// subproblem, recursively and without memoization, and returns the Pareto
// frontier of (worst, expected) cost pairs. It exists to cross-check Solve:
// Verify compares a strategy against the optimum found here.
func exhaustiveFrontier(p *Problem, set uint16, budget int) []option {
	if bits.OnesCount16(set) <= 1 {
		return []option{{worst: 0, exp: new(big.Rat)}}
	}
	var frontier []option
	for ti := range p.Tests {
		t := &p.Tests[ti]
		if t.Cost > budget {
			continue
		}
		_, groups, ok := partition(p, set, ti)
		if !ok {
			continue
		}
		perGroup := make([][]option, len(groups))
		for i, g := range groups {
			perGroup[i] = exhaustiveFrontier(p, g, budget-t.Cost)
		}
		combined := combine(perGroup)
		weight := sumProb(p, set)
		fee := big.NewRat(int64(t.Cost), 1)
		for i := range combined {
			combined[i].worst += t.Cost
			combined[i].exp.Add(combined[i].exp, new(big.Rat).Mul(weight, fee))
		}
		frontier = append(frontier, combined...)
		frontier = prune(frontier)
	}
	if len(frontier) == 0 {
		// No affordable informative test: only the trivial "stop here"
		// strategy tree exists for this subproblem.
		frontier = []option{{worst: 0, exp: new(big.Rat)}}
	}
	return frontier
}

// exhaustiveOptimum returns the lexicographically smallest (worst, expected)
// cost over all strategy trees, found by brute-force enumeration.
func exhaustiveOptimum(p *Problem, set uint16, budget int) (worst int, exp *big.Rat) {
	frontier := exhaustiveFrontier(p, set, budget)
	best := frontier[0]
	for _, o := range frontier[1:] {
		if less(o, best) {
			best = o
		}
	}
	return best.worst, new(big.Rat).Quo(best.exp, sumProb(p, set))
}
