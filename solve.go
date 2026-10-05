package main

import (
	"math/big"
)

// Statuses returned by Solve.
const (
	StatusOptimal    = "optimal"    // search completed, best solution is provably optimal
	StatusFeasible   = "feasible"   // budget exhausted; best known solution, not proven optimal
	StatusUnknown    = "unknown"    // budget exhausted before any feasible solution was found
	StatusInfeasible = "infeasible" // search completed; no feasible solution exists
)

// solver is an exact branch-and-bound search over per-shard path choices.
// Traversal order is fixed (shards in input order, paths in enumeration
// order), so the same input and budget always produce the same result.
// The budget counts expanded search nodes; one node is one (level,
// selected-set) state.
type solver struct {
	m      *model
	budget int64 // <0 means unlimited

	expansions int64
	bestCost   *big.Int
	bestSet    bitset
	bestIDs    []string
	truncated  bool
	frontierLB *big.Int // min lower bound over nodes cut off by the budget
}

// Solve finds a minimum-cost dependency-closed file set that restores every
// shard to its target. Costs and bounds are arbitrary-precision integers.
func Solve(m *model, diags []Diagnostic, budget int64) *Solution {
	sol := &Solution{Expansions: 0, Diagnostics: diags}

	for i := range m.shards {
		if len(m.paths[i]) == 0 {
			sol.Status = StatusInfeasible
			return sol
		}
	}

	s := &solver{m: m, budget: budget}
	s.search(0, newBitset(len(m.ids)), new(big.Int))
	sol.Expansions = s.expansions

	switch {
	case s.truncated && s.bestCost == nil:
		sol.Status = StatusUnknown
		sol.LowerBound = s.frontierLB.String()
	case s.truncated:
		sol.Status = StatusFeasible
		sol.Files = s.bestIDs
		sol.Cost = s.bestCost.String()
		// Valid lower bound: every explored leaf costs at least bestCost
		// and every unexplored leaf sits under a cut node, so the
		// optimum is at least min(bestCost, frontierLB).
		lb := s.frontierLB
		if s.bestCost.Cmp(lb) < 0 {
			lb = s.bestCost
		}
		sol.LowerBound = lb.String()
	case s.bestCost == nil:
		sol.Status = StatusInfeasible
	default:
		sol.Status = StatusOptimal
		sol.Files = s.bestIDs
		sol.Cost = s.bestCost.String()
		sol.LowerBound = s.bestCost.String()
	}
	return sol
}

func (s *solver) search(level int, sel bitset, cost *big.Int) {
	if s.budget >= 0 && s.expansions >= s.budget {
		// Budget exhausted: record this node's lower bound. Any not yet
		// explored optimum must pass through a cut node, so the minimum of
		// these bounds is a valid global lower bound.
		s.truncated = true
		s.recordFrontier(s.lowerBound(level, sel, cost))
		return
	}
	s.expansions++

	if level == len(s.m.shards) {
		s.consider(sel, cost)
		return
	}

	if s.bestCost != nil && s.lowerBound(level, sel, cost).Cmp(s.bestCost) > 0 {
		return // cannot beat the incumbent; equal-cost nodes are kept for the tie-break
	}

	for _, p := range s.m.paths[level] {
		nsel := sel.clone().or(p.set)
		ncost := s.m.costOf(nsel)
		if s.bestCost != nil && ncost.Cmp(s.bestCost) > 0 {
			continue
		}
		s.search(level+1, nsel, ncost)
	}
}

// lowerBound returns a valid lower bound on any solution reachable from this
// node: the current cost plus, for the remaining shards, the largest of their
// individual minimum marginal costs. The final set must contain each
// remaining shard's path, so it must pay at least each shard's minimum
// marginal cost, hence at least their maximum.
func (s *solver) lowerBound(level int, sel bitset, cost *big.Int) *big.Int {
	lb := new(big.Int).Set(cost)
	maxMarg := new(big.Int)
	for i := level; i < len(s.m.shards); i++ {
		var minMarg *big.Int
		for _, p := range s.m.paths[i] {
			marg := s.m.marginalCost(sel, p.set)
			if minMarg == nil || marg.Cmp(minMarg) < 0 {
				minMarg = marg
			}
		}
		if minMarg != nil && minMarg.Cmp(maxMarg) > 0 {
			maxMarg = minMarg
		}
	}
	return lb.Add(lb, maxMarg)
}

func (s *solver) recordFrontier(lb *big.Int) {
	if s.frontierLB == nil || lb.Cmp(s.frontierLB) < 0 {
		s.frontierLB = lb
	}
}

func (s *solver) consider(sel bitset, cost *big.Int) {
	switch {
	case s.bestCost == nil || cost.Cmp(s.bestCost) < 0:
		s.setBest(sel, cost)
	case cost.Cmp(s.bestCost) == 0:
		if ids := s.m.idsOf(sel); lessIDs(ids, s.bestIDs) {
			s.setBest(sel, cost)
		}
	}
}

func (s *solver) setBest(sel bitset, cost *big.Int) {
	s.bestCost = new(big.Int).Set(cost)
	s.bestSet = sel.clone()
	s.bestIDs = s.m.idsOf(sel)
}

// lessIDs compares two sorted file-ID lists lexicographically.
func lessIDs(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
