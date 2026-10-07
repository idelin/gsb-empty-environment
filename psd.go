package psdcomp

import (
	"fmt"
	"math/big"
	"sort"
)

// MinorReport records one principal minor: the determinant of the
// principal submatrix on Indices.
type MinorReport struct {
	Indices []int
	Det     *big.Rat
}

func (r MinorReport) String() string {
	return fmt.Sprintf("det(A%v) = %s", r.Indices, r.Det.RatString())
}

// IndexSubsets returns all nonempty subsets of {0,...,n-1} in increasing
// order of size, each subset sorted ascending.
func IndexSubsets(n int) [][]int {
	var out [][]int
	for mask := 1; mask < (1 << n); mask++ {
		var s []int
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				s = append(s, i)
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(a, b int) bool {
		if len(out[a]) != len(out[b]) {
			return len(out[a]) < len(out[b])
		}
		for k := range out[a] {
			if out[a][k] != out[b][k] {
				return out[a][k] < out[b][k]
			}
		}
		return false
	})
	return out
}

// PrincipalMinors computes every principal minor of m using the supplied
// determinant function (DetBareiss or DetGauss). For an n x n matrix this
// yields 2^n - 1 reports (15 for n = 4).
func PrincipalMinors(m *Matrix, det func(*Matrix) *big.Rat) []MinorReport {
	subsets := IndexSubsets(m.N)
	reports := make([]MinorReport, 0, len(subsets))
	for _, s := range subsets {
		reports = append(reports, MinorReport{
			Indices: s,
			Det:     det(m.Submatrix(s)),
		})
	}
	return reports
}

// IsPSDMinors applies the exact Sylvester criterion for semidefiniteness:
// a symmetric matrix is PSD iff EVERY principal minor is nonnegative.
// Note that nonnegativity of the diagonal and the full determinant alone
// is not sufficient; all 2^n - 1 principal minors are checked here.
func IsPSDMinors(m *Matrix) bool {
	for _, r := range PrincipalMinors(m, DetBareiss) {
		if r.Det.Sign() < 0 {
			return false
		}
	}
	return true
}

// IsPSDElimination is an independent exact PSD test based on rational
// LDL-style symmetric elimination (no determinants involved):
//
//   - scan the remaining diagonal for a nonzero pivot;
//   - a negative pivot proves indefiniteness;
//   - if the whole remaining diagonal is zero but some off-diagonal entry
//     is nonzero, the matrix is indefinite (it contains a 2x2 principal
//     submatrix [[0, x], [x, d]] with determinant -x^2 < 0);
//   - otherwise eliminate the pivot row/column symmetrically and recurse
//     on the Schur complement.
//
// All arithmetic is exact over big.Rat.
func IsPSDElimination(m *Matrix) bool {
	n := m.N
	a := make([][]*big.Rat, n)
	for i := range a {
		a[i] = make([]*big.Rat, n)
		for j := range a[i] {
			a[i][j] = new(big.Rat).Set(m.A[i][j])
		}
	}
	for k := 0; k < n; k++ {
		// Symmetric pivoting: bring a nonzero diagonal entry to (k,k).
		p := -1
		for r := k; r < n; r++ {
			if a[r][r].Sign() != 0 {
				p = r
				break
			}
		}
		if p < 0 {
			// Remaining diagonal is entirely zero. Any nonzero
			// off-diagonal entry implies indefiniteness.
			for i := k; i < n; i++ {
				for j := i + 1; j < n; j++ {
					if a[i][j].Sign() != 0 {
						return false
					}
				}
			}
			return true // remaining block is the zero matrix
		}
		if p != k {
			a[k], a[p] = a[p], a[k]
			for i := 0; i < n; i++ {
				a[i][k], a[i][p] = a[i][p], a[i][k]
			}
		}
		if a[k][k].Sign() < 0 {
			return false
		}
		pivot := new(big.Rat).Set(a[k][k])
		for i := k + 1; i < n; i++ {
			if a[i][k].Sign() == 0 {
				continue
			}
			f := new(big.Rat).Quo(a[i][k], pivot)
			for j := k + 1; j < n; j++ {
				t := new(big.Rat).Mul(f, a[k][j])
				a[i][j].Sub(a[i][j], t)
			}
			a[i][k] = new(big.Rat)
		}
	}
	return true
}
