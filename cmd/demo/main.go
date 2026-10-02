package main

import (
	"fmt"

	"algebraic"
)

func describe(name string, n algebraic.Num) {
	t := n.Tighten(50)
	lo, hi := t.Interval()
	flo, _ := lo.Float64()
	fhi, _ := hi.Float64()
	fmt.Printf("%s\n", name)
	fmt.Printf("  minimal polynomial : %s\n", n.MinimalPoly())
	fmt.Printf("  isolating interval : [%.12f, %.12f]  (width < 2^-50)\n", flo, fhi)
	fmt.Printf("  decimal (display)  : ~%s\n", n.Approx(12))
}

func verdict(c int, x, y string) string {
	switch {
	case c < 0:
		return fmt.Sprintf("-1  =>  %s < %s", x, y)
	case c > 0:
		return fmt.Sprintf("+1  =>  %s > %s", x, y)
	default:
		return fmt.Sprintf(" 0  =>  %s == %s (proved exactly)", x, y)
	}
}

func main() {
	sqrt2 := algebraic.Sqrt(algebraic.Rat(2))
	sqrt3 := algebraic.Sqrt(algebraic.Rat(3))

	a := algebraic.Add(sqrt2, sqrt3)
	b := algebraic.Sqrt(algebraic.Rat(10))
	c := algebraic.Sqrt(algebraic.Add(
		algebraic.Rat(5),
		algebraic.Mul(algebraic.Rat(2), algebraic.Sqrt(algebraic.Rat(6))),
	))

	describe("a = sqrt(2) + sqrt(3)", a)
	describe("b = sqrt(10)", b)
	describe("c = sqrt(5 + 2*sqrt(6))", c)

	fmt.Println()
	fmt.Printf("compare(a, b) = %s\n", verdict(algebraic.Compare(a, b), "a", "b"))
	fmt.Printf("compare(a, c) = %s\n", verdict(algebraic.Compare(a, c), "a", "c"))
}
