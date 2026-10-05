// Command matdiag solves small dense linear systems with partial pivoting
// and reports numerical diagnostics: residual, componentwise backward
// error, a 1-norm condition estimate, and scale-aware singularity
// classification (exactly singular vs. near-singular, never by a fixed
// absolute threshold). Optional diagonal equilibration solves
// (Dr*A*Dc)*y = Dr*b and maps back via x = Dc*y.
package main

import (
	"fmt"
	"math/big"
)

func hilbert(n int) [][]float64 {
	a := make([][]float64, n)
	for i := range a {
		a[i] = make([]float64, n)
		for j := range a[i] {
			a[i][j] = 1.0 / float64(i+j+1)
		}
	}
	return a
}

func ones(n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = 1
	}
	return x
}

func printExactComparison(x []float64, exact []*big.Rat) {
	if exact == nil || x == nil {
		return
	}
	fmt.Printf("  rel. forward error:  %12.4e  (vs exact rational reference)\n", RelErrorInf(x, exact))
}

func main() {
	fmt.Println("== 1. Row exchange required (zero leading pivot) ==")
	a1 := [][]float64{{0, 2, 1}, {1, -2, -3}, {3, 1, 4}}
	xe1 := []float64{1, 2, 3}
	b1 := ExactMatVec(a1, xe1)
	exact1, _ := ExactSolve(a1, b1)
	r1 := SolveWithDiagnostics(a1, b1, false)
	fmt.Print(r1.String())
	printExactComparison(r1.X, exact1)

	fmt.Println("\n== 2. Extreme scale disparity: unscaled vs diagonally scaled ==")
	a2 := [][]float64{{1, 1e20}, {1, 1}}
	b2 := []float64{1e20, 2} // A*[1,1] rounded to float64
	exact2, _ := ExactSolve(a2, b2)
	fmt.Println("-- unscaled:")
	r2u := SolveWithDiagnostics(a2, b2, false)
	fmt.Print(r2u.String())
	printExactComparison(r2u.X, exact2)
	fmt.Println("-- scaled (Dr*A*Dc)*y = Dr*b, x = Dc*y:")
	r2s := SolveWithDiagnostics(a2, b2, true)
	fmt.Print(r2s.String())
	printExactComparison(r2s.X, exact2)

	fmt.Println("\n== 3. Ill-conditioned (Hilbert 10): tiny residual, large error ==")
	a3 := hilbert(10)
	b3 := ExactMatVec(a3, ones(10)) // exact solution is x = ones
	exact3, _ := ExactSolve(a3, b3)
	r3 := SolveWithDiagnostics(a3, b3, false)
	fmt.Print(r3.String())
	printExactComparison(r3.X, exact3)

	fmt.Println("\n== 4. Exactly singular (rank-deficient rows) ==")
	a4 := [][]float64{{1, 2, 3}, {2, 4, 6}, {1, 1, 1}}
	b4 := []float64{6, 12, 3}
	r4 := SolveWithDiagnostics(a4, b4, false)
	fmt.Print(r4.String())

	fmt.Println("\n== 5. Tiny but perfectly conditioned: 1e-200 * I ==")
	a5 := [][]float64{{1e-200, 0, 0}, {0, 1e-200, 0}, {0, 0, 1e-200}}
	b5 := []float64{1e-200, 1e-200, 1e-200}
	exact5, _ := ExactSolve(a5, b5)
	r5 := SolveWithDiagnostics(a5, b5, false)
	fmt.Print(r5.String())
	printExactComparison(r5.X, exact5)
	fmt.Println("  (a fixed absolute pivot threshold would call this singular; the relative test does not)")
}
