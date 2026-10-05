package main

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Chain is a finite Markov chain with exact rational transition probabilities.
type Chain struct {
	N         int
	Absorbing []bool
	P         [][]*big.Rat
}

func ratZero() *big.Rat { return new(big.Rat) }

func ratOne() *big.Rat { return big.NewRat(1, 1) }

// NewChain allocates a zero-filled chain. Declared absorbing states are
// given an implicit self-loop of probability 1.
func NewChain(n int, absorbing []bool) *Chain {
	P := make([][]*big.Rat, n)
	for i := range P {
		P[i] = make([]*big.Rat, n)
		for j := range P[i] {
			P[i][j] = ratZero()
		}
	}
	c := &Chain{N: n, Absorbing: absorbing, P: P}
	for i := 0; i < n; i++ {
		if absorbing[i] {
			c.P[i][i] = ratOne()
		}
	}
	return c
}

type inputJSON struct {
	States      int   `json:"states"`
	Absorbing   []int `json:"absorbing"`
	Transitions []struct {
		From int    `json:"from"`
		To   int    `json:"to"`
		Prob string `json:"prob"`
	} `json:"transitions"`
}

// ParseChain builds a Chain from the JSON description, validating that every
// row of the transition matrix sums to exactly 1.
func ParseChain(data []byte) (*Chain, error) {
	var in inputJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	if in.States <= 0 {
		return nil, fmt.Errorf("states must be positive, got %d", in.States)
	}
	abs := make([]bool, in.States)
	for _, s := range in.Absorbing {
		if s < 0 || s >= in.States {
			return nil, fmt.Errorf("absorbing state %d out of range [0,%d)", s, in.States)
		}
		abs[s] = true
	}
	c := NewChain(in.States, abs)
	seen := make(map[[2]int]bool)
	for _, tr := range in.Transitions {
		if tr.From < 0 || tr.From >= in.States || tr.To < 0 || tr.To >= in.States {
			return nil, fmt.Errorf("transition %d->%d out of range", tr.From, tr.To)
		}
		if abs[tr.From] {
			return nil, fmt.Errorf("absorbing state %d must not declare outgoing transitions", tr.From)
		}
		p, ok := new(big.Rat).SetString(tr.Prob)
		if !ok {
			return nil, fmt.Errorf("invalid rational probability %q", tr.Prob)
		}
		if p.Sign() < 0 || p.Cmp(ratOne()) > 0 {
			return nil, fmt.Errorf("probability %q outside [0,1]", tr.Prob)
		}
		key := [2]int{tr.From, tr.To}
		if seen[key] {
			return nil, fmt.Errorf("duplicate transition %d->%d", tr.From, tr.To)
		}
		seen[key] = true
		c.P[tr.From][tr.To] = p
	}
	for i := 0; i < c.N; i++ {
		if abs[i] {
			continue
		}
		sum := ratZero()
		for j := 0; j < c.N; j++ {
			sum.Add(sum, c.P[i][j])
		}
		if sum.Cmp(ratOne()) != 0 {
			return nil, fmt.Errorf("row %d probabilities sum to %s, not 1", i, sum.RatString())
		}
	}
	return c, nil
}
