package main

import "math"

// machineEps is the unit roundoff of float64 (2^-52).
const machineEps = 1.0 / (1 << 52)

// LU holds the factorization P*A = L*U computed with partial pivoting,
// together with scale-aware singularity diagnostics.
//
// Singularity is never decided by a fixed absolute threshold: a pivot is
// "tiny" only relative to the infinity norm of the matrix itself, so a
// well-conditioned matrix scaled by 1e-200 is not mistaken for singular.
type LU struct {
	lu   [][]float64 // L (unit diagonal, below) and U (on/above diagonal)
	perm []int       // row i of P*A is row perm[i] of A
	n    int

	NormInf       float64 // ||A||_inf
	Norm1         float64 // ||A||_1
	Singular      bool    // an exactly zero pivot column was found
	NearSingular  bool    // some pivot fell below n*eps*||A||_inf (relative!)
	MinPivotRatio float64 // min |pivot| / (row max of pivot row), scale-invariant
	Growth        float64 // max|U| / max|A|, pivot growth factor
}

// Factorize computes the LU factorization of the square matrix a with
// partial pivoting.
func Factorize(a [][]float64) *LU {
	n := len(a)
	lu := make([][]float64, n)
	rowMax := make([]float64, n)
	f := &LU{lu: lu, perm: make([]int, n), n: n, MinPivotRatio: math.Inf(1)}

	maxAbs := 0.0
	for i := 0; i < n; i++ {
		lu[i] = make([]float64, n)
		rowSum := 0.0
		for j := 0; j < n; j++ {
			v := a[i][j]
			lu[i][j] = v
			av := math.Abs(v)
			rowSum += av
			if av > rowMax[i] {
				rowMax[i] = av
			}
			if av > maxAbs {
				maxAbs = av
			}
		}
		if rowSum > f.NormInf {
			f.NormInf = rowSum
		}
		f.perm[i] = i
	}
	for j := 0; j < n; j++ {
		colSum := 0.0
		for i := 0; i < n; i++ {
			colSum += math.Abs(a[i][j])
		}
		if colSum > f.Norm1 {
			f.Norm1 = colSum
		}
	}

	// Relative pivot tolerance: proportional to the matrix's own scale.
	tol := float64(n) * machineEps * f.NormInf
	maxU := 0.0

	for k := 0; k < n; k++ {
		p, best := k, math.Abs(lu[k][k])
		for i := k + 1; i < n; i++ {
			if v := math.Abs(lu[i][k]); v > best {
				p, best = i, v
			}
		}
		if best == 0 {
			// Entire subcolumn is exactly zero: A is exactly singular
			// (modulo the elimination already performed in exact arithmetic
			// this means rank < n).
			f.Singular = true
			return f
		}
		if p != k {
			lu[k], lu[p] = lu[p], lu[k]
			f.perm[k], f.perm[p] = f.perm[p], f.perm[k]
			rowMax[k], rowMax[p] = rowMax[p], rowMax[k]
		}
		pivot := lu[k][k]
		if math.Abs(pivot) < tol {
			f.NearSingular = true
		}
		if rowMax[k] > 0 {
			if r := math.Abs(pivot) / rowMax[k]; r < f.MinPivotRatio {
				f.MinPivotRatio = r
			}
		}
		if math.Abs(pivot) > maxU {
			maxU = math.Abs(pivot)
		}
		for i := k + 1; i < n; i++ {
			lu[i][k] /= pivot
			for j := k + 1; j < n; j++ {
				lu[i][j] -= lu[i][k] * lu[k][j]
			}
		}
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if v := math.Abs(lu[i][j]); v > maxU {
				maxU = v
			}
		}
	}
	if maxAbs > 0 {
		f.Growth = maxU / maxAbs
	}
	return f
}

// Solve returns x solving A*x = b using the factorization P*A = L*U.
func (f *LU) Solve(b []float64) []float64 {
	n := f.n
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		x[i] = b[f.perm[i]]
	}
	for i := 0; i < n; i++ {
		for j := 0; j < i; j++ {
			x[i] -= f.lu[i][j] * x[j]
		}
	}
	for i := n - 1; i >= 0; i-- {
		for j := i + 1; j < n; j++ {
			x[i] -= f.lu[i][j] * x[j]
		}
		x[i] /= f.lu[i][i]
	}
	return x
}

// SolveTranspose returns x solving A^T*x = b. Since P*A = L*U we have
// A^T = U^T * L^T * P, so we solve U^T u = b, then L^T v = u, then x = P^T v.
func (f *LU) SolveTranspose(b []float64) []float64 {
	n := f.n
	u := make([]float64, n)
	for i := 0; i < n; i++ {
		u[i] = b[i]
		for j := 0; j < i; j++ {
			u[i] -= f.lu[j][i] * u[j]
		}
		u[i] /= f.lu[i][i]
	}
	v := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		v[i] = u[i]
		for j := i + 1; j < n; j++ {
			v[i] -= f.lu[j][i] * v[j]
		}
	}
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		x[f.perm[i]] = v[i]
	}
	return x
}

// CondEst1 estimates the 1-norm condition number ||A||_1 * ||A^-1||_1
// using Hager's algorithm (as popularized by Higham) on the factorization.
func (f *LU) CondEst1() float64 {
	if f.Singular {
		return math.Inf(1)
	}
	n := f.n
	x := make([]float64, n)
	for i := range x {
		x[i] = 1.0 / float64(n)
	}
	est := 0.0
	for iter := 0; iter < 10; iter++ {
		y := f.Solve(x)
		gamma := 0.0
		v := make([]float64, n)
		for i, yi := range y {
			gamma += math.Abs(yi)
			switch {
			case yi > 0:
				v[i] = 1
			case yi < 0:
				v[i] = -1
			}
		}
		z := f.SolveTranspose(v)
		zInf, argmax := 0.0, 0
		zTx := 0.0
		for i, zi := range z {
			zTx += zi * x[i]
			if math.Abs(zi) > zInf {
				zInf, argmax = math.Abs(zi), i
			}
		}
		if gamma > est {
			est = gamma
		}
		if zInf <= zTx {
			break // converged: no better starting column
		}
		for i := range x {
			x[i] = 0
		}
		x[argmax] = 1
	}
	return est * f.Norm1
}
