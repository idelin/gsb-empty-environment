package main

import (
	"math/big"
	"testing"
)

// bruteForceDet enumerates every integral production plan and returns
// the minimum feasible cost.
func bruteForceDet(p Params, demand []int) (int, bool) {
	best, found := INF, false
	plan := make([]int, p.T)
	var rec func(t int)
	rec = func(t int) {
		if t == p.T {
			_, cost, ok := simulatePlan(p, demand, plan)
			if ok && cost < best {
				best, found = cost, true
			}
			return
		}
		for x := 0; x <= p.Cap; x++ {
			plan[t] = x
			rec(t + 1)
		}
	}
	rec(0)
	return best, found
}

func TestDetMatchesBruteForce(t *testing.T) {
	cases := []struct {
		p      Params
		demand []int
	}{
		{Params{T: 3, Shelf: 1, Cap: 3, Setup: 5, ProdCost: 1, HoldCost: 1}, []int{1, 2, 1}},
		{Params{T: 4, Shelf: 2, Cap: 3, Setup: 4, ProdCost: 1, HoldCost: 1}, []int{2, 0, 3, 1}},
		{Params{T: 4, Shelf: 3, Cap: 2, Setup: 10, ProdCost: 0, HoldCost: 2}, []int{1, 2, 1, 2}},
		{Params{T: 3, Shelf: 2, Cap: 2, Setup: 0, ProdCost: 1, HoldCost: 0}, []int{0, 2, 1}},
		{Params{T: 4, Shelf: 2, Cap: 1, Setup: 3, ProdCost: 2, HoldCost: 1}, []int{1, 0, 1, 0}},
		{Params{T: 4, Shelf: 4, Cap: 3, Setup: 7, ProdCost: 1, HoldCost: 3}, []int{0, 3, 0, 2}},
		{Params{T: 2, Shelf: 1, Cap: 1, Setup: 1, ProdCost: 1, HoldCost: 1}, []int{2, 0}}, // infeasible
	}
	for i, c := range cases {
		s := NewDetSolver(c.p, c.demand)
		cost, plan, ok := s.Solve()
		best, found := bruteForceDet(c.p, c.demand)
		if ok != found {
			t.Fatalf("case %d: solver ok=%v, brute force found=%v", i, ok, found)
		}
		if !ok {
			continue
		}
		if cost != best {
			t.Fatalf("case %d: solver cost %d, brute force %d", i, cost, best)
		}
		// The returned plan must itself achieve the reported cost.
		entries, simCost, simOK := simulatePlan(c.p, c.demand, plan)
		if !simOK || simCost != cost {
			t.Fatalf("case %d: plan %v simulates to cost %d ok=%v, DP says %d", i, plan, simCost, simOK, cost)
		}
		if err := checkLedger(c.p, c.demand, entries); err != nil {
			t.Fatalf("case %d: ledger invariant: %v", i, err)
		}
	}
}

// TestExpiryBoundary pins the shelf-life semantics: a unit produced on
// day p is usable through day p+Shelf-1 and expires at its end.
func TestExpiryBoundary(t *testing.T) {
	// Direct stepDay checks with Shelf=2.
	p := Params{T: 3, Shelf: 2, Cap: 5}
	inv, res := stepDay(p, []int{0}, 1, 0) // produce 1 on day 0
	if !res.OK || res.Expired != 0 || inv[0] != 1 {
		t.Fatalf("day 0: inv=%v res=%+v", inv, res)
	}
	// Day 1 (age 1): still usable.
	inv, res = stepDay(p, inv, 0, 1)
	if !res.OK || res.Expired != 0 {
		t.Fatalf("day 1: unit of age 1 must be usable: res=%+v", res)
	}
	// Day 1 alternative: left unused, it expires at end of day 1.
	_, res = stepDay(p, []int{1}, 0, 0)
	if res.Expired != 1 {
		t.Fatalf("day 1: unit reaching age 2 must expire, res=%+v", res)
	}

	// Instance where the boundary decides the optimum.
	demand := []int{2, 0, 2}
	base := Params{T: 3, Cap: 4, Setup: 100, ProdCost: 0, HoldCost: 1}

	base.Shelf = 2 // day-0 production cannot serve day 2
	cost2, plan2, ok2 := NewDetSolver(base, demand).Solve()
	if !ok2 || cost2 != 200 {
		t.Fatalf("shelf=2: cost=%d plan=%v ok=%v, want 200 (two setups)", cost2, plan2, ok2)
	}

	base.Shelf = 3 // day-0 production can serve day 2
	cost3, plan3, ok3 := NewDetSolver(base, demand).Solve()
	if !ok3 || cost3 != 104 {
		t.Fatalf("shelf=3: cost=%d plan=%v ok=%v, want 104 (one setup + holding)", cost3, plan3, ok3)
	}

	// A wasteful plan must show expiry in the ledger at the right age.
	wp := Params{T: 3, Shelf: 2, Cap: 3, Setup: 1, ProdCost: 1, HoldCost: 1}
	entries, _, ok := simulatePlan(wp, []int{1, 0, 0}, []int{3, 0, 0})
	if !ok {
		t.Fatal("wasteful plan should be feasible")
	}
	foundExpire := false
	for _, e := range entries {
		if e.Kind == "expire" {
			if e.Day != 1 || e.Qty != 2 || e.Age != 2 {
				t.Fatalf("bad expire entry: %v", e)
			}
			foundExpire = true
		}
	}
	if !foundExpire {
		t.Fatal("expected 2 units to expire at end of day 1")
	}
	if err := checkLedger(wp, []int{1, 0, 0}, entries); err != nil {
		t.Fatal(err)
	}
}

