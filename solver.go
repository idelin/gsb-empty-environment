package main

import (
	"fmt"
	"math/big"
	"sort"
)

// Result is the outcome of solving a problem.
type Result struct {
	// Feasible is false when no policy satisfies the capacity constraints.
	Feasible bool
	// Policy is an optimal feasible policy (signal index -> action index).
	Policy Policy
	// Worst and Avg are the optimal lexicographic objective values.
	Worst *big.Rat
	Avg   *big.Rat
	// Usage[a] is the probability mass the optimal policy puts on action a.
	Usage []*big.Rat
}

// Solve finds a policy minimizing (worst-case loss, average loss)
// lexicographically, subject to the shared action capacities, using exact
// rational arithmetic. It runs a branch-and-bound search over signal
// assignments; Enumerate can be used to cross-check the result.
func (p *Problem) Solve() (*Result, error) {
	n := p.NumSignals()
	if n == 0 {
		return nil, fmt.Errorf("problem has no signals")
	}

	// Order signals by descending probability mass for stronger pruning.
	order := make([]int, n)
	for s := range order {
		order[s] = s
	}
	mass := make([]*big.Rat, n)
	for s := range order {
		mass[s] = new(big.Rat)
		for _, w := range p.worldsBySignal[s] {
			mass[s].Add(mass[s], p.Worlds[w].Prob)
		}
	}
	sort.Slice(order, func(i, j int) bool { return mass[order[i]].Cmp(mass[order[j]]) > 0 })

	// Suffix lower bounds over the ordered signals, used for pruning:
	// avgLB[k]  = sum of p_w * min_a loss[w][a] over signals order[k:],
	// worstLB[k] = max of min_a loss[w][a] over signals order[k:].
	avgLB := make([]*big.Rat, n+1)
	worstLB := make([]*big.Rat, n+1)
	avgLB[n] = new(big.Rat)
	worstLB[n] = nil
	for k := n - 1; k >= 0; k-- {
		avgLB[k] = new(big.Rat).Set(avgLB[k+1])
		worstLB[k] = worstLB[k+1]
		for _, w := range p.worldsBySignal[order[k]] {
			minLoss := minActionLoss(p.Loss[w])
			avgLB[k].Add(avgLB[k], new(big.Rat).Mul(p.Worlds[w].Prob, minLoss))
			if worstLB[k] == nil || minLoss.Cmp(worstLB[k]) > 0 {
				worstLB[k] = minLoss
			}
		}
	}

	s := &search{
		p:       p,
		order:   order,
		avgLB:   avgLB,
		worstLB: worstLB,
		assign:  make([]int, n),
		usage:   make([]*big.Rat, len(p.Actions)),
		avg:     new(big.Rat),
	}
	for a := range s.usage {
		s.usage[a] = new(big.Rat)
	}
	for i := range s.assign {
		s.assign[i] = -1
	}
	s.walk(0)

	if s.best == nil {
		return &Result{Feasible: false}, nil
	}
	// Map the best assignment back to the original signal order.
	pol := make(Policy, n)
	for k, sig := range order {
		pol[sig] = s.best[k]
	}
	ev, err := p.Evaluate(pol)
	if err != nil {
		return nil, err
	}
	if !ev.Feasible {
		return nil, fmt.Errorf("internal error: solver returned infeasible policy")
	}
	return &Result{Feasible: true, Policy: pol, Worst: ev.Worst, Avg: ev.Avg, Usage: ev.Usage}, nil
}

func minActionLoss(row []*big.Rat) *big.Rat {
	m := row[0]
	for _, l := range row[1:] {
		if l.Cmp(m) < 0 {
			m = l
		}
	}
	return m
}

type search struct {
	p       *Problem
	order   []int
	avgLB   []*big.Rat
	worstLB []*big.Rat

	assign []int      // assign[k] = action for k-th signal in search order
	usage  []*big.Rat // probability mass per action so far
	worst  *big.Rat   // worst loss over assigned worlds (nil when none)
	avg    *big.Rat   // expected loss over assigned worlds
	best   []int      // best complete assignment (search order)
	bestW  *big.Rat
	bestA  *big.Rat
}

// lexGeq reports whether (w1, a1) >= (w2, a2) in the lexicographic order,
// treating a nil worst as -infinity.
func lexGeq(w1, a1, w2, a2 *big.Rat) bool {
	c := cmpRatNil(w1, w2)
	if c != 0 {
		return c > 0
	}
	return a1.Cmp(a2) >= 0
}

func cmpRatNil(a, b *big.Rat) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	default:
		return a.Cmp(b)
	}
}

func maxRat(a, b *big.Rat) *big.Rat {
	if cmpRatNil(a, b) >= 0 {
		return a
	}
	return b
}

func (s *search) walk(k int) {
	p := s.p
	if s.best != nil {
		// Lexicographic lower bound for any completion of this node.
		wLB := maxRat(s.worst, s.worstLB[k])
		aLB := new(big.Rat).Add(s.avg, s.avgLB[k])
		if lexGeq(wLB, aLB, s.bestW, s.bestA) {
			return
		}
	}
	if k == len(s.order) {
		s.best = append([]int(nil), s.assign...)
		s.bestW = copyRat(s.worst)
		s.bestA = new(big.Rat).Set(s.avg)
		return
	}
	sig := s.order[k]
	worlds := p.worldsBySignal[sig]
	for a := range p.Actions {
		// Shared capacity check: adding this signal's mass must not
		// exceed the action's capacity.
		if p.Capacity[a] != nil {
			sum := new(big.Rat).Set(s.usage[a])
			for _, w := range worlds {
				sum.Add(sum, p.Worlds[w].Prob)
			}
			if sum.Cmp(p.Capacity[a]) > 0 {
				continue
			}
		}
		// Apply the assignment.
		var newWorst *big.Rat
		addAvg := new(big.Rat)
		for _, w := range worlds {
			s.usage[a].Add(s.usage[a], p.Worlds[w].Prob)
			loss := p.Loss[w][a]
			if newWorst == nil || loss.Cmp(newWorst) > 0 {
				newWorst = loss
			}
			addAvg.Add(addAvg, new(big.Rat).Mul(p.Worlds[w].Prob, loss))
		}
		savedWorst := s.worst
		s.worst = maxRat(s.worst, newWorst)
		s.avg.Add(s.avg, addAvg)
		s.assign[k] = a

		s.walk(k + 1)

		s.assign[k] = -1
		s.avg.Sub(s.avg, addAvg)
		s.worst = savedWorst
		for _, w := range worlds {
			s.usage[a].Sub(s.usage[a], p.Worlds[w].Prob)
		}
	}
}

func copyRat(r *big.Rat) *big.Rat {
	if r == nil {
		return nil
	}
	return new(big.Rat).Set(r)
}
