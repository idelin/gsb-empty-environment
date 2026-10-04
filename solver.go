package main

import (
	"math"
	"time"
)

// Result is the outcome of a Solve run.
type Result struct {
	Optimal bool  // true if the search completed (not interrupted by budget)
	Set     []int // best feedback vertex set found (sorted vertex IDs)
	Cost    int64 // total deletion cost of Set
	Lower   int64 // lower bound on the optimal cost
	Upper   int64 // upper bound on the optimal cost (== Cost when a set exists)
	Topo    []int // topological order of the remaining graph (certificate)
	Nodes   int64 // number of search nodes expanded
}

// Solver is an exact branch-and-bound feedback-vertex-set solver.
// Objective lexicographic priority:
//   1. minimize total deletion cost
//   2. minimize number of deleted vertices
//   3. minimize the sorted ID sequence lexicographically
type Solver struct {
	g *Graph

	alive []bool // vertices not deleted on the current search path

	best      []int
	bestCost  int64
	bestCount int64

	nodes    int64
	maxNodes int64 // 0 = unlimited
	deadline time.Time
	hasDl    bool
	aborted  bool

	globalLB int64
}

// NewSolver creates a solver. maxNodes<=0 means no node budget;
// timeBudgetMs<=0 means no time budget.
func NewSolver(g *Graph, maxNodes int64, timeBudgetMs int64) *Solver {
	s := &Solver{
		g:        g,
		alive:    make([]bool, g.N),
		maxNodes: maxNodes,
		globalLB: math.MaxInt64,
	}
	for i := range s.alive {
		s.alive[i] = true
	}
	if timeBudgetMs > 0 {
		s.deadline = time.Now().Add(time.Duration(timeBudgetMs) * time.Millisecond)
		s.hasDl = true
	}
	// Trivially feasible incumbent: delete every vertex.
	total := int64(0)
	for _, c := range g.Cost {
		total += c
	}
	s.bestCost = total
	s.bestCount = int64(g.N)
	s.best = make([]int, g.N)
	for i := range s.best {
		s.best[i] = i
	}
	return s
}

// Solve runs the search and returns the result.
func (s *Solver) Solve() Result {
	s.search(nil, 0)
	res := Result{
		Optimal: !s.aborted,
		Set:     append([]int(nil), s.best...),
		Cost:    s.bestCost,
		Upper:   s.bestCost,
		Nodes:   s.nodes,
	}
	if s.aborted {
		res.Lower = s.globalLB
		if res.Lower == math.MaxInt64 || res.Lower > s.bestCost {
			res.Lower = s.bestCost
		}
	} else {
		res.Lower = s.bestCost
	}
	alive := make([]bool, s.g.N)
	for v := 0; v < s.g.N; v++ {
		alive[v] = true
	}
	for _, v := range s.best {
		alive[v] = false
	}
	topo, ok := s.g.TopoOrder(alive)
	if ok {
		res.Topo = topo
	}
	return res
}

// better reports whether candidate (cost,count,set) strictly beats the
// incumbent under the lexicographic objective.
func (s *Solver) better(cost, count int64, set []int) bool {
	if cost != s.bestCost {
		return cost < s.bestCost
	}
	if count != s.bestCount {
		return count < s.bestCount
	}
	sorted := sortedCopy(set)
	for i := 0; i < len(sorted) && i < len(s.best); i++ {
		if sorted[i] != s.best[i] {
			return sorted[i] < s.best[i]
		}
	}
	return len(sorted) < len(s.best)
}

func (s *Solver) consider(deleted []int, cost int64) {
	if s.better(cost, int64(len(deleted)), deleted) {
		s.best = sortedCopy(deleted)
		s.bestCost = cost
		s.bestCount = int64(len(deleted))
	}
}

// packingLB computes a lower bound on the additional deletion cost (and
// count) needed to make the remaining graph acyclic, via a greedy packing
// of vertex-disjoint cycles: each cycle in the packing must be hit by a
// distinct deleted vertex costing at least the cycle's minimum cost.
func (s *Solver) packingLB(peeled []bool) (lbCost, lbCount int64) {
	avail := make([]bool, s.g.N)
	none := make([]bool, s.g.N)
	for v := 0; v < s.g.N; v++ {
		avail[v] = s.alive[v] && !peeled[v]
	}
	for {
		cyc := s.g.findCycle(avail, none)
		if cyc == nil {
			return lbCost, lbCount
		}
		minC := int64(math.MaxInt64)
		for _, v := range cyc {
			if s.g.Cost[v] < minC {
				minC = s.g.Cost[v]
			}
			avail[v] = false
		}
		lbCost += minC
		lbCount++
	}
}

// recordLB folds a subtree lower bound into the global lower bound.
func (s *Solver) recordLB(lb int64) {
	if lb < s.globalLB {
		s.globalLB = lb
	}
}

func (s *Solver) overBudget() bool {
	if s.maxNodes > 0 && s.nodes >= s.maxNodes {
		return true
	}
	if s.hasDl && time.Now().After(s.deadline) {
		return true
	}
	return false
}

// search explores the branch-and-bound tree. `deleted` lists the vertices
// removed so far (in deletion order); s.alive reflects those removals.
func (s *Solver) search(deleted []int, cost int64) {
	if s.aborted {
		return
	}
	s.nodes++

	peeled := s.g.peel(s.alive)
	cycle := s.g.findCycle(s.alive, peeled)
	if cycle == nil {
		// Remaining graph is acyclic: feasible candidate.
		s.consider(deleted, cost)
		return
	}

	lbCost, lbCount := s.packingLB(peeled)
	nodeLB := cost + lbCost

	// Bound pruning: only prune when this subtree is strictly dominated
	// on cost, or ties on cost and is strictly dominated on count.
	// Equal-cost equal-count subtrees must still be explored because they
	// may contain a lexicographically smaller ID set.
	if nodeLB > s.bestCost ||
		(nodeLB == s.bestCost && int64(len(deleted))+lbCount > s.bestCount) {
		return
	}

	if s.overBudget() {
		s.recordLB(nodeLB)
		s.aborted = true
		return
	}

	for _, v := range cycle {
		s.alive[v] = false
		s.search(append(deleted, v), cost+s.g.Cost[v])
		s.alive[v] = true
		if s.aborted {
			// Unexplored siblings are bounded by this node's bound.
			s.recordLB(nodeLB)
			return
		}
	}
}
