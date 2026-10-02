// Command demo solves a 3-constraint maximization LP with the exact
// rational simplex solver and prints the full pivot history, the optimal
// primal solution, and the dual-price optimality proof.
package main

import (
	"fmt"
	"math/big"

	"simplex"
)

func mustRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func main() {
	//	max  2 x1 + x2
	//	s.t. x1 + x2 <= 3
	//	     x1       <= 5/4
	//	          x2 <= 2
	//	     x1, x2  >= 0
	c := []*big.Rat{mustRat("2"), mustRat("1")}
	A := [][]*big.Rat{
		{mustRat("1"), mustRat("1")},
		{mustRat("1"), mustRat("0")},
		{mustRat("0"), mustRat("1")},
	}
	b := []*big.Rat{mustRat("3"), mustRat("5/4"), mustRat("2")}

	fmt.Println("max  2*x1 + x2")
	fmt.Println("s.t. x1 + x2 <= 3")
	fmt.Println("     x1 <= 5/4")
	fmt.Println("     x2 <= 2,  x1,x2 >= 0")
	fmt.Println()

	res := simplex.Solve(c, A, b)

	fmt.Println("basis exchanges (exact rational pivots):")
	for _, p := range res.Pivots {
		fmt.Printf("  iter %d (phase %d): x%d enters, x%d leaves, pivot = %s\n",
			p.Iteration, p.Phase, p.Enter, p.Leave, p.Value.RatString())
	}
	fmt.Println()
	fmt.Print(res.String())

	// Dual-price proof: strong duality certifies global optimality.
	primal := new(big.Rat)
	for j := range c {
		primal.Add(primal, new(big.Rat).Mul(c[j], res.X[j]))
	}
	dual := new(big.Rat)
	for i := range b {
		dual.Add(dual, new(big.Rat).Mul(b[i], res.Dual[i]))
	}
	fmt.Println("optimality proof (strong duality):")
	fmt.Printf("  primal  c^T x = %s\n", primal.RatString())
	fmt.Printf("  dual    b^T y = %s\n", dual.RatString())
	fmt.Printf("  equal and exact => x* is globally optimal, max = %s\n",
		res.Objective.RatString())
}
