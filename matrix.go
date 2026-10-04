// Package sparselu implements sparse LU factorization of small rational
// matrices with full pivoting, exact arithmetic, and a resumable
// budget-limited factorizer.
package sparselu

import (
	"fmt"
	"math/big"
	"strings"
)

// Matrix is a sparse matrix of exact rationals. Zero values are never
// stored: setting an entry to zero deletes it, so NNZ always counts
// genuine nonzeros and cancelled entries leave no residue.
type Matrix struct {
	Rows, Cols int
	rows       map[int]map[int]*big.Rat
}

// NewMatrix returns a rows x cols zero matrix.
func NewMatrix(rows, cols int) *Matrix {
	if rows < 0 || cols < 0 {
		panic("sparselu: negative dimensions")
	}
	return &Matrix{Rows: rows, Cols: cols, rows: make(map[int]map[int]*big.Rat)}
}

func (m *Matrix) checkBounds(r, c int) {
	if r < 0 || r >= m.Rows || c < 0 || c >= m.Cols {
		panic(fmt.Sprintf("sparselu: index (%d,%d) out of bounds %dx%d", r, c, m.Rows, m.Cols))
	}
}

// at returns the stored value at (r,c) or nil if absent. The returned
// pointer is shared with the matrix; do not mutate it.
func (m *Matrix) at(r, c int) *big.Rat {
	if row, ok := m.rows[r]; ok {
		return row[c]
	}
	return nil
}

// Get returns a copy of the value at (r,c), or zero if absent.
func (m *Matrix) Get(r, c int) *big.Rat {
	m.checkBounds(r, c)
	if v := m.at(r, c); v != nil {
		return new(big.Rat).Set(v)
	}
	return new(big.Rat)
}

// Set stores a copy of v at (r,c). A zero (or nil) v deletes the entry,
// so cancelled entries never remain as stored zeros.
func (m *Matrix) Set(r, c int, v *big.Rat) {
	m.checkBounds(r, c)
	if v == nil || v.Sign() == 0 {
		m.delete(r, c)
		return
	}
	row, ok := m.rows[r]
	if !ok {
		row = make(map[int]*big.Rat)
		m.rows[r] = row
	}
	row[c] = new(big.Rat).Set(v)
}

func (m *Matrix) delete(r, c int) {
	if row, ok := m.rows[r]; ok {
		delete(row, c)
		if len(row) == 0 {
			delete(m.rows, r)
		}
	}
}

// NNZ returns the number of stored nonzero entries.
func (m *Matrix) NNZ() int {
	n := 0
	for _, row := range m.rows {
		n += len(row)
	}
	return n
}

// Clone returns a deep copy of m.
func (m *Matrix) Clone() *Matrix {
	out := NewMatrix(m.Rows, m.Cols)
	for r, row := range m.rows {
		nr := make(map[int]*big.Rat, len(row))
		for c, v := range row {
			nr[c] = new(big.Rat).Set(v)
		}
		out.rows[r] = nr
	}
	return out
}

// String renders the matrix densely, one row per line.
func (m *Matrix) String() string {
	var b strings.Builder
	for r := 0; r < m.Rows; r++ {
		for c := 0; c < m.Cols; c++ {
			if c > 0 {
				b.WriteByte('\t')
			}
			if v := m.at(r, c); v != nil {
				b.WriteString(v.RatString())
			} else {
				b.WriteByte('0')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Multiply returns the exact sparse product a*b. It is independent of the
// factorization code and is used to verify PAQ == LU.
func Multiply(a, b *Matrix) *Matrix {
	if a.Cols != b.Rows {
		panic(fmt.Sprintf("sparselu: cannot multiply %dx%d by %dx%d", a.Rows, a.Cols, b.Rows, b.Cols))
	}
	out := NewMatrix(a.Rows, b.Cols)
	for i, arow := range a.rows {
		for k, av := range arow {
			brow, ok := b.rows[k]
			if !ok {
				continue
			}
			orow, ok := out.rows[i]
			if !ok {
				orow = make(map[int]*big.Rat)
				out.rows[i] = orow
			}
			for j, bv := range brow {
				prod := new(big.Rat).Mul(av, bv)
				if cur := orow[j]; cur != nil {
					cur.Add(cur, prod)
					if cur.Sign() == 0 {
						delete(orow, j)
					}
				} else if prod.Sign() != 0 {
					orow[j] = prod
				}
			}
		}
		if orow, ok := out.rows[i]; ok && len(orow) == 0 {
			delete(out.rows, i)
		}
	}
	return out
}

// Permute returns the matrix P*M*Q where prow and pcol are permutation
// vectors: result[i][j] = m[prow[i]][pcol[j]].
func Permute(m *Matrix, prow, pcol []int) *Matrix {
	if len(prow) != m.Rows || len(pcol) != m.Cols {
		panic("sparselu: permutation length mismatch")
	}
	out := NewMatrix(m.Rows, m.Cols)
	for i := 0; i < m.Rows; i++ {
		row, ok := m.rows[prow[i]]
		if !ok {
			continue
		}
		orow := make(map[int]*big.Rat)
		for j := 0; j < m.Cols; j++ {
			if v := row[pcol[j]]; v != nil {
				orow[j] = new(big.Rat).Set(v)
			}
		}
		if len(orow) > 0 {
			out.rows[i] = orow
		}
	}
	return out
}

// Equal reports whether a and b have identical dimensions and entries.
func Equal(a, b *Matrix) bool {
	if a.Rows != b.Rows || a.Cols != b.Cols || a.NNZ() != b.NNZ() {
		return false
	}
	for r, row := range a.rows {
		for c, v := range row {
			bv := b.at(r, c)
			if bv == nil || v.Cmp(bv) != 0 {
				return false
			}
		}
	}
	return true
}
