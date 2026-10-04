package main

import (
	"math/bits"
	"sort"
	"time"
)

// Solution is a set of chosen stabbing points.
type Solution struct {
	Points []int64 // ascending
	Cost   int64
}

// lessSol implements the objective order: min total cost, then min point
// count, then lexicographically smallest ascending point list.
// Both point lists must be sorted ascending.
func lessSol(a []int64, aCost int64, b []int64, bCost int64) bool {
	if aCost != bCost {
		return aCost < bCost
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func sortedCopy(x []int64) []int64 {
	out := append([]int64(nil), x...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Solver is a branch-and-bound stabbing-point solver. Candidate points are
// shared globally: one chosen point stabs every interval containing it.
type Solver struct {
	p           *problem
	best        []int64
	bestCost    int64
	hasBest     bool
	nodes       int64
	nodeLimit   int64     // 0 = unlimited
	deadline    time.Time // zero = no time limit
	interrupted bool
	frames      []int64 // cost lower bounds of ancestor nodes with unexplored children
}

func (s *Solver) limitHit() bool {
	if s.nodeLimit > 0 && s.nodes >= s.nodeLimit {
		return true
	}
	if !s.deadline.IsZero() && s.nodes&255 == 0 && time.Now().After(s.deadline) {
		return true
	}
	return false
}

// search explores completions of the partial solution (chosen, cost), where
// covered marks already-stabbed intervals.
func (s *Solver) search(covered []uint64, chosen []int64, cost int64) {
	if s.interrupted {
		return
	}
	s.nodes++
	p := s.p

	// Scan uncovered intervals (in right-endpoint order): pick the branching
	// target (fewest candidates) and compute a lower bound from a maximal
	// set of pairwise disjoint uncovered intervals. Pairwise disjoint
	// intervals cannot share a stabbing point, so each needs its own new
	// point costing at least minCost[i].
	target := -1
	targetCands := int(^uint(0) >> 1)
	var sumMin int64
	var disjoint int
	var lastHi int64
	first := true
	for i := range p.intervals {
		if testBit(covered, i) {
			continue
		}
		iv := p.intervals[i]
		if first || iv.Lo > lastHi {
			sumMin += p.minCost[i]
			disjoint++
			lastHi = iv.Hi
			first = false
		}
		if c := len(p.candOf[i]); c < targetCands {
			targetCands = c
			target = i
		}
	}

	if target == -1 { // all intervals stabbed: feasible leaf
		pts := sortedCopy(chosen)
		if !s.hasBest || lessSol(pts, cost, s.best, s.bestCost) {
			s.best, s.bestCost, s.hasBest = pts, cost, true
		}
		return
	}

	costLB := cost + sumMin
	countLB := len(chosen) + disjoint
	// Prune only on a strict (cost, count) loss; equal-cost/equal-count
	// nodes may still hold a lexicographically smaller optimum.
	if s.hasBest && (costLB > s.bestCost || (costLB == s.bestCost && countLB > len(s.best))) {
		return
	}

	// This node's costLB is a valid lower bound for every child subtree, so
	// it covers all children left unexplored if the search is interrupted.
	s.frames = append(s.frames, costLB)
	if s.limitHit() {
		s.interrupted = true
		return // keep the frame: its subtree is entirely unexplored
	}
	for _, c := range p.candOf[target] {
		if s.interrupted {
			break
		}
		nc := make([]uint64, p.nWords)
		copy(nc, covered)
		orInto(nc, p.cover[c])
		s.search(nc, append(chosen, p.points[c]), cost+p.costs[c])
	}
	if !s.interrupted {
		s.frames = s.frames[:len(s.frames)-1]
	}
}

// lowerBound returns a valid lower bound on the optimal cost given the
// current search state: every unexplored solution sits under some recorded
// frame, and every explored solution costs at least the incumbent.
func (s *Solver) lowerBound() (int64, bool) {
	lb, ok := int64(0), false
	if s.hasBest {
		lb, ok = s.bestCost, true
	}
	for _, f := range s.frames {
		if !ok || f < lb {
			lb, ok = f, true
		}
	}
	return lb, ok
}

// greedy builds an initial feasible solution: repeatedly take the uncovered
// interval with the smallest right endpoint and stab it with the candidate
// that is cheapest, then covers the most still-uncovered intervals.
func greedy(p *problem) ([]int64, int64, bool) {
	if len(p.infeasible) > 0 {
		return nil, 0, false
	}
	covered := make([]uint64, p.nWords)
	var chosen []int64
	var cost int64
	for {
		target := -1
		for i := range p.intervals {
			if !testBit(covered, i) {
				target = i
				break
			}
		}
		if target == -1 {
			return chosen, cost, true
		}
		bestJ := -1
		var bestCost, bestCover, bestPt int64
		for _, j := range p.candOf[target] {
			var newly int64
			for w := range covered {
				newly += int64(bits.OnesCount64(p.cover[j][w] &^ covered[w]))
			}
			if bestJ == -1 ||
				p.costs[j] < bestCost ||
				(p.costs[j] == bestCost && newly > bestCover) ||
				(p.costs[j] == bestCost && newly == bestCover && p.points[j] < bestPt) {
				bestJ = j
				bestCost, bestCover, bestPt = p.costs[j], newly, p.points[j]
			}
		}
		chosen = append(chosen, p.points[bestJ])
		cost += p.costs[bestJ]
		orInto(covered, p.cover[bestJ])
	}
}
