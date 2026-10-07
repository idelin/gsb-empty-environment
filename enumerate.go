package main

import (
	"fmt"
	"math/big"
)

// EnumResult summarizes an exhaustive enumeration of all policies.
type EnumResult struct {
	// Total is the number of distinct policies, len(actions)^len(signals).
	Total int
	// FeasibleCount is the number of policies respecting every capacity.
	FeasibleCount int
	// Best is an optimal feasible policy, nil when none exists.
	Best Policy
	// BestWorst and BestAvg are its lexicographic objective values.
	BestWorst *big.Rat
	BestAvg   *big.Rat
}

// Enumerate exhaustively evaluates every signal -> action policy. Because a
// policy has exactly one action per signal, the enumeration only ever
// considers policies obeying the non-anticipativity rule, and it checks the
// shared capacity constraints on the summed probability masses. It serves
// as an independent, obviously-correct cross-check for Solve.
func (p *Problem) Enumerate() (*EnumResult, error) {
	n := p.NumSignals()
	res := &EnumResult{}
	counter := make(Policy, n)
	for {
		res.Total++
		ev, err := p.Evaluate(counter)
		if err != nil {
			return nil, err
		}
		if ev.Feasible {
			res.FeasibleCount++
			if res.Best == nil || betterLex(ev.Worst, ev.Avg, res.BestWorst, res.BestAvg) {
				res.Best = append(Policy(nil), counter...)
				res.BestWorst = new(big.Rat).Set(ev.Worst)
				res.BestAvg = new(big.Rat).Set(ev.Avg)
			}
		}
		// Increment the mixed-radix counter over actions^signals.
		i := 0
		for ; i < n; i++ {
			counter[i]++
			if counter[i] < len(p.Actions) {
				break
			}
			counter[i] = 0
		}
		if i == n {
			break
		}
	}
	return res, nil
}

// Verify solves the problem and cross-checks the answer against exhaustive
// enumeration, returning an error on any disagreement.
func (p *Problem) Verify() (*Result, *EnumResult, error) {
	got, err := p.Solve()
	if err != nil {
		return nil, nil, err
	}
	enum, err := p.Enumerate()
	if err != nil {
		return nil, nil, err
	}
	if got.Feasible != (enum.Best != nil) {
		return got, enum, fmt.Errorf("feasibility mismatch: solver=%v enumeration=%v", got.Feasible, enum.Best != nil)
	}
	if !got.Feasible {
		return got, enum, nil
	}
	if got.Worst.Cmp(enum.BestWorst) != 0 || got.Avg.Cmp(enum.BestAvg) != 0 {
		return got, enum, fmt.Errorf(
			"objective mismatch: solver=(worst %s, avg %s) enumeration=(worst %s, avg %s)",
			got.Worst.RatString(), got.Avg.RatString(),
			enum.BestWorst.RatString(), enum.BestAvg.RatString())
	}
	return got, enum, nil
}
