// Package sparselu implements sparse LU factorization of small rational
// matrices with row and column pivoting, exact arithmetic, fill-in
// accounting, and a resumable budget-limited factorizer.
package sparselu

import (
	"math/big"
	"strings"
)

// Matrix is a sparse matrix of exact rationals stored as rows of
// column-indexed values. The invariant "no stored zeros" is enforced by
// Set: assigning a zero value removes the entry.
type Matrix struct {
	Rows int
	Cols int
	rows []map[int]*big.Rat
}

// New returns an empty rows x cols matrix.
func New(rows, cols int) *Matrix {
	if rows < 0 || cols < 0 {
		panic("sparselu: negative dimension")
	}
	m := &Matrix{Rows: rows, Cols: cols, rows: make([]map[int]*big.Rat, rows)}
	for i := range m.rows {
		m.rows[i] = make(map[int]*big.Rat)
	}
	return m
}

// Set assigns v to position (i, j). A nil or zero v removes the entry, so
// cancelled-out zeros never remain stored.
func (m *Matrix) Set(i, j int, v *big.Rat) {
	if i < 0 || i >= m.Rows || j < 0 || j >= m.Cols {
		panic("sparselu: index out of range")
	}
	if v == nil || v.Sign() == 0 {
		delete(m.rows[i], j)
		return
	}
	m.rows[i][j] = new(big.Rat).Set(v)
}

// At returns the value at (i, j), or zero if absent. The result is a copy.
func (m *Matrix) At(i, j int) *big.Rat {
	if i < 0 || i >= m.Rows || j < 0 || j >= m.Cols {
		panic("sparselu: index out of range")
	}
	if v, ok := m.rows[i][j]; ok {
		return new(big.Rat).Set(v)
	}
	return new(big.Rat)
}

// NNZ returns the number of stored (nonzero) entries.
func (m *Matrix) NNZ() int {
	n := 0
	for _, r := range m.rows {
		n += len(r)
	}
	return n
}

// Clone returns a deep copy.
func (m *Matrix) Clone() *Matrix {
	c := New(m.Rows, m.Cols)
	for i, r := range m.rows {
		for j, v := range r {
			c.rows[i][j] = new(big.Rat).Set(v)
		}
	}
	return c
}

// Equal reports exact structural and numeric equality.
func (m *Matrix) Equal(o *Matrix) bool {
	if m.Rows != o.Rows || m.Cols != o.Cols {
		return false
	}
	for i := 0; i < m.Rows; i++ {
		if len(m.rows[i]) != len(o.rows[i]) {
			return false
		}
		for j, v := range m.rows[i] {
			w, ok := o.rows[i][j]
			if !ok || v.Cmp(w) != 0 {
				return false
			}
		}
	}
	return true
}

// Mul computes a*b with exact rational arithmetic. It is independent of the
// factorizer internals and is used by Verify to check PAQ = LU.
func Mul(a, b *Matrix) *Matrix {
	if a.Cols != b.Rows {
		panic("sparselu: dimension mismatch in Mul")
	}
	c := New(a.Rows, b.Cols)
	acc := new(big.Rat)
	term := new(big.Rat)
	for i := 0; i < a.Rows; i++ {
		for k, av := range a.rows[i] {
			for j, bv := range b.rows[k] {
				ev := c.rows[i][j]
				if ev == nil {
					ev = new(big.Rat)
				}
				term.Mul(av, bv)
				acc.Set(ev)
				acc.Add(acc, term)
				if acc.Sign() == 0 {
					delete(c.rows[i], j)
				} else {
					c.rows[i][j] = new(big.Rat).Set(acc)
				}
			}
		}
	}
	return c
}

// String renders the matrix densely, for debugging and tests.
func (m *Matrix) String() string {
	var sb strings.Builder
	for i := 0; i < m.Rows; i++ {
		parts := make([]string, m.Cols)
		for j := 0; j < m.Cols; j++ {
			parts[j] = m.At(i, j).RatString()
		}
		sb.WriteString("[")
		sb.WriteString(strings.Join(parts, " "))
		sb.WriteString("]\n")
	}
	return sb.String()
}

func identity(n int) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return p
}

func isPermutation(p []int) bool {
	seen := make([]bool, len(p))
	for _, v := range p {
		if v < 0 || v >= len(p) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}
