package linsolve_test

import (
	"fmt"
	"math/big"

	"linsolve"
)

func Example() {
	// x + y + z = 6 ; y - z = 1  — one free variable (z, index 2).
	A := linsolve.NewSparseMatrix(2, 3)
	A.Set(0, 0, linsolve.NewRat(1, 1))
	A.Set(0, 1, linsolve.NewRat(1, 1))
	A.Set(0, 2, linsolve.NewRat(1, 1))
	A.Set(1, 1, linsolve.NewRat(1, 1))
	A.Set(1, 2, linsolve.NewRat(-1, 1))
	b := []*big.Rat{linsolve.NewRat(6, 1), linsolve.NewRat(1, 1)}

	sol, err := linsolve.Solve(A, b)
	if err != nil {
		panic(err)
	}
	fmt.Println(sol.Status, "free vars:", sol.FreeVars)
	fmt.Println("particular:", sol.Point)
	fmt.Println("direction:", sol.Directions[0])
	fmt.Println("check:", linsolve.Check(A, b, sol))
	// Output:
	// Parametric free vars: [2]
	// particular: [5/1 1/1 0/1]
	// direction: [-2/1 1/1 1/1]
	// check: true
}
