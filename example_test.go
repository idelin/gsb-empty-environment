package dectree_test

import (
	"fmt"
	"math/big"

	"dectree"
)

func ExampleSolve() {
	p := &dectree.Problem{
		Candidates: []dectree.Candidate{
			{Name: "alpha", Prob: big.NewRat(1, 2)},
			{Name: "beta", Prob: big.NewRat(1, 4)},
			{Name: "gamma", Prob: big.NewRat(1, 4)},
		},
		Tests: []dectree.Test{
			{Name: "cheap", Cost: 2, Outcome: []string{"x", "y", "y"}},
			{Name: "pricey", Cost: 3, Outcome: []string{"z", "z", "w"}},
			{Name: "scan", Cost: 5, Outcome: []string{"a", "b", "c"}},
		},
		Budget: 10,
	}
	sol, err := dectree.Solve(p)
	if err != nil {
		panic(err)
	}
	if err := dectree.Verify(p, sol); err != nil {
		panic(err)
	}
	fmt.Print(sol)
	// Output:
	// worst=5 expected=7/2
	// test cheap (cost 2)
	//   "x" ->
	//     leaf [alpha]
	//   "y" ->
	//     test pricey (cost 3)
	//       "w" ->
	//         leaf [gamma]
	//       "z" ->
	//         leaf [beta]
}
