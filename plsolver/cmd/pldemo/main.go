// pldemo 演示分段线性求解器：含负权重、线性项、重复断点与增删项。
package main

import (
	"fmt"
	"math/big"

	"plsolver"
)

func rat(p, q int64) *big.Rat { return big.NewRat(p, q) }

func main() {
	// f(x) = x/2 + 2|x+1| - 3|x-1| + |x-2|, x in [-3, 3]
	s := plsolver.NewSolver(rat(-3, 1), rat(3, 1))
	s.AddLinear(rat(1, 2))
	s.AddTerm(rat(2, 1), rat(-1, 1))
	id := s.AddTerm(rat(-3, 1), rat(1, 1))
	s.AddTerm(rat(1, 1), rat(2, 1))
	fmt.Print(s.Solve().Report())

	// 删除负权重项后重新求解
	s.RemoveTerm(id)
	fmt.Println("after removing -3|x-1|:")
	fmt.Print(s.Solve().Report())
}
