package disclosure

import (
	"fmt"
	"sort"
)

type cellEnum struct {
	set     map[int64]struct{}
	witness map[int64][]int64
}

type enumConfig struct {
	published []bool // one entry per constraint
	collect   bool   // keep a witness table for every feasible value
	stopEarly bool   // stop once every sensitive cell reaches K

	fixedIdx int
	fixedVal int64
	hasFixed bool
}

type enumResult struct {
	cells    []cellEnum // one entry per sensitive cell
	nodes    int64
	limitHit bool

	// hasFixed results: whether any full table assigns the fixed cell the
	// requested value, plus one such table.
	found bool
	table []int64
}

// enumerate explores every integer grid consistent with the cell bounds and
// the published equality constraints. Domains are small (at most 16 cells),
// and partial constraint propagation prunes every branch that cannot reach a
// right-hand side.
func (c *compiled) enumerate(cfg enumConfig) enumResult {
	n := c.rows * c.cols
	assignment := make([]int64, n)
	copy(assignment, c.cellMin)

	res := enumResult{cells: make([]cellEnum, len(c.sensIdx))}
	for i := range res.cells {
		res.cells[i].set = make(map[int64]struct{})
		if cfg.collect {
			res.cells[i].witness = make(map[int64][]int64)
		}
	}

	var dfs func(pos int) bool
	dfs = func(pos int) bool {
		res.nodes++
		if res.nodes > c.enumCap {
			res.limitHit = true
			return true
		}
		if !c.partialOK(cfg.published, assignment, pos) {
			return false
		}
		if pos == n {
			if cfg.hasFixed {
				table := make([]int64, n)
				copy(table, assignment)
				res.found = true
				res.table = table
				return true
			}
			for i, idx := range c.sensIdx {
				value := assignment[idx]
				res.cells[i].set[value] = struct{}{}
				if cfg.collect {
					witness := make([]int64, n)
					copy(witness, assignment)
					res.cells[i].witness[value] = witness
				}
			}
			if cfg.stopEarly && c.allMeetK(res) {
				return true
			}
			return false
		}

		lo, hi := c.cellMin[pos], c.cellMax[pos]
		if cfg.hasFixed && pos == cfg.fixedIdx {
			lo, hi = cfg.fixedVal, cfg.fixedVal
		}
		for v := lo; v <= hi; v++ {
			assignment[pos] = v
			if dfs(pos + 1) {
				return true
			}
			if v == hi { // guard against int64 wrap
				break
			}
		}
		assignment[pos] = c.cellMin[pos]
		return false
	}

	dfs(0)
	return res
}

func (c *compiled) allMeetK(res enumResult) bool {
	for i, sc := range c.sensitive {
		if len(res.cells[i].set) < sc.K {
			return false
		}
	}
	return true
}

// partialOK verifies that every published equality can still be satisfied
// given the first pos assigned cells. Unassigned cells contribute an inclusive
// [lo, hi] interval computed from their bounds, including negative
// coefficients. Overflow makes the constraint un-prunable rather than failing.
func (c *compiled) partialOK(published []bool, assignment []int64, pos int) bool {
	for ci := range c.constraints {
		if !published[ci] {
			continue
		}
		var sum int64
		var lo, hi int64
		prunable := true
		for j, coeff := range c.termCoeffs[ci] {
			idx := c.termRows[ci][j]*c.cols + c.termCols[ci][j]
			if idx < pos {
				s, ok := mulAdd(sum, coeff, assignment[idx])
				if !ok {
					prunable = false
					break
				}
				sum = s
			} else {
				contribLo, okLo := boundContrib(coeff, c.cellMin[idx], c.cellMax[idx], true)
				contribHi, okHi := boundContrib(coeff, c.cellMin[idx], c.cellMax[idx], false)
				if !okLo || !okHi {
					prunable = false
					break
				}
				l, ok1 := safeAdd(lo, contribLo)
				h, ok2 := safeAdd(hi, contribHi)
				if !ok1 || !ok2 {
					prunable = false
					break
				}
				lo, hi = l, h
			}
		}
		if !prunable {
			continue
		}
		low, ok1 := safeAdd(sum, lo)
		high, ok2 := safeAdd(sum, hi)
		if !ok1 || !ok2 {
			continue
		}
		if c.termRHS[ci] < low || c.termRHS[ci] > high {
			return false
		}
	}
	return true
}

func boundContrib(coeff, minVal, maxVal int64, wantLow bool) (int64, bool) {
	if coeff > 0 {
		if wantLow {
			return safeMul(coeff, minVal)
		}
		return safeMul(coeff, maxVal)
	}
	if wantLow {
		return safeMul(coeff, maxVal)
	}
	return safeMul(coeff, minVal)
}

// feasibility is the guard used while searching suppression sets. It only
// needs a yes/no answer and stops as soon as every sensitive cell reaches K.
func (c *compiled) feasibility(published []bool) (bool, int64, bool) {
	res := c.enumerate(enumConfig{published: published, stopEarly: true})
	if res.limitHit {
		return false, res.nodes, true
	}
	return c.allMeetK(res), res.nodes, false
}

// explainInfeasible produces a re-checkable reason for one excluded value:
// either a concrete constraint whose reachable interval cannot contain the
// RHS, or an exhaustive-search statement when propagation alone cannot prove
// the exclusion.
func (c *compiled) explainInfeasible(published []bool, idx int, value int64, nodes int64) string {
	row, col := idx/c.cols, idx%c.cols

	for ci := range c.constraints {
		if !published[ci] {
			continue
		}
		var sum, lo, hi int64
		prunable := true
		for j, coeff := range c.termCoeffs[ci] {
			cellIdx := c.termRows[ci][j]*c.cols + c.termCols[ci][j]
			if cellIdx == idx {
				s, ok := mulAdd(sum, coeff, value)
				if !ok {
					prunable = false
					break
				}
				sum = s
				continue
			}
			contribLo, okLo := boundContrib(coeff, c.cellMin[cellIdx], c.cellMax[cellIdx], true)
			contribHi, okHi := boundContrib(coeff, c.cellMin[cellIdx], c.cellMax[cellIdx], false)
			if !okLo || !okHi {
				prunable = false
				break
			}
			l, ok1 := safeAdd(lo, contribLo)
			h, ok2 := safeAdd(hi, contribHi)
			if !ok1 || !ok2 {
				prunable = false
				break
			}
			lo, hi = l, h
		}
		if !prunable {
			continue
		}
		low, ok1 := safeAdd(sum, lo)
		high, ok2 := safeAdd(sum, hi)
		if !ok1 || !ok2 {
			continue
		}
		if c.termRHS[ci] < low || c.termRHS[ci] > high {
			return fmt.Sprintf(
				"cell (%d,%d)=%d excluded by %s: fixed contribution %d plus the bounded remainder [%d,%d] gives reachable weighted sum [%d,%d], which cannot equal RHS %d",
				row, col, value, c.constraintLabel(ci), sum, lo, hi, low, high, c.termRHS[ci])
		}
	}

	return fmt.Sprintf(
		"cell (%d,%d)=%d excluded: exhaustive enumeration of the remaining %d cells over published constraints and cell bounds found no completion (%d assignments explored before the value was ruled out; rerun the enumerator to reproduce)",
		row, col, value, c.rows*c.cols-1, nodes)
}

func (c *compiled) constraintLabel(ci int) string {
	if c.constraints[ci].ID != "" {
		return fmt.Sprintf("constraint %q", c.constraints[ci].ID)
	}
	return fmt.Sprintf("constraint #%d", ci)
}

// sortedValues returns the feasible values in ascending order.
func sortedValues(set map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
