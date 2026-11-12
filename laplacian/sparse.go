package laplacian

import (
	"fmt"
	"math"
	"sort"
)

// cooEntry is a triplet used while assembling sparse matrices.
type cooEntry struct {
	row, col int
	value    float64
}

// cscMatrix is a sparse matrix in compressed sparse column storage. The
// Laplacian uses full storage; a reduced normal-equation matrix stores only
// its lower triangle (row >= column) together with a symmetric get/matVec.
type cscMatrix struct {
	n    int
	cols []int // length n+1
	rows []int // row indices, ascending within each column
	vals []float64
}

// buildMode controls how buildCSC treats off-diagonal triplets.
type buildMode int

const (
	// buildFull keeps every triplet as given.
	buildFull buildMode = iota
	// buildLowerDiscard drops triplets above the diagonal.
	buildLowerDiscard
)

// buildCSC assembles a CSC matrix from triplets. Duplicate entries are summed.
func buildCSC(entries []cooEntry, n int, mode buildMode) *cscMatrix {
	type pair struct {
		row int
		val float64
	}
	columns := make([][]pair, n)

	for _, e := range entries {
		if e.row < 0 || e.row >= n || e.col < 0 || e.col >= n {
			continue
		}
		if e.row < e.col {
			if mode == buildLowerDiscard {
				continue
			}
		}
		columns[e.col] = append(columns[e.col], pair{e.row, e.value})
	}

	cols := make([]int, n+1)
	total := 0
	for c := 0; c < n; c++ {
		col := columns[c]
		sort.Slice(col, func(i, j int) bool { return col[i].row < col[j].row })

		// Merge duplicate row indices.
		merged := col[:0]
		for _, p := range col {
			if len(merged) > 0 && merged[len(merged)-1].row == p.row {
				merged[len(merged)-1].val += p.val
			} else {
				merged = append(merged, p)
			}
		}
		columns[c] = merged
		total += len(merged)
		cols[c+1] = total
	}

	rows := make([]int, 0, total)
	vals := make([]float64, 0, total)
	for c := 0; c < n; c++ {
		for _, p := range columns[c] {
			rows = append(rows, p.row)
			vals = append(vals, p.val)
		}
	}

	return &cscMatrix{n: n, cols: cols, rows: rows, vals: vals}
}

// get returns the value at (row, col) for a matrix storing its lower triangle.
func (m *cscMatrix) get(row, col int) float64 {
	if row < col {
		row, col = col, row
	}
	for k := m.cols[col]; k < m.cols[col+1]; k++ {
		if m.rows[k] == row {
			return m.vals[k]
		}
	}
	return 0
}

// matVec computes y = A*x for a matrix in full CSC storage.
func (m *cscMatrix) matVec(x []float64) []float64 {
	y := make([]float64, m.n)
	for c := 0; c < m.n; c++ {
		for k := m.cols[c]; k < m.cols[c+1]; k++ {
			y[m.rows[k]] += m.vals[k] * x[c]
		}
	}
	return y
}

// ldlFactorization is a simplicial LDLᵀ factorization of a symmetric
// positive-definite matrix: A = L D Lᵀ with unit diagonal L. Both L and D are
// kept in CSC-style column storage; L stores only off-diagonal entries.
type ldlFactorization struct {
	n       int
	lCols   []int     // length n+1, column pointers of L
	lRows   []int     // row indices (i > column), ascending per column
	lVals   []float64 // L entries, unit diagonal implicit
	diag    []float64 // diagonal entries of D
	rowList [][]int   // rows present in each column of L, for column access
}

