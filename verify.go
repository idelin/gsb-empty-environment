package main

import (
	"fmt"
	"math/big"
	"math/rand"
)

// Verification helpers: cross-check solvers against independent brute-force
// enumeration and independent simulation code.

// crossCheckSimulators compares the age-vector simulator against the
// independent batch-list simulator on random single-day transitions.
func crossCheckSimulators(rnd *rand.Rand, trials int) error {
	for i := 0; i < trials; i++ {
		shelf := 1 + rnd.Intn(4)
		inv := make([]int, shelf-1)
		batches := []Batch{}
		for a := 1; a < shelf; a++ {
			inv[a-1] = rnd.Intn(5)
			if inv[a-1] > 0 {
				batches = append(batches, Batch{Age: a, Qty: inv[a-1]})
			}
		}
		x := rnd.Intn(6)
		demand := rnd.Intn(9)
		next1, exp1, con1, ok1 := simulateDay(inv, x, demand, shelf)
		next2, exp2, ok2 := simulateDayBatches(batches, x, demand, shelf)
		if ok1 != ok2 {
			return fmt.Errorf("simulator mismatch on feasibility: inv=%v x=%d d=%d shelf=%d", inv, x, demand, shelf)
		}
		if !ok1 {
			continue
		}
		if exp1 != exp2 {
			return fmt.Errorf("simulator mismatch on expired: %d vs %d (inv=%v x=%d d=%d shelf=%d)", exp1, exp2, inv, x, demand, shelf)
		}
		if invKey(next1) != invKey(batchesToInv(next2, shelf)) {
			return fmt.Errorf("simulator mismatch on next inventory: %v vs %v (inv=%v x=%d d=%d shelf=%d)", next1, next2, inv, x, demand, shelf)
		}
		if sumInts(con1) != demand {
			return fmt.Errorf("consumed total %d != demand %d", sumInts(con1), demand)
		}
	}
	return nil
}

// verifyDeterministic compares the DP against exhaustive enumeration of all
// production sequences on random small instances.
func verifyDeterministic(rnd *rand.Rand, trials int) (int, error) {
	checked := 0
	for i := 0; i < trials; i++ {
		p := DetParams{
			T:     2 + rnd.Intn(4),
			Shelf: 1 + rnd.Intn(3),
			Cap:   1 + rnd.Intn(4),
			Setup: rnd.Intn(9),
			Unit:  1 + rnd.Intn(3),
			Hold:  rnd.Intn(3),
		}
		p.Demand = make([]int, p.T)
		for t := range p.Demand {
			p.Demand[t] = rnd.Intn(4)
		}
		dpCost, plan, dpOK := solveDeterministic(p)
		bfCost, bfOK := bruteDeterministic(p)
		if dpOK != bfOK {
			return checked, fmt.Errorf("feasibility mismatch: dp=%v brute=%v params=%+v", dpOK, bfOK, p)
		}
		if !dpOK {
			continue
		}
		if dpCost != bfCost {
			return checked, fmt.Errorf("cost mismatch: dp=%d brute=%d params=%+v", dpCost, bfCost, p)
		}
		if got := ledgerCost(buildLedger(p, plan)); got != dpCost {
			return checked, fmt.Errorf("ledger cost %d != dp cost %d params=%+v", got, dpCost, p)
		}
		checked++
	}
	return checked, nil
}

// verifyExpiryBoundary checks the shelf-life edge semantics on hand-built
// instances: a unit produced on day t is usable on days t..t+d-1 and expires
// at the end of day t+d-1.
func verifyExpiryBoundary() error {
	// Shelf life 1: nothing may be carried; each day's demand is produced
	// that same day. Producing ahead must show up as expiry.
	p1 := DetParams{T: 3, Shelf: 1, Cap: 9, Setup: 5, Unit: 1, Hold: 1, Demand: []int{2, 3, 1}}
	cost, plan, ok := solveDeterministic(p1)
	if !ok {
		return fmt.Errorf("boundary case 1 infeasible")
	}
	bf, _ := bruteDeterministic(p1)
	if cost != bf {
		return fmt.Errorf("boundary case 1: dp=%d brute=%d", cost, bf)
	}
	for t, x := range plan {
		if x != p1.Demand[t] {
			return fmt.Errorf("boundary case 1: shelf=1 must produce exact demand, plan=%v", plan)
		}
	}
	ledger := buildLedger(p1, []int{5, 3, 1}) // overproduce day 1: 3 units expire that day
	if ledger[0].Expired != 3 {
		return fmt.Errorf("boundary case 1: expected 3 expired on day 1, got %d", ledger[0].Expired)
	}

	// Shelf life 2: a unit produced on day 1 can serve day 2 but NOT day 3.
	p2 := DetParams{T: 3, Shelf: 2, Cap: 9, Setup: 100, Unit: 1, Hold: 0, Demand: []int{2, 2, 2}}
	_, plan2, ok2 := solveDeterministic(p2)
	if !ok2 {
		return fmt.Errorf("boundary case 2 infeasible")
	}
	if plan2[1] == 0 && plan2[2] == 0 {
		return fmt.Errorf("boundary case 2: day-3 demand illegally served from day-1 stock, plan=%v", plan2)
	}
	// Forced expiry: produce 4 on day 1 with demand 2,1,0 -> the unit left
	// at age 1 after day-2 consumption expires at the end of day 2.
	p3 := DetParams{T: 3, Shelf: 2, Cap: 9, Setup: 0, Unit: 1, Hold: 0, Demand: []int{2, 1, 0}}
	ledger3 := buildLedger(p3, []int{4, 0, 0})
	if ledger3[0].Expired != 0 || ledger3[1].Expired != 1 {
		return fmt.Errorf("boundary case 3: expiry ledger wrong: %+v / %+v", ledger3[0], ledger3[1])
	}
	// FEFO order: oldest units are consumed first.
	inv := []int{2, 0} // 2 units of age 1, none of age 2 (shelf = 3)
	_, _, consumed, _ := simulateDay(inv, 3, 4, 3)
	if consumed[1] != 2 || consumed[0] != 2 {
		return fmt.Errorf("FEFO violated: consumed=%v", consumed)
	}
	return nil
}

