// Command demo runs the 10-variable acceptance scenario with both a
// chronological backtracking solver and the CBJ solver, showing the
// conflict-directed backjump from step 8 to step 2.
package main

import (
	"fmt"

	"csp"
)

func main() {
	p := csp.NewBenchmarkProblem()

	fmt.Println("=== 10-variable CSP: conflict at step 8 is caused only by step 2 ===")
	fmt.Println("domains: X1..X7,X9,X10 in {1,2,3}; X8 = {1}")
	fmt.Println("constraints: X2 != X8; benign chain X3!=X4, X4!=X5, X5!=X6, X6!=X7")
	fmt.Println()

	naive := csp.NewNaiveSolver(csp.NewBenchmarkProblem())
	solN, _ := naive.Solve()
	fmt.Printf("chronological backtracking: %d value attempts, solution %v\n",
		naive.Assignments, solN)

	cbj := csp.NewCBJSolver(p)
	solC, ok := cbj.Solve()
	if !ok {
		fmt.Println("CBJ: no solution")
		return
	}
	for _, bj := range cbj.Stats.Backjumps {
		fmt.Printf("CBJ backjump: %s\n", bj)
	}
	fmt.Printf("CBJ: %d value attempts, solution %v\n", cbj.Stats.Assignments, solC)
	fmt.Printf("valid solution: %v\n", p.Validate(solC))
	fmt.Printf("pruning factor: %.1fx fewer value attempts\n",
		float64(naive.Assignments)/float64(cbj.Stats.Assignments))
}
