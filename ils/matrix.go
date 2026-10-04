// Package ils solves integer linear systems A x = b over the integers.
//
// Given an integer matrix A (m x n, possibly rectangular, rank deficient,
// with zero rows/columns) and an integer vector b, Solve returns either
//   - a particular integer solution x0 together with a basis of the
//     homogeneous solution lattice {x in Z^n : A x = 0}, or
//   - a certificate of infeasibility: a rational vector y with yA in Z^n
//     but y*b not in Z (such a y proves no integer solution exists).
//
// All arithmetic uses math/big, so elimination never overflows.
package ils

import "math/big"

// Matrix is a dense integer matrix in row-major order.
type Matrix struct {
	Rows int
	Cols int
	data []*big.Int
}

// NewMatrix returns a zero rows x cols matrix.
func NewMatrix(rows, cols int) *Matrix {
	m := &Matrix{Rows: rows, Cols: cols, data: make([]*big.Int, rows*cols)}
	for i := range m.data {
		m.data[i] = new(big.Int)
	}
	return m
}

// MatrixFromInts builds a matrix from int64 rows.
func MatrixFromInts(rows [][]int64) *Matrix {
	m := NewMatrix(len(rows), 0)
	if len(rows) > 0 {
		m.Cols = len(rows[0])
		m.data = make([]*big.Int, m.Rows*m.Cols)
		for i, row := range rows {
			for j, v := range row {
				m.data[i*m.Cols+j] = big.NewInt(v)
			}
		}
	}
	return m
}

// At returns the entry at (i, j). The returned pointer is shared with the
// matrix; do not mutate it directly.
func (m *Matrix) At(i, j int) *big.Int { return m.data[i*m.Cols+j] }

// Set assigns v to the entry at (i, j) (copying the value).
func (m *Matrix) Set(i, j int, v *big.Int) { m.data[i*m.Cols+j].Set(v) }

// Copy returns a deep copy.
func (m *Matrix) Copy() *Matrix {
	c := &Matrix{Rows: m.Rows, Cols: m.Cols, data: make([]*big.Int, len(m.data))}
	for i, v := range m.data {
		c.data[i] = new(big.Int).Set(v)
	}
	return c
}

// Transpose returns the transposed matrix.
func (m *Matrix) Transpose() *Matrix {
	t := NewMatrix(m.Cols, m.Rows)
	for i := 0; i < m.Rows; i++ {
		for j := 0; j < m.Cols; j++ {
			t.data[j*m.Rows+i].Set(m.data[i*m.Cols+j])
		}
	}
	return t
}

// Identity returns the n x n identity matrix.
func Identity(n int) *Matrix {
	m := NewMatrix(n, n)
	for i := 0; i < n; i++ {
		m.data[i*n+i].SetInt64(1)
	}
	return m
}

// SwapRows swaps rows i and j.
func (m *Matrix) SwapRows(i, j int) {
	for c := 0; c < m.Cols; c++ {
		m.data[i*m.Cols+c], m.data[j*m.Cols+c] = m.data[j*m.Cols+c], m.data[i*m.Cols+c]
	}
}

// NegRow negates row i in place.
func (m *Matrix) NegRow(i int) {
	for c := 0; c < m.Cols; c++ {
		m.data[i*m.Cols+c].Neg(m.data[i*m.Cols+c])
	}
}

// AddScaledRow adds s times row src to row dst.
func (m *Matrix) AddScaledRow(dst, src int, s *big.Int) {
	if s.Sign() == 0 {
		return
	}
	tmp := new(big.Int)
	for c := 0; c < m.Cols; c++ {
		tmp.Mul(s, m.data[src*m.Cols+c])
		m.data[dst*m.Cols+c].Add(m.data[dst*m.Cols+c], tmp)
	}
}

// MulVec returns the matrix-vector product m*x. x must have length m.Cols.
func (m *Matrix) MulVec(x []*big.Int) []*big.Int {
	out := make([]*big.Int, m.Rows)
	tmp := new(big.Int)
	for i := 0; i < m.Rows; i++ {
		acc := new(big.Int)
		for j := 0; j < m.Cols; j++ {
			tmp.Mul(m.data[i*m.Cols+j], x[j])
			acc.Add(acc, tmp)
		}
		out[i] = acc
	}
	return out
}

// Row returns a copy of row i.
func (m *Matrix) Row(i int) []*big.Int {
	row := make([]*big.Int, m.Cols)
	for c := 0; c < m.Cols; c++ {
		row[c] = new(big.Int).Set(m.data[i*m.Cols+c])
	}
	return row
}