// verifyStochastic compares the scenario-tree DP against exhaustive
// enumeration of all non-anticipative policies on tiny instances, and checks
// that the extracted policy respects non-anticipativity and that both
// simulators agree along every scenario path.
func verifyStochastic(rnd *rand.Rand, trials int) (int, error) {
	checked := 0
	for i := 0; i < trials; i++ {
		p := StochParams{
			T:     2 + rnd.Intn(2),
			Shelf: 1 + rnd.Intn(2),
			Cap:   1 + rnd.Intn(3),
			Setup: rnd.Intn(6),
			Unit:  1 + rnd.Intn(2),
			Hold:  rnd.Intn(2),
		}
		n := 2 + rnd.Intn(2)
		p.Scenarios = make([]Scenario, n)
		den := int64(0)
		nums := make([]int64, n)
		for s := 0; s < n; s++ {
			nums[s] = int64(1 + rnd.Intn(4))
			den += nums[s]
		}
		for s := 0; s < n; s++ {
			dem := make([]int, p.T)
			for t := range dem {
				dem[t] = rnd.Intn(3)
			}
			p.Scenarios[s] = Scenario{Demand: dem, Num: nums[s], Den: den}
		}
		dpCost, solver, dpOK := solveStochastic(p)
		bfCost, bfOK := bruteStochastic(p)
		if dpOK != bfOK {
			return checked, fmt.Errorf("stochastic feasibility mismatch: dp=%v brute=%v", dpOK, bfOK)
		}
		if !dpOK {
			continue
		}
		if dpCost.Cmp(bfCost) != 0 {
			return checked, fmt.Errorf("stochastic cost mismatch: dp=%s brute=%s", dpCost.RatString(), bfCost.RatString())
		}
		if err := checkNonAnticipativeAndLedger(solver); err != nil {
			return checked, err
		}
		checked++
	}
	return checked, nil
}

// checkNonAnticipativeAndLedger walks the optimal policy tree and verifies:
//  1. scenarios with identical observed history get identical decisions
//     (non-anticipativity, by construction of per-node decisions);
//  2. the age-vector ledger and the batch-list ledger agree on every day of
//     every scenario path (shared perishable stock account).
func checkNonAnticipativeAndLedger(s *stochSolver) error {
	p := s.p
	nodeByPrefix := map[string]*node{}
	for t := 1; t <= p.T; t++ {
		for _, n := range s.levels[t] {
			nodeByPrefix[prefixKey(n.prefix)] = n
		}
	}
	decisions := map[string]int{}
	for si := range p.Scenarios {
		sc := &p.Scenarios[si]
		inv := make([]int, p.Shelf-1)
		batches := []Batch{}
		for t := 1; t <= p.T; t++ {
			n := nodeByPrefix[prefixKey(sc.Demand[:t])]
			x := s.decisionAt(n, inv)
			key := prefixKey(n.prefix)
			if prev, seen := decisions[key]; seen && prev != x {
				return fmt.Errorf("non-anticipativity violated at history %v: %d vs %d", n.prefix, prev, x)
			}
			decisions[key] = x
			next1, exp1, _, ok1 := simulateDay(inv, x, sc.Demand[t-1], p.Shelf)
			next2, exp2, ok2 := simulateDayBatches(batches, x, sc.Demand[t-1], p.Shelf)
			if !ok1 || !ok2 {
				return fmt.Errorf("stockout on scenario %d day %d", si, t)
			}
			if exp1 != exp2 || invKey(next1) != invKey(batchesToInv(next2, p.Shelf)) {
				return fmt.Errorf("batch ledger mismatch on scenario %d day %d", si, t)
			}
			inv = next1
			batches = next2
		}
	}
	return nil
}

// expectedCostFloat renders a rational as a rounded decimal for display.
func expectedCostFloat(r *big.Rat) float64 {
	f, _ := new(big.Float).SetRat(r).Float64()
	return f
}