// TestLedgerConservation checks produced = consumed + expired + final
// stock on solver-produced ledgers.
func TestLedgerConservation(t *testing.T) {
	p := Params{T: 6, Shelf: 3, Cap: 4, Setup: 8, ProdCost: 1, HoldCost: 1}
	demand := []int{2, 0, 3, 1, 0, 2}
	cost, plan, ok := NewDetSolver(p, demand).Solve()
	if !ok {
		t.Fatal("infeasible")
	}
	entries, simCost, ok := simulatePlan(p, demand, plan)
	if !ok || simCost != cost {
		t.Fatalf("simulation mismatch: %d vs %d", simCost, cost)
	}
	var produced, consumed, expired, stock int
	for _, e := range entries {
		switch e.Kind {
		case "produce":
			produced += e.Qty
		case "consume":
			consumed += e.Qty
		case "expire":
			expired += e.Qty
		case "stock":
			stock = e.Qty // last stock line = final on-hand
		}
	}
	if produced != consumed+expired+stock {
		t.Fatalf("conservation: produced %d != consumed %d + expired %d + final %d",
			produced, consumed, expired, stock)
	}
	total := 0
	for _, d := range demand {
		total += d
	}
	if consumed != total {
		t.Fatalf("consumed %d != total demand %d", consumed, total)
	}
}

// bruteForceStoch enumerates every non-anticipative policy (one
// integral production quantity per tree node) and returns the minimum
// expected cost as an exact rational.
func bruteForceStoch(p Params, root *Node, scens []Scenario) (*big.Rat, bool) {
	nodes := Nodes(root)
	assign := make([]int, len(nodes))
	var best *big.Rat

	// nodeByID maps node ID to index for quick lookup during simulation.
	idx := map[int]int{}
	for i, n := range nodes {
		idx[n.ID] = i
	}

	var rec func(k int)
	rec = func(k int) {
		if k == len(nodes) {
			total := big.NewRat(0, 1)
			for _, sc := range scens {
				cost, ok := simulatePolicy(p, root, sc.Demand, nodes, assign)
				if !ok {
					return // stockout on a path: policy infeasible
				}
				total.Add(total, new(big.Rat).Mul(sc.prob(), big.NewRat(int64(cost), 1)))
			}
			if best == nil || total.Cmp(best) < 0 {
				best = new(big.Rat).Set(total)
			}
			return
		}
		for x := 0; x <= p.Cap; x++ {
			assign[k] = x
			rec(k + 1)
		}
	}
	rec(0)
	if best == nil {
		return nil, false
	}
	return best, true
}

// simulatePolicy runs one scenario under a per-node production
// assignment and returns the path cost.
func simulatePolicy(p Params, root *Node, demand []int, nodes []*Node, assign []int) (int, bool) {
	idx := map[int]int{}
	for i, n := range nodes {
		idx[n.ID] = i
	}
	inv := make([]int, p.Shelf-1)
	cost := 0
	cur := root
	for t := 0; t < p.T; t++ {
		var next *Node
		for _, c := range cur.Children {
			if c.Demand == demand[t] {
				next = c
				break
			}
		}
		if next == nil {
			return 0, false
		}
		x := assign[idx[next.ID]]
		ninv, res := stepDay(p, inv, x, demand[t])
		if !res.OK {
			return 0, false
		}
		cost += res.DayCost
		inv = ninv
		cur = next
	}
	return cost, true
}

