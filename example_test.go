package linsolve_test

import (
	"fmt"
	"math/big"

	"linsolve"
)

func Example() {
	sys := &linsolve.System{
		NumVars: 2,
		Equations: []linsolve.Equation{
			{Coeffs: map[int]*big.Rat{0: big.NewRat(2, 1), 1: big.NewRat(1, 1)}, RHS: big.NewRat(5, 1)},
			{Coeffs: map[int]*big.Rat{0: big.NewRat(1, 1), 1: big.NewRat(-1, 1)}, RHS: big.NewRat(1, 1)},
		},
	}
	res, err := linsolve.Solve(sys)
	if err != nil {
		panic(err)
	}
	if err := linsolve.Verify(sys, res); err != nil {
		panic(err)
	}
	fmt.Println(res.Status, res.Solution[0].RatString(), res.Solution[1].RatString())
	// Output: Unique 2 1
}
