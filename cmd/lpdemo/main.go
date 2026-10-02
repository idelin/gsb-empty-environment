// Command lpdemo solves a sample LP exactly with the rational simplex
// solver and prints the optimal primal solution, the dual prices and the
// strong-duality proof, plus unbounded and infeasible examples.
package main

import (
	"fmt"
	"math/big"

	simplex "ratsimplex"
)

func dot(a, b []*big.Rat) *big.Rat {
	sum := new(big.Rat)
	for i := range a {
		sum.Add(sum, new(big.Rat).Mul(a[i], b[i]))
	}
	return sum
}

func main() {
	// max 2x + 3y
	// s.t. x +  y <= 3/2
	//      x + 2y <= 11/4
	//      3x +  y <= 3
	//      x, y >= 0
	lp := &simplex.LP{
		A: [][]*big.Rat{
			{simplex.Rat("1"), simplex.Rat("1")},
			{simplex.Rat("1"), simplex.Rat("2")},
			{simplex.Rat("3"), simplex.Rat("1")},
		},
		B: []*big.Rat{simplex.Rat("3/2"), simplex.Rat("11/4"), simplex.Rat("3")},
		C: []*big.Rat{simplex.Rat("2"), simplex.Rat("3")},
	}

	fmt.Println("=== maximize 2x + 3y subject to 3 inequality constraints ===")
	res, err := simplex.Solve(lp)
	if err != nil {
		panic(err)
	}
	fmt.Println(res)
	fmt.Printf("strong duality: C^T x = %s = B^T y = %s\n",
		dot(lp.C, res.Primal), dot(lp.B, res.Dual))

	fmt.Println("\n=== unbounded example: max x + y, x - y <= 1 ===")
	unbounded := &simplex.LP{
		A: [][]*big.Rat{{simplex.Rat("1"), simplex.Rat("-1")}},
		B: []*big.Rat{simplex.Rat("1")},
		C: []*big.Rat{simplex.Rat("1"), simplex.Rat("1")},
	}
	res2, _ := simplex.Solve(unbounded)
	fmt.Println(res2)

	fmt.Println("\n=== infeasible example: x + y <= 1, x + y >= 2 ===")
	infeasible := &simplex.LP{
		A: [][]*big.Rat{
			{simplex.Rat("1"), simplex.Rat("1")},
			{simplex.Rat("-1"), simplex.Rat("-1")},
		},
		B: []*big.Rat{simplex.Rat("1"), simplex.Rat("-2")},
		C: []*big.Rat{simplex.Rat("1"), simplex.Rat("1")},
	}
	res3, _ := simplex.Solve(infeasible)
	fmt.Println(res3)
}
