package main

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
)

func main() {
	failed := false

	fmt.Println("== 1. Deterministic: 10 days, known demand, shelf life d, capacity + setup ==")
	demoDeterministic()

	fmt.Println()
	fmt.Println("== 2. Stochastic: scenario tree, non-anticipative policy, rational probabilities ==")
	demoStochastic()

	fmt.Println()
	fmt.Println("== 3. Verification (exhaustive small instances) ==")
	if !runVerifications() {
		failed = true
	}

	if failed {
		os.Exit(1)
	}
}

func demoDeterministic() {
	p := DetParams{
		T:      10,
		Shelf:  3,
		Cap:    6,
		Setup:  12,
		Unit:   2,
		Hold:   1,
		Demand: []int{4, 3, 2, 5, 1, 0, 3, 2, 4, 6}, // total = 30
	}
	cost, plan, ok := solveDeterministic(p)
	if !ok {
		fmt.Println("  infeasible instance")
		return
	}
	fmt.Printf("  params: T=%d shelf=%d cap=%d setup=%d unit=%d hold=%d demand=%v (total %d)\n",
		p.T, p.Shelf, p.Cap, p.Setup, p.Unit, p.Hold, p.Demand, sumInts(p.Demand))
	fmt.Printf("  optimal cost = %d, production plan = %v\n", cost, plan)
	ledger := buildLedger(p, plan)
	printLedger(ledger)
	s, pr, h := 0, 0, 0
	for _, l := range ledger {
		s += l.Setup
		pr += l.ProdCost
		h += l.HoldCost
	}
	fmt.Printf("  cost breakdown: setup=%d production=%d holding=%d total=%d\n", s, pr, h, s+pr+h)
}

func printLedger(ledger []DayLedger) {
	fmt.Println("  batch age ledger (ages in days; consumed indexed by age 0=fresh):")
	for _, l := range ledger {
		fmt.Printf("    day %2d | start %-14s | prod %d | dem %d | used %-18s | expired %d | end %-14s | cost %d\n",
			l.Day, fmtInv(l.StartInv), l.Prod, l.Demand, fmtConsumed(l.Consumed),
			l.Expired, fmtInv(l.EndInv), l.total())
	}
}

func demoStochastic() {
	p := StochParams{
		T:     4,
		Shelf: 2,
		Cap:   6,
		Setup: 10,
		Unit:  2,
		Hold:  1,
		Scenarios: []Scenario{
			{Demand: []int{3, 2, 4, 1}, Num: 1, Den: 2},
			{Demand: []int{3, 2, 1, 5}, Num: 1, Den: 4},
			{Demand: []int{3, 5, 2, 2}, Num: 1, Den: 8},
			{Demand: []int{2, 4, 3, 3}, Num: 1, Den: 8},
		},
	}
	cost, solver, ok := solveStochastic(p)
	if !ok {
		fmt.Println("  infeasible instance")
		return
	}
	fmt.Printf("  params: T=%d shelf=%d cap=%d setup=%d unit=%d hold=%d, %d scenarios\n",
		p.T, p.Shelf, p.Cap, p.Setup, p.Unit, p.Hold, len(p.Scenarios))
	for _, sc := range p.Scenarios {
		fmt.Printf("    scenario demand=%v prob=%d/%d\n", sc.Demand, sc.Num, sc.Den)
	}
	fmt.Printf("  minimal expected cost = %s (~%.4f)\n", cost.RatString(), expectedCostFloat(cost))
	fmt.Println("  optimal policy tree (decision per observed demand history):")
	printPolicyTree(solver)
	fmt.Println("  per-scenario batch age ledgers under the optimal policy:")
	printScenarioLedgers(solver)
}

func printPolicyTree(s *stochSolver) {
	var walk func(n *node, inv []int, depth int)
	walk = func(n *node, inv []int, depth int) {
		x := s.decisionAt(n, inv)
		next, expired, _, _ := simulateDay(inv, x, n.prefix[n.level-1], s.p.Shelf)
		fmt.Printf("    %sday %d history=%-12v prob=%-6s inv=%-12s -> produce %d (expired %d)\n",
			strings.Repeat("  ", depth), n.level, n.prefix, n.prob.RatString(), fmtInv(inv), x, expired)
		for _, c := range n.children {
			walk(c, next, depth+1)
		}
	}
	for _, n := range s.levels[1] {
		walk(n, make([]int, s.p.Shelf-1), 0)
	}
}

func printScenarioLedgers(s *stochSolver) {
	p := s.p
	nodeByPrefix := map[string]*node{}
	for t := 1; t <= p.T; t++ {
		for _, n := range s.levels[t] {
			nodeByPrefix[prefixKey(n.prefix)] = n
		}
	}
	for si := range p.Scenarios {
		sc := &p.Scenarios[si]
		fmt.Printf("    scenario %d demand=%v prob=%s:\n", si+1, sc.Demand, sc.prob.RatString())
		inv := make([]int, p.Shelf-1)
		total := 0
		for t := 1; t <= p.T; t++ {
			n := nodeByPrefix[prefixKey(sc.Demand[:t])]
			x := s.decisionAt(n, inv)
			next, expired, consumed, _ := simulateDay(inv, x, sc.Demand[t-1], p.Shelf)
			c := p.Unit*x + p.Hold*sumInts(next)
			if x > 0 {
				c += p.Setup
			}
			total += c
			fmt.Printf("      day %d | start %-10s | prod %d | dem %d | used %-16s | expired %d | end %-10s | cost %d\n",
				t, fmtInv(inv), x, sc.Demand[t-1], fmtConsumed(consumed), expired, fmtInv(next), c)
			inv = next
		}
		fmt.Printf("      path cost = %d\n", total)
	}
}

func runVerifications() bool {
	rnd := rand.New(rand.NewSource(42))
	okAll := true
	report := func(name string, err error) {
		if err != nil {
			fmt.Printf("  [FAIL] %s: %v\n", name, err)
			okAll = false
		} else {
			fmt.Printf("  [PASS] %s\n", name)
		}
	}

	report("age-vector simulator vs batch-list simulator (2000 random days)",
		crossCheckSimulators(rnd, 2000))

	report("expiry boundary semantics (shelf-life edge cases)", verifyExpiryBoundary())

	n, err := verifyDeterministic(rnd, 300)
	report(fmt.Sprintf("deterministic DP vs exhaustive enumeration (%d feasible instances)", n), err)

	m, err := verifyStochastic(rnd, 60)
	report(fmt.Sprintf("stochastic tree DP vs exhaustive policy enumeration (%d feasible instances)", m), err)

	return okAll
}
