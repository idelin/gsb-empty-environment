// Package factor decomposes a full rational joint probability table over
// discrete variables into the finest partition of mutually independent
// blocks, using only exact rational arithmetic (sums and products, never
// division), so zero-probability cells are handled soundly.
package factor

import (
	"fmt"
	"math/big"
	"strings"
)

const (
	// MaxVars is the maximum number of discrete variables.
	MaxVars = 6
	// MaxDomain is the maximum domain size of each variable.
	MaxDomain = 4
)

// Table is a full joint probability table over discrete variables
// x_0 .. x_{n-1}. Cells are stored in row-major (mixed-radix) order with
// the last variable varying fastest. All values are exact rationals.
type Table struct {
	Dims  []int
	Cells []*big.Rat
}

// NewTable validates dims and cells and returns a joint probability table.
// Every cell must be a non-negative rational and the cells must sum to 1.
func NewTable(dims []int, cells []*big.Rat) (*Table, error) {
	if len(dims) < 1 || len(dims) > MaxVars {
		return nil, fmt.Errorf("factor: need 1..%d variables, got %d", MaxVars, len(dims))
	}
	size := 1
	for i, d := range dims {
		if d < 1 || d > MaxDomain {
			return nil, fmt.Errorf("factor: domain of x_%d must be 1..%d, got %d", i, MaxDomain, d)
		}
		size *= d
	}
	if len(cells) != size {
		return nil, fmt.Errorf("factor: need %d cells, got %d", size, len(cells))
	}
	sum := new(big.Rat)
	out := make([]*big.Rat, size)
	for i, c := range cells {
		if c == nil {
			return nil, fmt.Errorf("factor: cell %d is nil", i)
		}
		if c.Sign() < 0 {
			return nil, fmt.Errorf("factor: cell %d is negative: %s", i, c.RatString())
		}
		out[i] = new(big.Rat).Set(c)
		sum.Add(sum, c)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		return nil, fmt.Errorf("factor: cells sum to %s, not 1", sum.RatString())
	}
	return &Table{Dims: append([]int(nil), dims...), Cells: out}, nil
}

// NewTableFromStrings is NewTable with cells parsed from exact rational
// strings such as "1/8", "2", or "0.25".
func NewTableFromStrings(dims []int, probs []string) (*Table, error) {
	cells := make([]*big.Rat, len(probs))
	for i, s := range probs {
		r, ok := new(big.Rat).SetString(strings.TrimSpace(s))
		if !ok {
			return nil, fmt.Errorf("factor: cell %d: cannot parse %q as a rational", i, s)
		}
		cells[i] = r
	}
	return NewTable(dims, cells)
}

// NumVars returns the number of variables.
func (t *Table) NumVars() int { return len(t.Dims) }

// Size returns the total number of cells.
func (t *Table) Size() int { return len(t.Cells) }

func stridesOf(dims []int) []int {
	s := make([]int, len(dims))
	acc := 1
	for i := len(dims) - 1; i >= 0; i-- {
		s[i] = acc
		acc *= dims[i]
	}
	return s
}

// Index maps a full assignment to its flat cell index.
func (t *Table) Index(assign []int) int {
	if len(assign) != len(t.Dims) {
		panic("factor: assignment length mismatch")
	}
	s := stridesOf(t.Dims)
	idx := 0
	for i, v := range assign {
		if v < 0 || v >= t.Dims[i] {
			panic("factor: assignment value out of domain")
		}
		idx += v * s[i]
	}
	return idx
}

// Assignment maps a flat cell index back to a full assignment.
func (t *Table) Assignment(flat int) []int {
	s := stridesOf(t.Dims)
	assign := make([]int, len(t.Dims))
	for i := range t.Dims {
		assign[i] = flat / s[i]
		flat %= s[i]
	}
	return assign
}

// At returns the probability of a full assignment.
func (t *Table) At(assign []int) *big.Rat { return t.Cells[t.Index(assign)] }

// Marginal sums out all variables not in keep (which must be sorted
// ascending) and returns the exact marginal table over keep.
// Marginal with an empty keep returns the 1-cell table holding 1.
// Only additions are performed; zero cells are never special-cased.
func (t *Table) Marginal(keep []int) *Table {
	dims := make([]int, len(keep))
	for i, v := range keep {
		dims[i] = t.Dims[v]
	}
	size := 1
	for _, d := range dims {
		size *= d
	}
	cells := make([]*big.Rat, size)
	for i := range cells {
		cells[i] = new(big.Rat)
	}
	s := stridesOf(dims)
	for flat, p := range t.Cells {
		assign := t.Assignment(flat)
		idx := 0
		for i, v := range keep {
			idx += assign[v] * s[i]
		}
		cells[idx].Add(cells[idx], p)
	}
	return &Table{Dims: dims, Cells: cells}
}

// Equal reports whether two tables have identical dims and cells.
func (t *Table) Equal(o *Table) bool {
	if len(t.Dims) != len(o.Dims) || len(t.Cells) != len(o.Cells) {
		return false
	}
	for i := range t.Dims {
		if t.Dims[i] != o.Dims[i] {
			return false
		}
	}
	for i := range t.Cells {
		if t.Cells[i].Cmp(o.Cells[i]) != 0 {
			return false
		}
	}
	return true
}

// project extracts the sub-assignment for the variables in block
// (sorted ascending) from a full assignment.
func project(assign, block []int) []int {
	sub := make([]int, len(block))
	for i, v := range block {
		sub[i] = assign[v]
	}
	return sub
}
