package ils_test

import (
	"fmt"
	"math/big"

	"ils/ils"
)

func ExampleSolve() {
	//  x + y + z = 6
	// 2x - y   = 3
	A := ils.MatrixFromInts([][]int64{
		{1, 1, 1},
		{2, -1, 0},
	})
	b := []*big.Int{big.NewInt(6), big.NewInt(3)}

	sol, _, _ := ils.Solve(A, b)
	fmt.Println("x0 =", sol.X0)
	fmt.Println("kernel =", sol.Kernel)
	fmt.Println("verify =", ils.VerifySolution(A, b, sol))

	// Output:
	// x0 = [6 9 -9]
	// kernel = [[-1 -2 3]]
	// verify = <nil>
}

func ExampleSolve_unsatisfiable() {
	// 2x = 1 has no integer solution.
	A := ils.MatrixFromInts([][]int64{{2}})
	b := []*big.Int{big.NewInt(1)}

	sol, cert, _ := ils.Solve(A, b)
	fmt.Println("solution?", sol != nil)
	fmt.Println("y =", cert.Num, "/", cert.Den)
	fmt.Println("verify =", ils.VerifyUnsat(A, b, cert))

	// Output:
	// solution? false
	// y = [1] / 2
	// verify = <nil>
}
