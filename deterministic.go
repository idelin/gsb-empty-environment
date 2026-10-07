package main

// ---------------------------------------------------------------------------
// Deterministic perishable lot-sizing.
//
// Horizon T days, known integer demands (total <= 30), shelf life d,
// daily capacity Cap, setup cost Setup, unit cost Unit, holding cost Hold.
// FEFO consumption, no stockouts. Minimize setup + production + holding.
//
// Solved exactly by DP over (day, age-vector inventory). Since total demand
// is at most 30, the number of reachable age vectors is small.
// ---------------------------------------------------------------------------

const infCost = int64(1) << 50

type DetParams struct {
	T      int
	Shelf  int
	Cap    int
	Setup  int
	Unit   int
	Hold   int
	Demand []int
}

// DayLedger is the batch age account for one day.
type DayLedger struct {
	Day      int
	StartInv []int // ages 1..Shelf-1 at start of day
	Prod     int
	Demand   int
	Consumed []int // consumed units by age 0..Shelf-1
	Expired  int
	EndInv   []int // ages 1..Shelf-1 carried to next day
	Setup    int
	ProdCost int
	HoldCost int
}

func (l DayLedger) total() int { return l.Setup + l.ProdCost + l.HoldCost }

// solveDeterministic returns the optimal cost and production plan.
func solveDeterministic(p DetParams) (best int, plan []int, ok bool) {
	suffix := make([]int, p.T+1) // suffix[t] = demand from day t..T-1
	for t := p.T - 1; t >= 0; t-- {
		suffix[t] = suffix[t+1] + p.Demand[t]
	}
	type key struct {
		t   int
		inv string
	}
	memo := map[key]int64{}
	choice := map[key]int{}
	var dp func(t int, inv []int) int64
	dp = func(t int, inv []int) int64 {
		if t == p.T {
			return 0
		}
		k := key{t, invKey(inv)}
		if v, seen := memo[k]; seen {
			return v
		}
		invSum := sumInts(inv)
		dem := p.Demand[t]
		lo := maxInt(0, dem-invSum)
		// Never useful to produce more than the total remaining demand.
		hi := minInt(p.Cap, suffix[t]-invSum)
		bestV := infCost
		bestX := -1
		for x := lo; x <= hi; x++ {
			next, _, _, feasible := simulateDay(inv, x, dem, p.Shelf)
			if !feasible {
				continue
			}
			c := int64(p.Unit*x + p.Hold*sumInts(next))
			if x > 0 {
				c += int64(p.Setup)
			}
			sub := dp(t+1, next)
			if sub >= infCost {
				continue
			}
			c += sub
			if c < bestV {
				bestV = c
				bestX = x
			}
		}
		memo[k] = bestV
		choice[k] = bestX
		return bestV
	}
	total := dp(0, make([]int, p.Shelf-1))
	if total >= infCost {
		return 0, nil, false
	}
	plan = make([]int, p.T)
	inv := make([]int, p.Shelf-1)
	for t := 0; t < p.T; t++ {
		x := choice[key{t, invKey(inv)}]
		plan[t] = x
		next, _, _, _ := simulateDay(inv, x, p.Demand[t], p.Shelf)
		inv = next
	}
	return int(total), plan, true
}

// buildLedger replays a plan and produces the per-day batch age account.
func buildLedger(p DetParams, plan []int) []DayLedger {
	inv := make([]int, p.Shelf-1)
	out := make([]DayLedger, 0, p.T)
	for t := 0; t < p.T; t++ {
		next, expired, consumed, _ := simulateDay(inv, plan[t], p.Demand[t], p.Shelf)
		l := DayLedger{
			Day:      t + 1,
			StartInv: append([]int{}, inv...),
			Prod:     plan[t],
			Demand:   p.Demand[t],
			Consumed: consumed,
			Expired:  expired,
			EndInv:   append([]int{}, next...),
			ProdCost: p.Unit * plan[t],
			HoldCost: p.Hold * sumInts(next),
		}
		if plan[t] > 0 {
			l.Setup = p.Setup
		}
		out = append(out, l)
		inv = next
	}
	return out
}

func ledgerCost(ledger []DayLedger) int {
	s := 0
	for _, l := range ledger {
		s += l.total()
	}
	return s
}

// bruteDeterministic enumerates every production sequence x in [0..Cap]^T,
// simulating each with the independent batch-list simulator, and returns the
// minimum feasible cost. Used to verify the DP on small instances.
func bruteDeterministic(p DetParams) (int, bool) {
	best := infCost
	var rec func(t int, batches []Batch, cost int64)
	rec = func(t int, batches []Batch, cost int64) {
		if cost >= best {
			return
		}
		if t == p.T {
			best = cost
			return
		}
		dem := p.Demand[t]
		for x := 0; x <= p.Cap; x++ {
			nb, _, feasible := simulateDayBatches(batches, x, dem, p.Shelf)
			if !feasible {
				continue
			}
			c := cost + int64(p.Unit*x+p.Hold*sumBatches(nb))
			if x > 0 {
				c += int64(p.Setup)
			}
			rec(t+1, nb, c)
		}
	}
	rec(0, nil, 0)
	if best >= infCost {
		return 0, false
	}
	return int(best), true
}
