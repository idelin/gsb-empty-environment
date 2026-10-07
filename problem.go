package psdcomp

import (
	"fmt"
	"math/big"
	"sort"
)

// FixedEntry pins matrix position (I,J) — and by symmetry (J,I) — to a
// rational value.
type FixedEntry struct {
	I, J  int
	Value *big.Rat
}

// Variable is an undetermined upper-triangular position (I <= J) whose
// value must be chosen from Candidates.
type Variable struct {
	I, J       int
	Candidates []*big.Rat
}

// Problem describes a PSD matrix completion instance.
type Problem struct {
	N     int
	Fixed []FixedEntry
	Vars  []Variable
}

// posKey packs an upper-triangular position into a unique int.
func posKey(i, j int) int {
	if i > j {
		i, j = j, i
	}
	return i*MaxN + j
}

// Validate checks structural well-formedness: order within [1, MaxN],
// positions in range and unique, no overlap between fixed and variable
// entries, at least one candidate per variable, and the total number of
// candidate combinations within MaxCombos.
func (p *Problem) Validate() error {
	if p.N < 1 || p.N > MaxN {
		return fmt.Errorf("psdcomp: matrix order %d out of range [1,%d]", p.N, MaxN)
	}
	seen := map[int]bool{}
	for _, f := range p.Fixed {
		if f.Value == nil {
			return fmt.Errorf("psdcomp: fixed entry (%d,%d) has nil value", f.I, f.J)
		}
		if f.I < 0 || f.I >= p.N || f.J < 0 || f.J >= p.N {
			return fmt.Errorf("psdcomp: fixed entry (%d,%d) out of range for n=%d", f.I, f.J, p.N)
		}
		k := posKey(f.I, f.J)
		if seen[k] {
			return fmt.Errorf("psdcomp: position (%d,%d) specified more than once", f.I, f.J)
		}
		seen[k] = true
	}
	combos := big.NewInt(1)
	for v := range p.Vars {
		vv := &p.Vars[v]
		if vv.I < 0 || vv.I >= p.N || vv.J < 0 || vv.J >= p.N {
			return fmt.Errorf("psdcomp: variable (%d,%d) out of range for n=%d", vv.I, vv.J, p.N)
		}
		if vv.I > vv.J {
			return fmt.Errorf("psdcomp: variable (%d,%d) must be upper-triangular (I<=J)", vv.I, vv.J)
		}
		k := posKey(vv.I, vv.J)
		if seen[k] {
			return fmt.Errorf("psdcomp: position (%d,%d) specified more than once", vv.I, vv.J)
		}
		seen[k] = true
		if len(vv.Candidates) == 0 {
			return fmt.Errorf("psdcomp: variable (%d,%d) has no candidates", vv.I, vv.J)
		}
		for _, c := range vv.Candidates {
			if c == nil {
				return fmt.Errorf("psdcomp: variable (%d,%d) has nil candidate", vv.I, vv.J)
			}
		}
		combos.Mul(combos, big.NewInt(int64(len(vv.Candidates))))
	}
	if combos.Cmp(big.NewInt(MaxCombos)) > 0 {
		return fmt.Errorf("psdcomp: %s candidate combinations exceed the limit %d", combos.String(), MaxCombos)
	}
	return nil
}

// TotalCombos returns the number of candidate combinations (assumes a
// validated problem).
func (p *Problem) TotalCombos() uint64 {
	c := uint64(1)
	for _, v := range p.Vars {
		c *= uint64(len(v.Candidates))
	}
	return c
}

// baseMatrix builds the matrix with fixed entries installed; variable
// positions are left at zero and filled by the search.
func (p *Problem) baseMatrix() *Matrix {
	m := NewMatrix(p.N)
	for _, f := range p.Fixed {
		m.Set(f.I, f.J, f.Value)
	}
	return m
}

// orderedVars returns variable indices sorted in upper-triangular
// lexicographic position order, and deduplicated sorted candidates per
// variable.
func (p *Problem) orderedVars() ([]int, [][]*big.Rat) {
	idx := make([]int, len(p.Vars))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		va, vb := p.Vars[idx[a]], p.Vars[idx[b]]
		if va.I != vb.I {
			return va.I < vb.I
		}
		return va.J < vb.J
	})
	cands := make([][]*big.Rat, len(idx))
	for k, vi := range idx {
		cs := make([]*big.Rat, len(p.Vars[vi].Candidates))
		for i, c := range p.Vars[vi].Candidates {
			cs[i] = new(big.Rat).Set(c)
		}
		sort.Slice(cs, func(a, b int) bool { return cs[a].Cmp(cs[b]) < 0 })
		// Deduplicate.
		out := cs[:0]
		for i, c := range cs {
			if i == 0 || c.Cmp(cs[i-1]) != 0 {
				out = append(out, c)
			}
		}
		cands[k] = out
	}
	return idx, cands
}
