package taskpkg

import (
	"fmt"
	"math"
)

// denseSolve solves a dense square system A x = b by Gaussian elimination with
// partial pivoting. It is the independent dense reference used in tests to
// validate the sparse solver on small meshes.
func denseSolve(a [][]float64, b []float64) ([]float64, error) {
	n := len(a)
	if n == 0 {
		return []float64{}, nil
	}
	if len(b) != n {
		return nil, fmt.Errorf("denseSolve: dimension mismatch, %d rows and %d rhs entries", n, len(b))
	}

	m := make([][]float64, n)
	for i := range a {
		if len(a[i]) != n {
			return nil, fmt.Errorf("denseSolve: matrix is not square, row %d has %d entries", i, len(a[i]))
		}
		m[i] = append([]float64(nil), a[i]...)
		m[i] = append(m[i], b[i])
	}

	for col := 0; col < n; col++ {
		pivot := col
		best := math.Abs(m[col][col])
		for r := col + 1; r < n; r++ {
			if v := math.Abs(m[r][col]); v > best {
				best = v
				pivot = r
			}
		}
		if best == 0 || math.IsNaN(best) {
			return nil, fmt.Errorf("denseSolve: singular matrix at column %d", col)
		}
		m[col], m[pivot] = m[pivot], m[col]

		for r := col + 1; r < n; r++ {
			factor := m[r][col] / m[col][col]
			if factor == 0 {
				continue
			}
			for c := col; c <= n; c++ {
				m[r][c] -= factor * m[col][c]
			}
		}
	}

	x := make([]float64, n)
	for r := n - 1; r >= 0; r-- {
		s := m[r][n]
		for c := r + 1; c < n; c++ {
			s -= m[r][c] * x[c]
		}
		x[r] = s / m[r][r]
	}
	return x, nil
}

// denseFromCSR expands a CSR matrix into a dense matrix for cross-checks.
func denseFromCSR(a *csrMatrix) [][]float64 {
	m := make([][]float64, a.n)
	for r := range m {
		m[r] = make([]float64, a.n)
	}
	for r := 0; r < a.n; r++ {
		for p := a.row[r]; p < a.row[r+1]; p++ {
			m[r][a.col[p]] = a.val[p]
		}
	}
	return m
}
