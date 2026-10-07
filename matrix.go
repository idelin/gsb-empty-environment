// Package psdcomp solves positive-semidefinite matrix completion problems
// over the rationals for symmetric matrices of order up to 4.
//
// A problem fixes some entries of an n x n symmetric matrix to rational
// values; every remaining upper-triangular entry must be chosen from a
// finite set of rational candidates. The solver finds a completion that
// makes the matrix positive semidefinite while minimizing the trace;
// ties are broken lexicographically on the upper triangle.
//
// Feasibility is decided by the exact Sylvester criterion for
// semidefiniteness: ALL principal minors must be nonnegative (checking
// only the diagonal and the determinant is not sufficient). The optimal
// solution is returned together with a certificate listing every
// principal minor, and can be cross-validated by an independent exact
// elimination-based PSD test and a pruning-free brute-force search.
package psdcomp

import (
	"fmt"
	"math/big"
)

// MaxN is the maximum matrix order supported.
const MaxN = 4

// MaxCombos is the maximum number of candidate combinations a problem may
// have, per the problem statement (<= 1,000,000).
const MaxCombos = 1_000_000

// Matrix is a dense symmetric matrix with exact rational entries.
// Only A[i][j] with i <= j is meaningful to construct via Set; both
// orientations are stored so algorithms can index freely.
type Matrix struct {
	N int
	A [][]*big.Rat
}

// NewMatrix returns an n x n zero matrix.
func NewMatrix(n int) *Matrix {
	a := make([][]*big.Rat, n)
	for i := range a {
		a[i] = make([]*big.Rat, n)
		for j := range a[i] {
			a[i][j] = new(big.Rat)
		}
	}
	return &Matrix{N: n, A: a}
}

// Clone returns a deep copy.
func (m *Matrix) Clone() *Matrix {
	c := NewMatrix(m.N)
	for i := 0; i < m.N; i++ {
		for j := 0; j < m.N; j++ {
			c.A[i][j].Set(m.A[i][j])
		}
	}
	return c
}

// Set assigns v to both (i,j) and (j,i), preserving symmetry.
func (m *Matrix) Set(i, j int, v *big.Rat) {
	m.A[i][j].Set(v)
	m.A[j][i].Set(v)
}

// Get returns the entry at (i,j).
func (m *Matrix) Get(i, j int) *big.Rat { return m.A[i][j] }

// Trace returns the sum of diagonal entries.
func (m *Matrix) Trace() *big.Rat {
	t := new(big.Rat)
	for i := 0; i < m.N; i++ {
		t.Add(t, m.A[i][i])
	}
	return t
}

// UpperTriangle returns the entries in upper-triangular lexicographic
// order: (0,0),(0,1),...,(0,n-1),(1,1),...,(n-1,n-1).
func (m *Matrix) UpperTriangle() []*big.Rat {
	out := make([]*big.Rat, 0, m.N*(m.N+1)/2)
	for i := 0; i < m.N; i++ {
		for j := i; j < m.N; j++ {
			out = append(out, new(big.Rat).Set(m.A[i][j]))
		}
	}
	return out
}

// Submatrix extracts the principal submatrix on the given row/column
// indices (which must be sorted in increasing order).
func (m *Matrix) Submatrix(indices []int) *Matrix {
	k := len(indices)
	sub := NewMatrix(k)
	for a := 0; a < k; a++ {
		for b := 0; b < k; b++ {
			sub.A[a][b].Set(m.A[indices[a]][indices[b]])
		}
	}
	return sub
}

// String renders the matrix row by row.
func (m *Matrix) String() string {
	s := ""
	for i := 0; i < m.N; i++ {
		for j := 0; j < m.N; j++ {
			s += fmt.Sprintf("%8s", m.A[i][j].RatString())
		}
		s += "\n"
	}
	return s
}

// DetBareiss computes the determinant exactly using the fraction-free
// Bareiss algorithm. Over Q every division is exact, so the result is an
// exact rational.
func DetBareiss(m *Matrix) *big.Rat {
	n := m.N
	if n == 0 {
		return big.NewRat(1, 1)
	}
	a := make([][]*big.Rat, n)
	for i := range a {
		a[i] = make([]*big.Rat, n)
		for j := range a[i] {
			a[i][j] = new(big.Rat).Set(m.A[i][j])
		}
	}
	sign := int64(1)
	prev := big.NewRat(1, 1)
	for k := 0; k < n-1; k++ {
		if a[k][k].Sign() == 0 {
			p := -1
			for r := k + 1; r < n; r++ {
				if a[r][k].Sign() != 0 {
					p = r
					break
				}
			}
			if p < 0 {
				return new(big.Rat)
			}
			a[k], a[p] = a[p], a[k]
			sign = -sign
		}
		pivot := new(big.Rat).Set(a[k][k])
		for i := k + 1; i < n; i++ {
			for j := k + 1; j < n; j++ {
				t1 := new(big.Rat).Mul(a[i][j], pivot)
				t2 := new(big.Rat).Mul(a[i][k], a[k][j])
				t1.Sub(t1, t2)
				a[i][j].Quo(t1, prev)
			}
		}
		prev.Set(pivot)
	}
	res := new(big.Rat).Set(a[n-1][n-1])
	if sign < 0 {
		res.Neg(res)
	}
	return res
}

// DetGauss computes the determinant exactly using Gaussian elimination
// with partial pivoting over big.Rat. It is algorithmically independent
// of DetBareiss and is used for cross-validation.
func DetGauss(m *Matrix) *big.Rat {
	n := m.N
	if n == 0 {
		return big.NewRat(1, 1)
	}
	a := make([][]*big.Rat, n)
	for i := range a {
		a[i] = make([]*big.Rat, n)
		for j := range a[i] {
			a[i][j] = new(big.Rat).Set(m.A[i][j])
		}
	}
	det := big.NewRat(1, 1)
	for col := 0; col < n; col++ {
		p := -1
		for r := col; r < n; r++ {
			if a[r][col].Sign() != 0 {
				p = r
				break
			}
		}
		if p < 0 {
			return new(big.Rat)
		}
		if p != col {
			a[col], a[p] = a[p], a[col]
			det.Neg(det)
		}
		pivot := new(big.Rat).Set(a[col][col])
		det.Mul(det, pivot)
		for r := col + 1; r < n; r++ {
			if a[r][col].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Quo(a[r][col], pivot)
			for c := col; c < n; c++ {
				t := new(big.Rat).Mul(f, a[col][c])
				a[r][c].Sub(a[r][c], t)
			}
		}
	}
	return det
}