func TestStochMatchesBruteForce(t *testing.T) {
	cases := []struct {
		p     Params
		scens []Scenario
	}{
		{Params{T: 2, Shelf: 2, Cap: 2, Setup: 3, ProdCost: 1, HoldCost: 1},
			[]Scenario{
				{1, 2, []int{1, 2}},
				{1, 2, []int{1, 0}},
			}},
		{Params{T: 3, Shelf: 2, Cap: 3, Setup: 5, ProdCost: 1, HoldCost: 2},
			[]Scenario{
				{1, 3, []int{1, 1, 2}},
				{2, 3, []int{1, 2, 0}},
			}},
		{Params{T: 2, Shelf: 2, Cap: 2, Setup: 4, ProdCost: 1, HoldCost: 1},
			[]Scenario{ // day-0 demand differs: root splits immediately
				{1, 4, []int{0, 2}},
				{3, 4, []int{2, 0}},
			}},
		{Params{T: 2, Shelf: 1, Cap: 3, Setup: 2, ProdCost: 2, HoldCost: 1},
			[]Scenario{ // shelf life 1: no carryover at all
				{1, 2, []int{2, 1}},
				{1, 2, []int{2, 3}},
			}},
	}
	for i, c := range cases {
		root, err := BuildTree(c.p, c.scens)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		sv := NewStochSolver(c.p, root)
		exp, ok := sv.Solve()
		if !ok {
			t.Fatalf("case %d: solver reports infeasible", i)
		}
		best, found := bruteForceStoch(c.p, root, c.scens)
		if !found {
			t.Fatalf("case %d: brute force found no feasible policy", i)
		}
		if exp.Cmp(best) != 0 {
			t.Fatalf("case %d: DP expected cost %s, brute force %s", i, exp.RatString(), best.RatString())
		}

		// The DP policy simulated path by path must reproduce the same
		// expected cost and never stock out.
		total := big.NewRat(0, 1)
		for _, sc := range c.scens {
			plan, ok := sv.PathPlan(sc.Demand)
			if !ok {
				t.Fatalf("case %d: no plan for scenario %v", i, sc.Demand)
			}
			entries, cost, ok := simulatePlan(c.p, sc.Demand, plan)
			if !ok {
				t.Fatalf("case %d: stockout on path %v", i, sc.Demand)
			}
			if err := checkLedger(c.p, sc.Demand, entries); err != nil {
				t.Fatalf("case %d: ledger invariant: %v", i, err)
			}
			total.Add(total, new(big.Rat).Mul(sc.prob(), big.NewRat(int64(cost), 1)))
		}
		if total.Cmp(exp) != 0 {
			t.Fatalf("case %d: simulated expected cost %s != DP %s", i, total.RatString(), exp.RatString())
		}
	}
}

// TestNonAnticipativity verifies that scenarios with identical observed
// history receive identical production decisions, and that the policy
// assigns exactly one decision per tree node.
func TestNonAnticipativity(t *testing.T) {
	p := Params{T: 3, Shelf: 2, Cap: 3, Setup: 5, ProdCost: 1, HoldCost: 1}
	scens := []Scenario{
		{1, 3, []int{1, 2, 0}},
		{1, 3, []int{1, 2, 3}},
		{1, 3, []int{1, 0, 0}},
	}
	root, err := BuildTree(p, scens)
	if err != nil {
		t.Fatal(err)
	}
	sv := NewStochSolver(p, root)
	if _, ok := sv.Solve(); !ok {
		t.Fatal("infeasible")
	}
	plans := make([][]int, len(scens))
	for i, sc := range scens {
		plan, ok := sv.PathPlan(sc.Demand)
		if !ok {
			t.Fatalf("no plan for scenario %d", i)
		}
		plans[i] = plan
	}
	// All scenarios share day-0 demand 1: day-0 production must agree.
	if plans[0][0] != plans[1][0] || plans[1][0] != plans[2][0] {
		t.Fatalf("day-0 decisions differ despite identical history: %v", plans)
	}
	// Scenarios 0 and 1 share the history through day 1.
	if plans[0][1] != plans[1][1] {
		t.Fatalf("day-1 decisions differ for shared history: %v vs %v", plans[0], plans[1])
	}
	// Scenario 2 diverged at day 1; its day-1 decision may differ, but
	// day-2 decisions of scenarios 0 and 1 (different day-2 demand) are
	// taken at different nodes and may differ.
	t.Logf("plans: %v", plans)

	// Structurally: scenarios sharing a prefix must traverse the same
	// nodes, hence the same decision variables.
	n0 := nodeFor(root, scens[0].Demand[:2])
	n1 := nodeFor(root, scens[1].Demand[:2])
	if n0 != n1 {
		t.Fatal("shared prefix did not map to a shared node")
	}
	if nodeFor(root, scens[0].Demand[:3]) == nodeFor(root, scens[1].Demand[:3]) {
		t.Fatal("different day-2 demands must map to different nodes")
	}
}

func nodeFor(root *Node, prefix []int) *Node {
	cur := root
	for _, d := range prefix {
		var next *Node
		for _, c := range cur.Children {
			if c.Demand == d {
				next = c
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}
