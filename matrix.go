// Package linsolve solves sparse linear systems over the rationals exactly.
//
// All arithmetic uses math/big.Rat; there are no floating-point rank
// thresholds anywhere. Solve classifies a system A x = b as inconsistent
// (with an explicit certificate: a linear combination of the equations
// yielding a nonzero constant), uniquely solvable, or underdetermined
// (with a parametric solution whose parameters correspond to the free
// variables in increasing column-index order).
package linsolve

import "math/big"

// SparseMatrix is a row-major sparse matrix with exact rational entries.
// The zero value is not usable; construct with NewSparseMatrix.
type SparseMatrix struct {
	rows []map[int]*big.Rat
	cols int
}

// NewSparseMatrix returns a rows x cols zero matrix.
func NewSparseMatrix(rows, cols int) *SparseMatrix {
	if rows < 0 || cols < 0 {
		panic("linsolve: negative matrix dimension")
	}
	m := &SparseMatrix{rows: make([]map[int]*big.Rat, rows), cols: cols}
	for i := range m.rows {
		m.rows[i] = make(map[int]*big.Rat)
	}
	return m
}

// Rows returns the number of rows.
func (m *SparseMatrix) Rows() int { return len(m.rows) }

// Cols returns the number of columns.
func (m *SparseMatrix) Cols() int { return m.cols }

// Set assigns A[r][c] = v. A zero value deletes the entry, preserving
// sparsity. The value is copied, so the caller may reuse v.
func (m *SparseMatrix) Set(r, c int, v *big.Rat) {
	m.checkBounds(r, c)
	if v == nil || v.Sign() == 0 {
		delete(m.rows[r], c)
		return
	}
	m.rows[r][c] = new(big.Rat).Set(v)
}

// At returns a copy of A[r][c] (a fresh zero Rat for absent entries).
func (m *SparseMatrix) At(r, c int) *big.Rat {
	m.checkBounds(r, c)
	if v, ok := m.rows[r][c]; ok {
		return new(big.Rat).Set(v)
	}
	return new(big.Rat)
}

// Row returns a deep copy of row r as a column-index map.
func (m *SparseMatrix) Row(r int) map[int]*big.Rat {
	if r < 0 || r >= len(m.rows) {
		panic("linsolve: row index out of range")
	}
	return cloneRow(m.rows[r])
}

func (m *SparseMatrix) checkBounds(r, c int) {
	if r < 0 || r >= len(m.rows) || c < 0 || c >= m.cols {
		panic("linsolve: matrix index out of range")
	}
}

func cloneRow(row map[int]*big.Rat) map[int]*big.Rat {
	out := make(map[int]*big.Rat, len(row))
	for c, v := range row {
		out[c] = new(big.Rat).Set(v)
	}
	return out
}

// NewRat is a convenience constructor for the rational a/b.
func NewRat(a, b int64) *big.Rat { return big.NewRat(a, b) }

// DenseVec returns a fresh zero vector of length n.
func DenseVec(n int) []*big.Rat {
	v := make([]*big.Rat, n)
	for i := range v {
		v[i] = new(big.Rat)
	}
	return v
}
