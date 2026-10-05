package hgsolve

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// MaxBruteForceEdges bounds the exhaustive verifier; 2^n subsets are enumerated.
const MaxBruteForceEdges = 24

// BruteForce finds the exact minimum proof cost for target by enumerating
// every subset of edges. A subset is a valid proof iff forward chaining from
// facts using only those edges derives target — this automatically rejects
// cyclic self-proofs, because a cycle with no factual grounding never fires.
// The subset's cost is the sum of its distinct edge costs, i.e. shared proof
// nodes are charged exactly once. It is exponential and meant to cross-check
// SolveExact on small hypergraphs.
func (g *Graph) BruteForce(facts []string, target string) (cost float64, edgeIDs []string, ok bool) {
	n := len(g.Edges)
	if n > MaxBruteForceEdges {
		return 0, nil, false
	}
	ti, tok := g.index[target]
	start := g.factSetOf(facts)
	if tok && start.has(ti) {
		return 0, nil, true
	}
	if !tok {
		return 0, nil, false
	}
	best := math.Inf(1)
	var bestIDs []string
	total := uint64(1) << uint(n)
	for mask := uint64(1); mask < total; mask++ {
		c := 0.0
		for i := 0; i < n; i++ {
			if mask>>uint(i)&1 == 1 {
				c += g.Edges[i].Cost
			}
		}
		if c >= best {
			continue
		}
		derived := g.closure(start, mask)
		if !derived[ti] {
			continue
		}
		best = c
		bestIDs = bestIDs[:0]
		for i := 0; i < n; i++ {
			if mask>>uint(i)&1 == 1 {
				bestIDs = append(bestIDs, g.Edges[i].ID)
			}
		}
	}
	if math.IsInf(best, 1) {
		return 0, nil, false
	}
	sort.Strings(bestIDs)
	return best, bestIDs, true
}

// closure returns the set of nodes derivable from start using only edges
// whose bit is set in mask.
func (g *Graph) closure(start factSet, mask uint64) []bool {
	derived := make([]bool, len(g.names))
	for _, x := range start.vals {
		derived[x] = true
	}
	for changed := true; changed; {
		changed = false
		for i := range g.Edges {
			if mask>>uint(i)&1 == 0 || derived[g.tgt[i]] {
				continue
			}
			fireable := true
			for _, p := range g.prem[i] {
				if !derived[p] {
					fireable = false
					break
				}
			}
			if fireable {
				derived[g.tgt[i]] = true
				changed = true
			}
		}
	}
	return derived
}

// VerifyProof checks that edgeIDs form a well-founded proof of target from
// facts and that their distinct costs sum to claimedCost. It rejects cyclic
// self-proofs: an edge set that only supports itself never becomes fireable
// from the facts.
func (g *Graph) VerifyProof(facts []string, target string, edgeIDs []string, claimedCost float64) error {
	byID := map[string][]int{}
	for i, e := range g.Edges {
		byID[e.ID] = append(byID[e.ID], i)
	}
	seen := map[string]bool{}
	mask := uint64(0)
	cost := 0.0
	for _, id := range edgeIDs {
		if seen[id] {
			return fmt.Errorf("edge %q listed twice", id)
		}
		seen[id] = true
		idxs, ok := byID[id]
		if !ok {
			return fmt.Errorf("unknown edge %q", id)
		}
		if len(idxs) > 1 {
			return fmt.Errorf("ambiguous edge ID %q", id)
		}
		mask |= 1 << uint(idxs[0])
		cost += g.Edges[idxs[0]].Cost
	}
	if math.Abs(cost-claimedCost) > 1e-9 {
		return fmt.Errorf("cost mismatch: edges sum to %v, claimed %v", cost, claimedCost)
	}
	ti, ok := g.index[target]
	if !ok {
		return errors.New("unknown target")
	}
	if g.factSetOf(facts).has(ti) {
		if len(edgeIDs) != 0 {
			return errors.New("target is a fact; proof must be empty")
		}
		return nil
	}
	if !g.closure(g.factSetOf(facts), mask)[ti] {
		return errors.New("edges do not derive target from facts (not well-founded)")
	}
	return nil
}
