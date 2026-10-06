package main

import "fmt"

// DetSolver solves the deterministic perishable lot-sizing problem by
// dynamic programming over (day, inventory age vector) states.
// All quantities are integral; stockouts are forbidden.
type DetSolver struct {
	P      Params
	Demand []int
	rem    []int // rem[t] = total demand on days t..T-1
	memo   map[string]int
	choice map[string]int
	states int
}

func NewDetSolver(p Params, demand []int) *DetSolver {
	s := &DetSolver{P: p, Demand: demand, memo: map[string]int{}, choice: map[string]int{}}
	s.rem = make([]int, p.T+1)
	for t := p.T - 1; t >= 0; t-- {
		s.rem[t] = s.rem[t+1] + demand[t]
	}
	return s
}

// dp returns the minimum cost of days t..T-1 given the start-of-day
// inventory inv (ages 1..Shelf-1), or INF if infeasible.
func (s *DetSolver) dp(t int, inv []int) int {
	if t == s.P.T {
		return 0
	}
	key := fmt.Sprintf("%d|%s", t, invKey(inv))
	if v, ok := s.memo[key]; ok {
		return v
	}
	s.states++

	onHand := 0
	for _, v := range inv {
		onHand += v
	}
	// Must meet today's demand; never produce more than the remaining
	// demand can absorb (extra units can only add cost).
	lo := s.Demand[t] - onHand
	if lo < 0 {
		lo = 0
	}
	hi := s.P.Cap
	if r := s.rem[t] - onHand; r < hi {
		hi = r
	}

	best, bestX := INF, -1
	for x := lo; x <= hi; x++ {
		next, res := stepDay(s.P, inv, x, s.Demand[t])
		if !res.OK {
			continue
		}
		sub := s.dp(t+1, next)
		if sub >= INF {
			continue
		}
		if c := res.DayCost + sub; c < best {
			best, bestX = c, x
		}
	}
	s.memo[key] = best
	s.choice[key] = bestX
	return best
}

// Solve returns the minimum total cost, an optimal integral production
// plan, and ok=false if no feasible plan exists.
func (s *DetSolver) Solve() (cost int, plan []int, ok bool) {
	inv := make([]int, s.P.Shelf-1)
	cost = s.dp(0, inv)
	if cost >= INF {
		return 0, nil, false
	}
	plan = make([]int, s.P.T)
	for t := 0; t < s.P.T; t++ {
		key := fmt.Sprintf("%d|%s", t, invKey(inv))
		x := s.choice[key]
		plan[t] = x
		next, res := stepDay(s.P, inv, x, s.Demand[t])
		if !res.OK {
			return 0, nil, false
		}
		inv = next
	}
	return cost, plan, true
}
