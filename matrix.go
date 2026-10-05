// Package exactmat implements exact rational-arithmetic square matrices.
// It maintains the determinant incrementally across row replacements via
// the Sherman-Morrison rank-1 update, falls back to full exact elimination
// whenever the update denominator is exactly zero or the matrix is singular,
// and solves linear systems exactly, returning independently checkable
// solutions or evidence of inconsistency/underdetermination.
//
// All arithmetic uses math/big.Rat. Zero tests are exact (Sign() == 0);
// no floating-point tolerance is ever involved.
package exactmat

import (
	"errors"
	"fmt"
	"math/big"
)

// Matrix is an n x n matrix over the rationals with a maintained
// determinant, rank, and (when invertible) inverse.
type Matrix struct {
	n    int
	a    [][]*big.Rat
	det  *big.Rat
	inv  [][]*big.Rat // nil while singular
	rank int
}

// NewMatrix builds a Matrix from n rows of length n. The input is copied.
func NewMatrix(rows [][]*big.Rat) (*Matrix, error) {
	n := len(rows)
	if n == 0 {
		return nil, errors.New("exactmat: matrix must have at least one row")
	}
	a := make([][]*big.Rat, n)
	for i, row := range rows {
		if len(row) != n {
			return nil, fmt.Errorf("exactmat: row %d has length %d, want %d", i, len(row), n)
		}
		a[i] = make([]*big.Rat, n)
		for j, v := range row {
			if v == nil {
				return nil, fmt.Errorf("exactmat: nil entry at (%d,%d)", i, j)
			}
			a[i][j] = new(big.Rat).Set(v)
		}
	}
	m := &Matrix{n: n, a: a}
	m.rebuild()
	return m, nil
}

// NewFromInts is a convenience constructor for integer entries.
func NewFromInts(rows [][]int64) (*Matrix, error) {
	rs := make([][]*big.Rat, len(rows))
	for i, row := range rows {
		rs[i] = make([]*big.Rat, len(row))
		for j, v := range row {
			rs[i][j] = big.NewRat(v, 1)
		}
	}
	return NewMatrix(rs)
}

// rebuild recomputes determinant, rank, and inverse from scratch with full
// exact Gaussian elimination. This is the reliable path used on construction
// and whenever the incremental formula cannot be applied.
func (m *Matrix) rebuild() {
	m.det, m.rank = detAndRank(m.a)
	if m.rank == m.n {
		m.inv = inverse(m.a)
	} else {
		m.inv = nil
	}
}

// N returns the dimension.
func (m *Matrix) N() int { return m.n }

// Det returns a copy of the current determinant.
func (m *Matrix) Det() *big.Rat { return new(big.Rat).Set(m.det) }

// Rank returns the current rank.
func (m *Matrix) Rank() int { return m.rank }

// Invertible reports whether the matrix is currently nonsingular.
func (m *Matrix) Invertible() bool { return m.inv != nil }

// At returns a copy of entry (i, j).
func (m *Matrix) At(i, j int) *big.Rat { return new(big.Rat).Set(m.a[i][j]) }

// Row returns a copy of row i.
func (m *Matrix) Row(i int) []*big.Rat {
	out := make([]*big.Rat, m.n)
	for j := range out {
		out[j] = new(big.Rat).Set(m.a[i][j])
	}
	return out
}

// ReplaceRow substitutes row i with the given row and updates the maintained
// determinant, rank, and inverse.
//
// While the matrix is invertible it uses the Sherman-Morrison rank-1
// identity with A' = A + e_i d^T, d = new - old:
//
//	det(A') = det(A) * (1 + d^T A^{-1} e_i)
//
// The denominator 1 + d^T A^{-1} e_i is tested with exact arithmetic. When
// it is exactly zero the updated matrix is singular and the formula has no
// inverse to update, so the matrix switches to the reliable path: full exact
// elimination from scratch. The same reliable path is used whenever the
// matrix is currently singular, which is how a singular matrix recovers to
// invertible after a later row replacement.
func (m *Matrix) ReplaceRow(i int, row []*big.Rat) error {
	if i < 0 || i >= m.n {
		return fmt.Errorf("exactmat: row index %d out of range [0,%d)", i, m.n)
	}
	if len(row) != m.n {
		return fmt.Errorf("exactmat: replacement row has length %d, want %d", len(row), m.n)
	}
	newRow := make([]*big.Rat, m.n)
	delta := make([]*big.Rat, m.n)
	for j := 0; j < m.n; j++ {
		if row[j] == nil {
			return fmt.Errorf("exactmat: nil entry at column %d of replacement row", j)
		}
		newRow[j] = new(big.Rat).Set(row[j])
		delta[j] = new(big.Rat).Sub(newRow[j], m.a[i][j])
	}

	if m.inv == nil {
		m.a[i] = newRow
		m.rebuild()
		return nil
	}

	n := m.n
	col := make([]*big.Rat, n) // A^{-1} e_i, column i of the inverse
	for r := 0; r < n; r++ {
		col[r] = new(big.Rat).Set(m.inv[r][i])
	}
	denom := big.NewRat(1, 1)
	for j := 0; j < n; j++ {
		denom.Add(denom, new(big.Rat).Mul(delta[j], col[j]))
	}
	if denom.Sign() == 0 {
		m.a[i] = newRow
		m.rebuild()
		return nil
	}

	w := make([]*big.Rat, n) // d^T A^{-1}
	for c := 0; c < n; c++ {
		w[c] = new(big.Rat)
		for r := 0; r < n; r++ {
			w[c].Add(w[c], new(big.Rat).Mul(delta[r], m.inv[r][c]))
		}
	}
	denomInv := new(big.Rat).Inv(denom)
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			t := new(big.Rat).Mul(col[r], w[c])
			t.Mul(t, denomInv)
			m.inv[r][c].Sub(m.inv[r][c], t)
		}
	}
	m.det.Mul(m.det, denom)
	m.a[i] = newRow
	return nil
}

