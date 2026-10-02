package main

import (
	"fmt"

	"intervalcalc/interval"
)

func main() {
	a := interval.Must(interval.Rat(1, 1), interval.Rat(2, 1))
	b := interval.Must(interval.Rat(3, 1), interval.Rat(4, 1))

	fmt.Println("A =", a)
	fmt.Println("B =", b)
	fmt.Println("A + B =", a.Add(b))
	fmt.Println("A - B =", a.Sub(b))
	fmt.Println("A * B =", a.Mul(b))

	if q, err := a.Div(b); err == nil {
		fmt.Println("A / B =", q)
	}

	s, _ := interval.Must(interval.Rat(2, 1), interval.Rat(2, 1)).Sqrt()
	fmt.Println("sqrt([2,2]) enclosed by", s)

	bad := interval.Must(interval.Rat(-1, 1), interval.Rat(1, 1))
	if _, err := a.Div(bad); err != nil {
		fmt.Println("A / [-1,1] ->", err)
	}
}
