package suppress_test

import (
	"fmt"

	"example.org/netsec/q0437/suppress"
)

func ExampleSolve() {
	lower := [][]int{{0, 0}, {0, 0}}
	upper := [][]int{{9, 9}, {9, 9}}
	problem := &suppress.Problem{
		Lower:     lower,
		Upper:     upper,
		RowSums:   []int{4, 6},
		ColSums:   []int{3, 7},
		Sensitive: []suppress.Cell{{Row: 0, Col: 0}},
	}
	plan, err := suppress.Solve(problem)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("extra:", plan.Extra)
	fmt.Println("cost:", plan.TotalCost)
	witness := plan.Witnesses[0]
	fmt.Println("table A:", witness.A)
	fmt.Println("table B:", witness.B)

	if err := suppress.Verify(problem, plan, 0); err != nil {
		fmt.Println("verify error:", err)
		return
	}
	fmt.Println("verified")
	// Output:
	// extra: [{0 1} {1 0} {1 1}]
	// cost: 3
	// table A: [[2 2] [1 5]]
	// table B: [[3 1] [0 6]]
	// verified
}
