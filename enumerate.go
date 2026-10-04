package main

import "math"

// maxEnumEdges caps brute-force subset enumeration at 2^maxEnumEdges.
const maxEnumEdges = 24

// enumMinCost independently recomputes the optimum by enumerating every
// subset of the (augmented) edges. A subset proves target iff target lies
// in its forward closure; the closure construction itself guarantees the
// derivation is acyclic, so cyclic self-proofs are rejected automatically.
// The subset cost sums each chosen edge once, so shared premises are
// charged once. Returns ok=false when the edge count is too large.
func enumMinCost(edges []Hyperedge, target string) (best float64, bestSet []int, ok bool) {
	n := len(edges)
	if n > maxEnumEdges {
		return 0, nil, false
	}
	best = math.Inf(1)
	found := false
	for mask := uint64(0); mask < (uint64(1) << uint(n)); mask++ {
		cost := 0.0
		var subset []Hyperedge
		var idxs []int
		for i := 0; i < n; i++ {
			if mask&(uint64(1)<<uint(i)) != 0 {
				cost += edges[i].Cost
				subset = append(subset, edges[i])
				idxs = append(idxs, i)
			}
		}
		if cost >= best {
			continue
		}
		if closure(subset)[target] {
			best = cost
			bestSet = idxs
			found = true
		}
	}
	if !found {
		return math.Inf(1), nil, true
	}
	return best, bestSet, true
}
