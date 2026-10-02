// Command example demonstrates the interval arithmetic engine on the
// acceptance boundaries.
package main

import (
	"fmt"
	"math/big"

	"interval"
)

func main() {
	a := interval.OfInt64(1, 2)
	b := interval.OfInt64(3, 4)

	fmt.Println("A =", a)
	fmt.Println("B =", b)
	fmt.Println("A + B =", a.Add(b))
	fmt.Println("A - B =", a.Sub(b))
	fmt.Println("A * B =", a.Mul(b))

	if q, err := a.Div(b); err != nil {
		fmt.Println("A / B -> error:", err)
	} else {
		fmt.Println("A / B =", q)
	}

	if _, err := a.Div(interval.OfInt64(-1, 1)); err != nil {
		fmt.Println("A / [-1, 1] -> error:", err)
	}

	s, _ := interval.OfInt64(2, 2).Sqrt()
	fmt.Println("sqrt([2, 2]) =", s, "(outward enclosure of sqrt(2))")
	lo2 := new(big.Rat).Mul(s.Lo, s.Lo)
	hi2 := new(big.Rat).Mul(s.Hi, s.Hi)
	fmt.Println("check: Lo^2 =", lo2, "<= 2 <=", hi2, "= Hi^2")

	exact := interval.New(big.NewRat(1, 3), big.NewRat(1, 3))
	sum := interval.OfInt64(0, 0)
	for i := 0; i < 300; i++ {
		sum = sum.Add(exact)
	}
	fmt.Println("300 * [1/3, 1/3] =", sum, "(exact, no float drift)")
}
