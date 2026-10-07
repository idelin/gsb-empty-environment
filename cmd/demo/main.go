// Command demo solves a sample control decision problem, prints the
// optimal policy tree and per-world replays, and cross-checks the result
// against full policy enumeration.
package main

import (
	"fmt"
	"math/big"

	"policytree"
)

func rats(ss ...string) []*big.Rat {
	out := make([]*big.Rat, len(ss))
	for i, s := range ss {
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			panic("bad rational: " + s)
		}
		out[i] = r
	}
	return out
}

func main() {
	// 4 worlds, 3 actions, 3 probes, at most 2 probes before acting.
	p := &policy.Problem{
		NumWorlds:  4,
		NumActions: 3,
		Loss: [][]int{
			{0, 12, 3, 10}, // A0
			{11, 1, 9, 2},  // A1
			{8, 8, 8, 8},   // A2 (safe fallback)
		},
		ActObs: [][]string{
			{"ok", "hot", "ok", "hot"},
			{"ok", "ok", "hot", "ok"},
			{"ok", "ok", "ok", "ok"},
		},
		Probes: []policy.Probe{
			{Name: "scan", Cost: 1, Obs: []string{"low", "low", "high", "high"}},
			{Name: "ping", Cost: 2, Obs: []string{"x", "y", "x", "y"}},
			{Name: "noop", Cost: 0, Obs: []string{"-", "-", "-", "-"}}, // uninformative
		},
		MaxProbes: 2,
		Prob:      rats("1/2", "1/4", "1/8", "1/8"),
	}

	sol, err := p.Solve()
	if err != nil {
		panic(err)
	}
	fmt.Println("== optimal policy tree ==")
	fmt.Print(p.TreeString(sol.Tree))
	fmt.Printf("worst-case total loss: %d\n", sol.Worst)
	fmt.Printf("expected total loss:   %s (~%.4f)\n\n",
		sol.Expected.RatString(), ratFloat(sol.Expected))

	replays, err := p.ReplayAll(sol.Tree)
	if err != nil {
		panic(err)
	}
	fmt.Println("== per-world replay ==")
	fmt.Print(p.ReplayString(replays))

	if err := p.CheckPolicy(sol.Tree); err != nil {
		panic(err)
	}
	fmt.Println("non-clairvoyance check: OK (same observation history -> same action)")

	best, trees, err := p.BruteForce()
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nbrute-force check over %s policy trees: worst=%d expected=%s\n",
		trees.String(), best.Worst, best.Expected.RatString())
	if best.Worst == sol.Worst && best.Expected.Cmp(sol.Expected) == 0 {
		fmt.Println("solver matches exhaustive enumeration")
	} else {
		fmt.Println("MISMATCH between solver and enumeration!")
	}
}

func ratFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}
