package main

import (
	"fmt"
	"math/big"

	"plsolver"
)

func rat(n, d int64) *big.Rat { return big.NewRat(n, d) }

func main() {
	// f(x) = -2|x| + |x-2| + |x+2| + x/2 - 1 on [-3, 3].
	s := plsolver.NewSolver(rat(-3, 1), rat(3, 1))
	s.AddAbs(rat(-2, 1), rat(0, 1))
	s.AddAbs(rat(1, 1), rat(2, 1))
	s.AddAbs(rat(1, 1), rat(-2, 1))
	lin := s.AddLinear(rat(1, 2), rat(-1, 1))

	fmt.Println("== with linear term x/2 - 1 ==")
	fmt.Print(s.Solve().Evidence())

	s.Remove(lin)
	fmt.Println("== after removing the linear term ==")
	fmt.Print(s.Solve().Evidence())
}