// detAndRank computes determinant and rank by exact Gaussian elimination
// with partial pivoting on a copy of a.
func detAndRank(a [][]*big.Rat) (*big.Rat, int) {
	n := len(a)
	m := copyMat(a)
	det := big.NewRat(1, 1)
	rank := 0
	for col := 0; col < n && rank < n; col++ {
		piv := -1
		for r := rank; r < n; r++ {
			if m[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			continue
		}
		if piv != rank {
			m[rank], m[piv] = m[piv], m[rank]
			det.Neg(det)
		}
		pv := new(big.Rat).Set(m[rank][col])
		det.Mul(det, pv)
		for r := rank + 1; r < n; r++ {
			if m[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Quo(m[r][col], pv)
			for c := col; c < n; c++ {
				m[r][c].Sub(m[r][c], new(big.Rat).Mul(f, m[rank][c]))
			}
		}
		rank++
	}
	if rank < n {
		det.SetInt64(0)
	}
	return det, rank
}

// inverse computes the exact inverse by Gauss-Jordan elimination.
// The caller must ensure a is invertible.
func inverse(a [][]*big.Rat) [][]*big.Rat {
	n := len(a)
	aug := make([][]*big.Rat, n)
	for i := 0; i < n; i++ {
		aug[i] = make([]*big.Rat, 2*n)
		for j := 0; j < n; j++ {
			aug[i][j] = new(big.Rat).Set(a[i][j])
		}
		for j := 0; j < n; j++ {
			aug[i][n+j] = big.NewRat(0, 1)
		}
		aug[i][n+i].SetInt64(1)
	}
	row := 0
	for col := 0; col < n; col++ {
		piv := -1
		for r := row; r < n; r++ {
			if aug[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			continue
		}
		aug[row], aug[piv] = aug[piv], aug[row]
		pv := new(big.Rat).Set(aug[row][col])
		for c := 0; c < 2*n; c++ {
			aug[row][c].Quo(aug[row][c], pv)
		}
		for r := 0; r < n; r++ {
			if r == row || aug[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(aug[r][col])
			for c := 0; c < 2*n; c++ {
				aug[r][c].Sub(aug[r][c], new(big.Rat).Mul(f, aug[row][c]))
			}
		}
		row++
	}
	inv := make([][]*big.Rat, n)
	for i := 0; i < n; i++ {
		inv[i] = aug[i][n:]
	}
	return inv
}

// rref reduces a (in place) to reduced row echelon form and returns the
// pivot column indices in increasing order.
func rref(a [][]*big.Rat) []int {
	rows := len(a)
	if rows == 0 {
		return nil
	}
	cols := len(a[0])
	pivots := []int{}
	row := 0
	for col := 0; col < cols && row < rows; col++ {
		piv := -1
		for r := row; r < rows; r++ {
			if a[r][col].Sign() != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			continue
		}
		a[row], a[piv] = a[piv], a[row]
		pv := new(big.Rat).Set(a[row][col])
		for c := col; c < cols; c++ {
			a[row][c].Quo(a[row][c], pv)
		}
		for r := 0; r < rows; r++ {
			if r == row || a[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Set(a[r][col])
			for c := col; c < cols; c++ {
				a[r][c].Sub(a[r][c], new(big.Rat).Mul(f, a[row][c]))
			}
		}
		pivots = append(pivots, col)
		row++
	}
	return pivots
}

func copyMat(a [][]*big.Rat) [][]*big.Rat {
	out := make([][]*big.Rat, len(a))
	for i, row := range a {
		out[i] = make([]*big.Rat, len(row))
		for j, v := range row {
			out[i][j] = new(big.Rat).Set(v)
		}
	}
	return out
}

func matVec(a [][]*big.Rat, x []*big.Rat) []*big.Rat {
	out := make([]*big.Rat, len(a))
	for i := range a {
		out[i] = new(big.Rat)
		for j := range x {
			out[i].Add(out[i], new(big.Rat).Mul(a[i][j], x[j]))
		}
	}
	return out
}

func vecEqual(x, y []*big.Rat) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i].Cmp(y[i]) != 0 {
			return false
		}
	}
	return true
}

func isZeroVec(x []*big.Rat) bool {
	for _, v := range x {
		if v.Sign() != 0 {
			return false
		}
	}
	return true
}
