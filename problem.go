// Package bnb implements a branch-and-bound solver for the bounded
// integer knapsack problem:
//
//	maximize  sum_i values[i]  * x_i
//	subject to sum_i weights[i] * x_i <= capacity
//	           0 <= x_i <= bounds[i], x_i integer
//
// The solver explores nodes in priority order: highest relaxation upper
// bound first, ties broken by the node's path encoding. The full frontier
// plus the incumbent can be checkpointed and restored, and Solve accepts a
// node budget; when the budget is exhausted it returns valid lower and
// upper bounds on the optimum.
package bnb

import (
	"errors"
	"math/rand"
)

// Problem is a bounded integer knapsack instance.
type Problem struct {
	Values   []int64 `json:"values"`
	Weights  []int64 `json:"weights"`
	Bounds   []int64 `json:"bounds"`
	Capacity int64   `json:"capacity"`
}

// NumItems returns the number of decision variables.
func (p *Problem) NumItems() int { return len(p.Values) }

// Validate checks the instance for structural consistency.
func (p *Problem) Validate() error {
	n := len(p.Values)
	if len(p.Weights) != n || len(p.Bounds) != n {
		return errors.New("bnb: values, weights and bounds must have equal length")
	}
	if p.Capacity < 0 {
		return errors.New("bnb: capacity must be non-negative")
	}
	for i := 0; i < n; i++ {
		if p.Weights[i] < 0 {
			return errors.New("bnb: weights must be non-negative")
		}
		if p.Bounds[i] < 0 {
			return errors.New("bnb: bounds must be non-negative")
		}
	}
	return nil
}

// RandomProblem builds a reproducible random instance. Item weights are in
// [1,maxWeight], values in [0,maxValue], per-item bounds in [1,maxBound], and
// the capacity is capacityNum/capacityDen of the total bounded weight
// (clamped to [1, totalWeight]).
func RandomProblem(rng *rand.Rand, n int, maxWeight, maxValue, maxBound int64, capacityNum, capacityDen int64) *Problem {
	p := &Problem{
		Values:  make([]int64, n),
		Weights: make([]int64, n),
		Bounds:  make([]int64, n),
	}
	var total int64
	for i := 0; i < n; i++ {
		p.Weights[i] = 1 + rng.Int63n(maxWeight)
		p.Values[i] = rng.Int63n(maxValue + 1)
		p.Bounds[i] = 1 + rng.Int63n(maxBound)
		total += p.Weights[i] * p.Bounds[i]
	}
	cap := total * capacityNum / capacityDen
	if cap < 1 {
		cap = 1
	}
	if cap > total {
		cap = total
	}
	p.Capacity = cap
	return p
}
