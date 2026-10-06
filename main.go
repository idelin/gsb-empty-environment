package main

import (
	"fmt"
	"strings"
)

func main() {
	demoDeterministic()
	fmt.Println()
	demoStochastic()
}

func demoDeterministic() {
	fmt.Println("=== Deterministic: 10 days, shelf life d=3, total demand 24 (<=30) ===")
	p := Params{T: 10, Shelf: 3, Cap: 6, Setup: 20, ProdCost: 2, HoldCost: 1}
	demand := []int{3, 0, 5, 2, 0, 4, 1, 0, 6, 3}
	s := NewDetSolver(p, demand)
	cost, plan, ok := s.Solve()
	if !ok {
		fmt.Println("infeasible")
		return
	}
	fmt.Printf("demand:    %v\n", demand)
	fmt.Printf("plan:      %v\n", plan)
	fmt.Printf("min cost:  %d   (dp states: %d)\n", cost, s.states)
	fmt.Println("--- batch age ledger ---")
	entries, simCost, ok := simulatePlan(p, demand, plan)
	if !ok {
		fmt.Println("ledger simulation failed")
		return
	}
	for _, e := range entries {
		fmt.Println(e)
	}
	fmt.Printf("ledger cost check: %d (matches DP: %v)\n", simCost, simCost == cost)
	if err := checkLedger(p, demand, entries); err != nil {
		fmt.Println("LEDGER INVARIANT VIOLATION:", err)
	}
}

func demoStochastic() {
	fmt.Println("=== Stochastic: 4 days, shelf life d=2, 6 scenarios, rational probabilities ===")
	p := Params{T: 4, Shelf: 2, Cap: 4, Setup: 10, ProdCost: 1, HoldCost: 1}
	scens := []Scenario{
		{Num: 1, Den: 3, Demand: []int{2, 1, 3, 0}},
		{Num: 1, Den: 6, Demand: []int{2, 1, 0, 3}},
		{Num: 1, Den: 6, Demand: []int{2, 3, 1, 1}},
		{Num: 1, Den: 12, Demand: []int{1, 2, 2, 2}},
		{Num: 1, Den: 12, Demand: []int{1, 2, 3, 0}},
		{Num: 1, Den: 6, Demand: []int{2, 3, 3, 1}},
	}
	root, err := BuildTree(p, scens)
	if err != nil {
		fmt.Println("tree error:", err)
		return
	}
	sv := NewStochSolver(p, root)
	exp, ok := sv.Solve()
	if !ok {
		fmt.Println("infeasible: some scenario cannot be served without stockout")
		return
	}
	fmt.Printf("min expected cost: %s (~%.4f)   (dp states: %d)\n",
		exp.RatString(), ratFloat(exp), sv.states)

	fmt.Println("--- policy tree (one production decision per node: non-anticipative) ---")
	printPolicyTree(sv, root, make([]int, p.Shelf-1), 0)

	fmt.Println("--- per-scenario batch age ledgers ---")
	for i, sc := range scens {
		plan, ok := sv.PathPlan(sc.Demand)
		if !ok {
			fmt.Printf("scenario %d: no plan\n", i)
			return
		}
		entries, cost, ok := simulatePlan(p, sc.Demand, plan)
		if !ok {
			fmt.Printf("scenario %d: STOCKOUT\n", i)
			return
		}
		fmt.Printf("scenario %d  p=%s  demand=%v  plan=%v  cost=%d\n",
			i, sc.prob().RatString(), sc.Demand, plan, cost)
		for _, e := range entries {
			fmt.Println("   ", e)
		}
		if err := checkLedger(p, sc.Demand, entries); err != nil {
			fmt.Println("LEDGER INVARIANT VIOLATION:", err)
		}
	}
}

func printPolicyTree(sv *StochSolver, n *Node, inv []int, depth int) {
	indent := strings.Repeat("  ", depth)
	if n.Day >= 0 {
		x := sv.Decision(n, inv)
		fmt.Printf("%s%s -> produce %d\n", indent, n, x)
		next, _ := stepDay(sv.P, inv, x, n.Demand)
		inv = next
	}
	for _, c := range n.Children {
		printPolicyTree(sv, c, inv, depth+1)
	}
}

func ratFloat(r interface{ FloatString(prec int) string }) float64 {
	var f float64
	fmt.Sscanf(r.FloatString(6), "%g", &f)
	return f
}
