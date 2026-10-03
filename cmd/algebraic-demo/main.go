package main

import (
	"fmt"

	"example.com/algebraic"
)

func relation(compare int) string {
	switch compare {
	case -1:
		return "<"
	case 0:
		return "="
	case 1:
		return ">"
	default:
		panic("invalid comparison result")
	}
}

func printComparison(name string, left, right *algebraic.Number) {
	compare := left.Compare(right)
	fmt.Printf("%s: %s %s %s\n", name, left, relation(compare), right)
	fmt.Printf("  left minimal polynomial:  %s\n", left.MinimalPolynomial())
	fmt.Printf("  right minimal polynomial: %s\n", right.MinimalPolynomial())
	fmt.Printf("  left isolating interval:  %s\n", left.IsolatingInterval())
	fmt.Printf("  right isolating interval: %s\n", right.IsolatingInterval())
}

func main() {
	sqrt2 := algebraic.MustSqrt(algebraic.Int(2))
	sqrt3 := algebraic.MustSqrt(algebraic.Int(3))
	sqrt6 := algebraic.MustSqrt(algebraic.Int(6))
	sqrt10 := algebraic.MustSqrt(algebraic.Int(10))

	sum := algebraic.Add(sqrt2, sqrt3)
	nested := algebraic.MustSqrt(algebraic.Add(
		algebraic.Int(5),
		algebraic.Mul(algebraic.Int(2), sqrt6),
	))

	printComparison("nested radical identity", sum, nested)
	fmt.Println()
	printComparison("strict radical comparison", sum, sqrt10)
}
