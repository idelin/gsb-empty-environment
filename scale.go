package main

import "math"

// Scaling describes diagonal row/column equilibration of the system
// A*x = b. All scale factors are powers of two, so applying them is exact
// in binary floating point (no rounding is introduced by scaling itself).
//
// The scaled system is
//
//	(Dr * A * Dc) * y = Dr * b
//
// and the solution in the ORIGINAL variables is recovered by
//
//	x = Dc * y
//
// i.e. x[i] = Col[i] * y[i]. The residual and backward error must always
// be evaluated against the original (unscaled) A and b.
type Scaling struct {
	Row []float64 // diagonal of Dr
	Col []float64 // diagonal of Dc
}

// pow2BelowOne returns the power of two s such that s*v lies in [0.5, 1).
func pow2Scale(v float64) float64 {
	if v == 0 {
		return 1
	}
	_, e := math.Frexp(v) // v = f * 2^e, f in [0.5, 1)
	return math.Ldexp(1, -e)
}

// Equilibrate computes row and column scalings for A*x = b and returns the
// scaled system. Rows are scaled to unit max-magnitude first, then columns
// of the row-scaled matrix.
func Equilibrate(a [][]float64, b []float64) (sa [][]float64, sb []float64, sc Scaling) {
	n := len(a)
	sc.Row = make([]float64, n)
	sc.Col = make([]float64, n)

	sa = make([][]float64, n)
	sb = make([]float64, n)
	for i := 0; i < n; i++ {
		rowMax := 0.0
		for j := 0; j < n; j++ {
			if v := math.Abs(a[i][j]); v > rowMax {
				rowMax = v
			}
		}
		sc.Row[i] = pow2Scale(rowMax)
		sa[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			sa[i][j] = sc.Row[i] * a[i][j]
		}
		sb[i] = sc.Row[i] * b[i]
	}
	for j := 0; j < n; j++ {
		colMax := 0.0
		for i := 0; i < n; i++ {
			if v := math.Abs(sa[i][j]); v > colMax {
				colMax = v
			}
		}
		sc.Col[j] = pow2Scale(colMax)
		for i := 0; i < n; i++ {
			sa[i][j] *= sc.Col[j]
		}
	}
	return sa, sb, sc
}

// MapBack maps a solution y of the scaled system back to the original
// variables: x = Dc * y.
func (sc Scaling) MapBack(y []float64) []float64 {
	x := make([]float64, len(y))
	for i := range y {
		x[i] = sc.Col[i] * y[i]
	}
	return x
}