// factorLDL computes a left-looking simplicial LDLᵀ factorization. The input
// must store its lower triangle. A singular pivot returns an error locating
// the offending variable.
//
// For column k it gathers the symbolic pattern (union of A's column pattern
// with fill propagated through prior L columns) into a dense workspace, then
// applies L[:,j] D[j] updates for every prior non-zero L[k,j].
func factorLDL(a *cscMatrix) (*ldlFactorization, error) {
	n := a.n

	diag := make([]float64, n)
	rowLists := make([][]int, n)
	valLists := make([][]float64, n)

	work := make([]float64, n)
	mark := make([]int, n) // mark[i] == k+1 means work[i] belongs to column k

	var maxPivot float64

	for k := 0; k < n; k++ {
		// Scatter the lower-triangle part of column k of A into work.
		for p := a.cols[k]; p < a.cols[k+1]; p++ {
			r := a.rows[p]
			if r < k {
				continue // lower-triangle input: r >= k expected
			}
			work[r] = a.vals[p]
			mark[r] = k + 1
		}

		// Left-looking update over every prior column j that reaches row k.
		for j := 0; j < k; j++ {
			lkj := valueInColumn(rowLists[j], valLists[j], k)
			if lkj == 0 {
				continue
			}
			scale := lkj * diag[j]
			work[k] -= scale * lkj
			for idx, r := range rowLists[j] {
				if r <= k {
					continue
				}
				work[r] -= scale * valLists[j][idx]
				mark[r] = k + 1 // fill-in entry joins the column pattern
			}
		}

		dkk := work[k]
		if math.IsNaN(dkk) || math.IsInf(dkk, 0) || math.Abs(dkk) < pivotTolerance(maxPivot) {
			return nil, fmt.Errorf("laplacian: singular reduced system at variable %d", k)
		}
		diag[k] = dkk
		if av := math.Abs(dkk); av > maxPivot {
			maxPivot = av
		}

		// Column k of L: L[i,k] = work[i] / D[k] for active rows i > k.
		rows := make([]int, 0)
		vals := make([]float64, 0)
		for i := k + 1; i < n; i++ {
			if mark[i] != k+1 {
				continue
			}
			v := work[i] / dkk
			work[i] = 0
			mark[i] = 0
			if v == 0 {
				continue
			}
			rows = append(rows, i)
			vals = append(vals, v)
		}
		work[k] = 0
		mark[k] = 0

		rowLists[k] = rows
		valLists[k] = vals
	}

	// Flatten per-column slices into CSC storage.
	lCols := make([]int, n+1)
	total := 0
	for k := 0; k < n; k++ {
		lCols[k+1] = total + len(rowLists[k])
		total += len(rowLists[k])
	}
	lRows := make([]int, 0, total)
	lVals := make([]float64, 0, total)
	for k := 0; k < n; k++ {
		lRows = append(lRows, rowLists[k]...)
		lVals = append(lVals, valLists[k]...)
	}

	return &ldlFactorization{
		n:       n,
		lCols:   lCols,
		lRows:   lRows,
		lVals:   lVals,
		diag:    diag,
		rowList: rowLists,
	}, nil
}

// valueInColumn returns the L entry at the given row of a stored L column.
func valueInColumn(rows []int, vals []float64, target int) float64 {
	for idx, r := range rows {
		if r == target {
			return vals[idx]
		}
		if r > target {
			break
		}
	}
	return 0
}

func pivotTolerance(maxPivot float64) float64 {
	const eps = 1e-14
	if maxPivot == 0 {
		return eps
	}
	return eps * maxPivot
}

// solve computes x for A*x = b using the factorization. It solves
// L y = b, D z = y and Lᵀ x = z in place; b is not modified.
func (f *ldlFactorization) solve(b []float64) []float64 {
	n := f.n
	x := make([]float64, n)
	copy(x, b)

	// Forward substitution L y = b (unit diagonal).
	for j := 0; j < n; j++ {
		for p := f.lCols[j]; p < f.lCols[j+1]; p++ {
			x[f.lRows[p]] -= f.lVals[p] * x[j]
		}
	}

	// Diagonal solve D z = y.
	for i := 0; i < n; i++ {
		x[i] /= f.diag[i]
	}

	// Back substitution Lᵀ x = z.
	for j := n - 1; j >= 0; j-- {
		for p := f.lCols[j]; p < f.lCols[j+1]; p++ {
			x[j] -= f.lVals[p] * x[f.lRows[p]]
		}
	}

	return x
}
