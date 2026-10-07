package psdcomplete

import "math/big"

// Matrix is a dense square matrix with exact rational entries.
type Matrix [][]*big.Rat

// NewMatrix returns an n x n zero matrix.
func NewMatrix(n int) Matrix {
	m := make(Matrix, n)
	for i := range m {
		m[i] = make([]*big.Rat, n)
		for j := range m[i] {
			m[i][j] = new(big.Rat)
		}
	}
	return m
}

// Clone returns a deep copy of m.
func (m Matrix) Clone() Matrix {
	out := make(Matrix, len(m))
	for i := range m {
		out[i] = make([]*big.Rat, len(m[i]))
		for j := range m[i] {
			out[i][j] = cloneRat(m[i][j])
		}
	}
	return out
}

// Trace returns the exact trace of m.
func (m Matrix) Trace() *big.Rat {
	tr := new(big.Rat)
	for i := range m {
		tr.Add(tr, m[i][i])
	}
	return tr
}

// Submatrix returns the principal submatrix of m on the given indices.
func (m Matrix) Submatrix(indices []int) Matrix {
	out := NewMatrix(len(indices))
	for a, i := range indices {
		for b, j := range indices {
			out[a][b] = cloneRat(m[i][j])
		}
	}
	return out
}

// Det computes the determinant exactly using the fraction-free Bareiss
// algorithm on big.Rat values. It is O(n^3) and exact for any size; the
// library only requires n <= 4.
func Det(m Matrix) *big.Rat {
	n := len(m)
	if n == 0 {
		return big.NewRat(1, 1)
	}
	a := m.Clone()
	sign := int64(1)
	prev := big.NewRat(1, 1)
	for k := 0; k < n-1; k++ {
		if a[k][k].Sign() == 0 {
			pivot := -1
			for i := k + 1; i < n; i++ {
				if a[i][k].Sign() != 0 {
					pivot = i
					break
				}
			}
			if pivot == -1 {
				return new(big.Rat)
			}
			a[k], a[pivot] = a[pivot], a[k]
			sign = -sign
		}
		for i := k + 1; i < n; i++ {
			for j := k + 1; j < n; j++ {
				t := new(big.Rat).Mul(a[i][j], a[k][k])
				t.Sub(t, new(big.Rat).Mul(a[i][k], a[k][j]))
				t.Quo(t, prev)
				a[i][j] = t
			}
		}
		prev = cloneRat(a[k][k])
	}
	res := cloneRat(a[n-1][n-1])
	if sign < 0 {
		res.Neg(res)
	}
	return res
}
