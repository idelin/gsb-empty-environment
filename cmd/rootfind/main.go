// Command rootfind demonstrates bounded isolation of all real roots of a
// rational polynomial in a closed interval, with exact multiplicities,
// and contrasts it with point sampling, which cannot certify completeness.
package main

import (
	"fmt"
	"math/big"

	"rootfind"
)

func lin(num, den int64) rootfind.Poly { return rootfind.Linear(rootfind.Rat(num, den)) }

func prod(ps ...rootfind.Poly) rootfind.Poly {
	r := rootfind.Const(rootfind.Rat(1, 1))
	for _, p := range ps {
		r = r.Mul(p)
	}
	return r
}

func main() {
	// p has a double root at 1/3, a double root at 2/7 (no sign change
	// anywhere near them), a simple root at 2 and a triple root at -1/2.
	p := prod(lin(1, 3).Pow(2), lin(2, 7).Pow(2), lin(2, 1), lin(-1, 2).Pow(3))
	a, b := rootfind.Rat(-1, 1), rootfind.Rat(3, 1)
	prec := rootfind.Rat(1, 1000)

	fmt.Printf("p(x) = %s\n", p)
	fmt.Printf("isolating all roots of p in [%s, %s], precision %s\n\n", a, b, prec)

	res := rootfind.Isolate(p, a, b, prec, -1)
	for _, r := range res.Roots {
		fmt.Println(" ", r)
	}
	fmt.Printf("complete: %v, bisections: %d\n\n", res.Complete(), res.Steps)

	// Why sampling is not a completeness proof: restrict to the even
	// part q = (x-1/3)^2 (x-2/7)^2. q >= 0 everywhere, so no sample
	// ever sees a sign change, and on a uniform grid no sample point
	// ever hits 1/3 or 2/7 exactly. Sampling concludes "no roots";
	// Sturm-based isolation finds both.
	q := prod(lin(1, 3).Pow(2), lin(2, 7).Pow(2))
	fmt.Printf("q(x) = %s  (all roots have even multiplicity)\n", q)
	const n = 10000
	minVal := new(big.Rat)
	signChanges := 0
	prev := q.Eval(a)
	for i := 0; i <= n; i++ {
		x := rootfind.Rat(int64(i)*3-1, n) // uniform grid over [-1, 3]
		v := q.Eval(x)
		if i == 0 || v.Cmp(minVal) < 0 {
			minVal.Set(v)
		}
		if i > 0 && v.Sign() != 0 && prev.Sign() != 0 && v.Sign() != prev.Sign() {
			signChanges++
		}
		if v.Sign() != 0 {
			prev = v
		}
	}
	fmt.Printf("sampling q at %d grid points: sign changes = %d, min |q| = %s\n",
		n+1, signChanges, minVal.RatString())
	fmt.Println("=> a sampler reports 'no roots', which is wrong and cannot be")
	fmt.Println("   a completeness proof; Sturm counts are exact instead:")
	resQ := rootfind.Isolate(q, a, b, prec, -1)
	for _, r := range resQ.Roots {
		fmt.Println(" ", r)
	}
	fmt.Printf("complete: %v, bisections: %d\n", resQ.Complete(), resQ.Steps)
}
