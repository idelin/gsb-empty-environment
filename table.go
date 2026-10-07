// Package decomp finds the finest partition of discrete random variables
// into mutually independent blocks, given a full rational joint
// probability table. All arithmetic is exact (math/big.Rat); the
// independence test is the product-of-marginals identity checked on
// every cell, so zero-probability cells are handled directly and never
// "divided away" via conditional probabilities.
package decomp

import (
	"fmt"
	"math/big"
)

// MaxVars and MaxDomain bound the supported problem size.
const (
	MaxVars   = 6
	MaxDomain = 4
)

// Table is a full joint probability table over discrete variables with
// exact rational entries. Probs is laid out row-major with the last
// variable varying fastest: flat index = sum(tuple[i] * stride[i]).
type Table struct {
	Dims  []int      // domain size of each variable, each in [1, MaxDomain]
	Probs []*big.Rat // len == product(Dims)
}

// NewTable validates dimensions and copies the probabilities.
func NewTable(dims []int, probs []*big.Rat) (*Table, error) {
	if len(dims) == 0 || len(dims) > MaxVars {
		return nil, fmt.Errorf("need 1..%d variables, got %d", MaxVars, len(dims))
	}
	size := 1
	for _, d := range dims {
		if d < 1 || d > MaxDomain {
			return nil, fmt.Errorf("domain size %d out of range [1,%d]", d, MaxDomain)
		}
		size *= d
	}
	if len(probs) != size {
		return nil, fmt.Errorf("need %d probabilities for dims %v, got %d", size, dims, len(probs))
	}
	cp := make([]*big.Rat, size)
	for i, p := range probs {
		if p == nil {
			return nil, fmt.Errorf("nil probability at flat index %d", i)
		}
		cp[i] = new(big.Rat).Set(p)
	}
	return &Table{Dims: append([]int(nil), dims...), Probs: cp}, nil
}

// NumVars returns the number of variables.
func (t *Table) NumVars() int { return len(t.Dims) }

// Size returns the number of cells in the table.
func (t *Table) Size() int { return len(t.Probs) }

// tuple decodes a flat index into per-variable values (last variable fastest).
func (t *Table) tuple(idx int) []int {
	tup := make([]int, len(t.Dims))
	for i := len(t.Dims) - 1; i >= 0; i-- {
		tup[i] = idx % t.Dims[i]
		idx /= t.Dims[i]
	}
	return tup
}

// Validate checks that all entries are nonnegative and sum to exactly 1.
func (t *Table) Validate() error {
	sum := new(big.Rat)
	for i, p := range t.Probs {
		if p.Sign() < 0 {
			return fmt.Errorf("negative probability at flat index %d", i)
		}
		sum.Add(sum, p)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		return fmt.Errorf("probabilities sum to %s, not 1", sum.RatString())
	}
	return nil
}

// Marginal returns the exact marginal distribution over the given
// variables. block must be nonempty, sorted ascending, in range.
func (t *Table) Marginal(block []int) *Table {
	dims := make([]int, len(block))
	for i, v := range block {
		dims[i] = t.Dims[v]
	}
	size := 1
	for _, d := range dims {
		size *= d
	}
	probs := make([]*big.Rat, size)
	for i := range probs {
		probs[i] = new(big.Rat)
	}
	for flat, p := range t.Probs {
		if p.Sign() == 0 {
			continue
		}
		tup := t.tuple(flat)
		sub, acc := 0, 1
		for i := len(block) - 1; i >= 0; i-- {
			sub += tup[block[i]] * acc
			acc *= dims[i]
		}
		probs[sub].Add(probs[sub], p)
	}
	return &Table{Dims: dims, Probs: probs}
}
