// Demo: isolate every root of a polynomial built from square (and higher)
// factors, then show how an exhausted budget is reported.
package main

import (
	"fmt"
	"math/big"

	"rootfind"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic(s)
	}
	return r
}

func lin(s string) rootfind.Poly {
	return rootfind.NewPoly(new(big.Rat).Neg(rat(s)), big.NewRat(1, 1))
}

func main() {
	// p = (x+1) * (x-1)^2 * (x-3/2)^2 * (x-2)^3
	p := rootfind.PolyFromInts(1)
	for _, f := range []struct {
		r string
		n int
	}{{"-1", 1}, {"1", 2}, {"3/2", 2}, {"2", 3}} {
		p = rootfind.Mul(p, rootfind.Pow(lin(f.r), f.n))
	}
	fmt.Println("p =", p)

	res, err := rootfind.FindRoots(p, rat("-2"), rat("5"), rat("1/1000"), -1)
	if err != nil {
		panic(err)
	}
	fmt.Println("\nroots in [-2, 5], eps = 1/1000:")
	for _, r := range res.Roots {
		fmt.Printf("  mult %d  root in [%s, %s]  (width %s)\n",
			r.Mult, r.Lo.FloatString(9), r.Hi.FloatString(9), r.Width().RatString())
	}
	fmt.Println("complete:", res.Complete())

	// Same problem with a deliberately tiny budget.
	limited, _ := rootfind.FindRoots(p, rat("-2"), rat("5"), rat("1/1000"), 4)
	fmt.Println("\nwith budget = 4 bisections:")
	fmt.Println("  isolated roots:", len(limited.Roots))
	for _, g := range limited.Incomplete {
		fmt.Printf("  unfinished: (%s, %s] still holds %d root(s) of mult %d\n",
			g.Lo.FloatString(4), g.Hi.FloatString(4), g.Roots, g.Mult)
	}
}
