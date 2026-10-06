// Package markov analyzes finite Markov chains with exact rational
// transition probabilities. Given a target absorbing set A it computes,
// for every start state i, the exact probability h_i of ever reaching A
// and the conditional expected number of steps E_i[T | T < infinity],
// where T is the hitting time of A.
//
// The two degenerate situations are reported distinctly:
//
//   - h_i == 0: A is never reached from i; the conditional expectation
//     is undefined and reported as nil.
//   - 0 < h_i < 1: A may never be reached, so the unconditional
//     expectation E_i[T] is infinite, while the conditional expectation
//     (which is always finite in a finite chain) is still reported.
package markov

import (
	"fmt"
	"math/big"
)

// Chain is a finite Markov chain with exact rational transition
// probabilities. P[i][j] is the probability of moving from state i to
// state j in one step.
type Chain struct {
	N int
	P [][]*big.Rat
}

// NewChain validates that p is a square row-stochastic matrix with
// entries in [0,1] and every row summing to exactly 1, and returns a
// Chain holding a defensive copy of p.
func NewChain(p [][]*big.Rat) (*Chain, error) {
	n := len(p)
	if n == 0 {
		return nil, fmt.Errorf("chain must have at least one state")
	}
	one := big.NewRat(1, 1)
	for i, row := range p {
		if len(row) != n {
			return nil, fmt.Errorf("row %d: expected %d entries, got %d", i, n, len(row))
		}
		sum := new(big.Rat)
		for j, v := range row {
			if v == nil {
				return nil, fmt.Errorf("entry (%d,%d): nil probability", i, j)
			}
			if v.Sign() < 0 || v.Cmp(one) > 0 {
				return nil, fmt.Errorf("entry (%d,%d): probability %s outside [0,1]", i, j, v.RatString())
			}
			sum.Add(sum, v)
		}
		if sum.Cmp(one) != 0 {
			return nil, fmt.Errorf("row %d: probabilities sum to %s, want 1", i, sum.RatString())
		}
	}
	cp := make([][]*big.Rat, n)
	for i := range p {
		cp[i] = make([]*big.Rat, n)
		for j := range p[i] {
			cp[i][j] = new(big.Rat).Set(p[i][j])
		}
	}
	return &Chain{N: n, P: cp}, nil
}

// ParseChain builds a Chain from decimal or fraction strings such as
// "1/2", "0.25" or "1".
func ParseChain(rows [][]string) (*Chain, error) {
	p := make([][]*big.Rat, len(rows))
	for i, row := range rows {
		p[i] = make([]*big.Rat, len(row))
		for j, s := range row {
			r, ok := new(big.Rat).SetString(s)
			if !ok {
				return nil, fmt.Errorf("entry (%d,%d): cannot parse %q as a rational", i, j, s)
			}
			p[i][j] = r
		}
	}
	return NewChain(p)
}
