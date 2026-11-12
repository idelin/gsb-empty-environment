package taskpkg

import (
	"fmt"
	"math"
	"sort"
)

// csrMatrix is a square matrix stored in compressed sparse rows.
type csrMatrix struct {
	n   int
	row []int // length n+1, start index of each row in col/val
	col []int // column index of every nonzero
	val []float64
}

// buildLaplacianReduced builds the symmetric positive definite linear system
// for the interior vertices of a disk mesh with uniform Laplacian weights:
//
//	deg(i) * u_i - sum_{interior j adjacent i} u_j =
//	    sum_{boundary j adjacent i} u_j.
//
// interior maps an original vertex id to its compact row index, or -1.
// Boundary vertex coordinates are already stored in uv.
func buildLaplacianReduced(adj [][]int, interior []int, uv [][2]float64) (*csrMatrix, [][2]float64) {
	k := 0
	for _, r := range interior {
		if r >= 0 {
			k++
		}
	}
	a := &csrMatrix{n: k}
	rhs := make([][2]float64, k)

	type entry struct {
		col int
		val float64
	}
	rows := make([][]entry, k)

	interiorRows := make([]int, k)
	for vi, r := range interior {
		if r >= 0 {
			interiorRows[r] = vi
		}
	}
	for r, vi := range interiorRows {
		dense := make(map[int]float64)
		for _, vj := range adj[vi] {
			cj := interior[vj]
			if cj < 0 {
				// Boundary neighbor: its coordinate is pinned to the unit circle
				// and its contribution moves to the right-hand side.
				rhs[r][0] += uv[vj][0]
				rhs[r][1] += uv[vj][1]
			} else {
				dense[cj] -= 1
			}
		}
		dense[r] += float64(len(adj[vi]))

		cols := make([]int, 0, len(dense))
		for c := range dense {
			cols = append(cols, c)
		}
		sort.Ints(cols)
		for _, c := range cols {
			rows[r] = append(rows[r], entry{c, dense[c]})
		}
	}

	a.row = make([]int, k+1)
	for r := 0; r < k; r++ {
		a.row[r+1] = a.row[r] + len(rows[r])
	}
	a.col = make([]int, a.row[k])
	a.val = make([]float64, a.row[k])
	off := 0
	for _, entries := range rows {
		for _, e := range entries {
			a.col[off] = e.col
			a.val[off] = e.val
			off++
		}
	}
	return a, rhs
}

func (a *csrMatrix) mul(x []float64) []float64 {
	y := make([]float64, a.n)
	for r := 0; r < a.n; r++ {
		s := 0.0
		for p := a.row[r]; p < a.row[r+1]; p++ {
			s += a.val[p] * x[a.col[p]]
		}
		y[r] = s
	}
	return y
}

func (a *csrMatrix) diagonal() []float64 {
	d := make([]float64, a.n)
	for r := 0; r < a.n; r++ {
		for p := a.row[r]; p < a.row[r+1]; p++ {
			if a.col[p] == r {
				d[r] = a.val[p]
				break
			}
		}
	}
	return d
}

// pcg solves a symmetric positive definite system A x = b with a Jacobi
// (diagonal) preconditioner. It returns x, the number of matrix-vector
// products and the infinity norm of the final residual.
//
// tol is relative to ||b||. A zero right-hand side converges immediately
// because the unique SPD solution is x = 0.
func (a *csrMatrix) pcg(b []float64, tol float64, maxIter int) (x []float64, iterations int, residualNorm float64, err error) {
	n := a.n
	x = make([]float64, n)
	if n == 0 {
		return x, 0, 0, nil
	}

	diag := a.diagonal()
	minv := make([]float64, n)
	for i, d := range diag {
		if d <= 0 || math.IsNaN(d) || math.IsInf(d, 0) {
			return nil, 0, 0, fmt.Errorf("%w: non-positive diagonal entry %.3e at row %d", ErrSolver, d, i)
		}
		minv[i] = 1 / d
	}

	bnorm := infNorm(b)
	if bnorm == 0 {
		return x, 0, 0, nil
	}

	r := append([]float64(nil), b...) // r = b - A*0
	z := scale(minv, r)
	p := append([]float64(nil), z...)
	rz := dot(r, z)
	if rz <= 0 {
		return nil, 0, infNorm(r), fmt.Errorf("%w: preconditioned residual is not positive", ErrSolver)
	}

	for iterations = 1; iterations <= maxIter; iterations++ {
		ap := a.mul(p)
		pap := dot(p, ap)
		if pap <= 0 || math.IsNaN(pap) {
			return nil, iterations, infNorm(r), fmt.Errorf("%w: non-positive curvature %.3e", ErrSolver, pap)
		}
		alpha := rz / pap
		for i := 0; i < n; i++ {
			x[i] += alpha * p[i]
			r[i] -= alpha * ap[i]
		}
		residualNorm = infNorm(r)
		if residualNorm <= tol*bnorm {
			return x, iterations, residualNorm, nil
		}
		z = scale(minv, r)
		rzNew := dot(r, z)
		if rzNew <= 0 {
			return nil, iterations, residualNorm, fmt.Errorf("%w: preconditioned residual is not positive", ErrSolver)
		}
		beta := rzNew / rz
		for i := 0; i < n; i++ {
			p[i] = z[i] + beta*p[i]
		}
		rz = rzNew
	}
	return nil, maxIter, residualNorm, fmt.Errorf("%w: did not reach relative tolerance %g in %d iterations", ErrSolver, tol, maxIter)
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

func scale(a, x []float64) []float64 {
	y := make([]float64, len(x))
	for i := range x {
		y[i] = a[i] * x[i]
	}
	return y
}

func infNorm(x []float64) float64 {
	m := 0.0
	for _, v := range x {
		if av := math.Abs(v); av > m {
			m = av
		}
	}
	return m
}
