package shardplan

import (
	"math"
	"sort"
)

const eps = 1e-9

// solver is a branch-and-bound search over item-to-executor assignments.
// It is used in two modes:
//
//   - capped (searchCapped): loads may not exceed cap; minimizes
//     (migrations, max load) lexicographically.
//   - makespan (searchMakespan): no capacity; minimizes the max load.
type solver struct {
	items []item
	k     int
	cap   float64

	loads []float64
	place []int
	moves int

	suffixDur  []float64 // suffixDur[i]: total duration of items[i:]
	suffixMax  []float64 // suffixMax[i]: max duration among items[i:]
	suffixMinC []int     // suffixMinC[i]: sum of minC over items[i:]

	disp [][]dispItem // scratch, one slice per executor

	bestMoves int
	bestMax   float64
	bestPlace []int
	found     bool
}

// dispItem is an item considered for the displacement lower bound.
type dispItem struct {
	dur   float64
	ratio float64 // gap per unit of duration
	gap   float64
}

func newSolver(items []item, k int, cap float64) *solver {
	n := len(items)
	s := &solver{
		items:      items,
		k:          k,
		cap:        cap,
		loads:      make([]float64, k),
		place:      make([]int, n),
		suffixDur:  make([]float64, n+1),
		suffixMax:  make([]float64, n+1),
		suffixMinC: make([]int, n+1),
		disp:       make([][]dispItem, k),
		bestMoves:  int(1 << 30),
		bestMax:    math.Inf(1),
		bestPlace:  make([]int, n),
	}
	for i := n - 1; i >= 0; i-- {
		s.suffixDur[i] = s.suffixDur[i+1] + items[i].dur
		s.suffixMax[i] = math.Max(s.suffixMax[i+1], items[i].dur)
		s.suffixMinC[i] = s.suffixMinC[i+1] + items[i].minC
	}
	return s
}

// result builds a Result from the best assignment found.
func (s *solver) result(tests []Test, feasible bool, lowerBound float64) Result {
	r := Result{
		Feasible:    feasible,
		Assignments: make(map[string]int, len(tests)),
		Loads:       make([]float64, s.k),
		LowerBound:  lowerBound,
	}
	for i := range s.items {
		e := s.bestPlace[i]
		for _, ti := range s.items[i].tests {
			r.Assignments[tests[ti].Name] = e
			r.Loads[e] += tests[ti].Duration
			if tests[ti].Previous >= 0 && tests[ti].Previous != e {
				r.Migrations++
			}
		}
	}
	for _, l := range r.Loads {
		if l > r.MaxLoad {
			r.MaxLoad = l
		}
	}
	return r
}

func (s *solver) loadStats() (curMax, sum, min float64) {
	curMax, sum, min = 0, 0, math.Inf(1)
	for _, l := range s.loads {
		sum += l
		if l > curMax {
			curMax = l
		}
		if l < min {
			min = l
		}
	}
	return curMax, sum, min
}

// executorOrder returns executor indices ordered for the capped search:
// cheapest migration cost first, then least loaded.
func (s *solver) executorOrder(it *item) []int {
	order := make([]int, s.k)
	for e := range order {
		order[e] = e
	}
	sort.Slice(order, func(a, b int) bool {
		ea, eb := order[a], order[b]
		if it.cost[ea] != it.cost[eb] {
			return it.cost[ea] < it.cost[eb]
		}
		if s.loads[ea] != s.loads[eb] {
			return s.loads[ea] < s.loads[eb]
		}
		return ea < eb
	})
	return order
}

// displacement returns a lower bound on the extra migration cost (beyond
// each item's minimum cost) forced by executors whose remaining capacity
// cannot hold all remaining items that are cheapest there. It is a
// fractional-knapsack relaxation: displacing item j costs gap_j per item
// and frees dur_j capacity, so covering the excess duration costs at least
// the greedy fractional value.
func (s *solver) displacement(idx int, tol float64) float64 {
	for e := range s.disp {
		s.disp[e] = s.disp[e][:0]
	}
	for j := idx; j < len(s.items); j++ {
		it := &s.items[j]
		if it.cheap >= 0 && it.gap > 0 {
			ratio := math.Inf(1)
			if it.dur > 0 {
				ratio = it.gap / it.dur
			}
			s.disp[it.cheap] = append(s.disp[it.cheap], dispItem{dur: it.dur, ratio: ratio, gap: it.gap})
		}
	}
	extra := 0.0
	for e := 0; e < s.k; e++ {
		list := s.disp[e]
		if len(list) == 0 {
			continue
		}
		sum := 0.0
		for _, d := range list {
			sum += d.dur
		}
		excess := sum - (s.cap - s.loads[e])
		if excess <= tol {
			continue
		}
		sort.Slice(list, func(a, b int) bool { return list[a].ratio < list[b].ratio })
		for _, d := range list {
			if excess <= 0 {
				break
			}
			take := math.Min(d.dur, excess)
			if d.dur > 0 {
				extra += d.gap * take / d.dur
			}
			excess -= take
		}
	}
	return extra
}

// searchCapped assigns items[idx:] to executors without exceeding the
// capacity, minimizing (migrations, max load) lexicographically.
func (s *solver) searchCapped(idx int) {
	n := len(s.items)
	curMax, sumLoads, minLoad := s.loadStats()
	tol := eps * (1 + s.cap)

	if idx == n {
		if !s.found || s.moves < s.bestMoves ||
			(s.moves == s.bestMoves && curMax < s.bestMax-eps*(1+s.bestMax)) {
			s.found = true
			s.bestMoves = s.moves
			s.bestMax = curMax
			copy(s.bestPlace, s.place)
		}
		return
	}

	// Bound A: each remaining item pays at least the cost of its cheapest
	// executor it still fits on. Loads only grow, so this stays valid.
	lbA := float64(s.moves)
	for j := idx; j < n; j++ {
		it := &s.items[j]
		best := int(1 << 30)
		for e := 0; e < s.k; e++ {
			if s.loads[e]+it.dur <= s.cap+tol && it.cost[e] < best {
				best = it.cost[e]
			}
		}
		if best == int(1<<30) {
			return // item j fits nowhere: dead end
		}
		lbA += float64(best)
	}

	// Bound B: per-item minimum costs plus forced displacement.
	lbB := float64(s.moves+s.suffixMinC[idx]) + s.displacement(idx, tol)

	lbMoves := math.Max(lbA, lbB)
	lbMax := math.Max(curMax, (sumLoads+s.suffixDur[idx])/float64(s.k))
	lbMax = math.Max(lbMax, minLoad+s.suffixMax[idx])

	m := math.Ceil(lbMoves - eps)
	if m > float64(s.bestMoves) ||
		(m == float64(s.bestMoves) && lbMax >= s.bestMax-eps*(1+s.bestMax)) {
		return
	}

	it := &s.items[idx]
	for _, e := range s.executorOrder(it) {
		if s.loads[e]+it.dur > s.cap+tol {
			continue
		}
		s.loads[e] += it.dur
		s.place[idx] = e
		s.moves += it.cost[e]
		s.searchCapped(idx + 1)
		s.moves -= it.cost[e]
		s.loads[e] -= it.dur
	}
}

// searchMakespan assigns items[idx:] to executors minimizing the maximum
// load. Used to compute the capacity lower bound for infeasible inputs.
func (s *solver) searchMakespan(idx int) {
	n := len(s.items)
	curMax, sumLoads, minLoad := s.loadStats()
	tol := eps
	if !math.IsInf(s.bestMax, 1) {
		tol = eps * (1 + s.bestMax)
	}

	if idx == n {
		if !s.found || curMax < s.bestMax-tol {
			s.found = true
			s.bestMax = curMax
			copy(s.bestPlace, s.place)
		}
		return
	}

	lbMax := math.Max(curMax, (sumLoads+s.suffixDur[idx])/float64(s.k))
	lbMax = math.Max(lbMax, minLoad+s.suffixMax[idx])
	if lbMax >= s.bestMax-tol {
		return
	}

	it := &s.items[idx]
	tried := make([]float64, 0, s.k)
	for _, e := range s.loadOrder() {
		if s.loads[e]+it.dur >= s.bestMax-tol {
			continue
		}
		// Executors with identical loads are interchangeable here.
		dup := false
		for _, l := range tried {
			if l == s.loads[e] {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		tried = append(tried, s.loads[e])
		s.loads[e] += it.dur
		s.place[idx] = e
		s.searchMakespan(idx + 1)
		s.loads[e] -= it.dur
	}
}

// loadOrder returns executor indices ordered by increasing load.
func (s *solver) loadOrder() []int {
	order := make([]int, s.k)
	for e := range order {
		order[e] = e
	}
	sort.Slice(order, func(a, b int) bool {
		if s.loads[order[a]] != s.loads[order[b]] {
			return s.loads[order[a]] < s.loads[order[b]]
		}
		return order[a] < order[b]
	})
	return order
}

// greedyCapped builds an initial feasible-if-possible incumbent for the
// capped search, preferring cheap then least-loaded executors.
func (s *solver) greedyCapped() {
	tol := eps * (1 + s.cap)
	curMax := 0.0
	ok := true
	for i := range s.items {
		it := &s.items[i]
		bestE, bestCost := -1, int(1<<30)
		bestLoad := math.Inf(1)
		for e := 0; e < s.k; e++ {
			nl := s.loads[e] + it.dur
			if nl > s.cap+tol {
				continue
			}
			if it.cost[e] < bestCost || (it.cost[e] == bestCost && nl < bestLoad) {
				bestE, bestCost, bestLoad = e, it.cost[e], nl
			}
		}
		if bestE < 0 {
			ok = false
			break
		}
		s.loads[bestE] += it.dur
		s.place[i] = bestE
		s.moves += it.cost[bestE]
		curMax = math.Max(curMax, s.loads[bestE])
	}
	if ok {
		s.found = true
		s.bestMoves = s.moves
		s.bestMax = curMax
		copy(s.bestPlace, s.place)
	}
	for e := range s.loads {
		s.loads[e] = 0
	}
	s.moves = 0
}

// greedyMakespan builds an initial incumbent for the makespan search by
// placing each item on the executor that keeps the max load smallest.
func (s *solver) greedyMakespan() {
	curMax := 0.0
	for i := range s.items {
		it := &s.items[i]
		bestE := -1
		bestVal := math.Inf(1)
		for e := 0; e < s.k; e++ {
			v := math.Max(curMax, s.loads[e]+it.dur)
			if v < bestVal || (v == bestVal && bestE >= 0 && s.loads[e] < s.loads[bestE]) {
				bestE, bestVal = e, v
			}
		}
		s.loads[bestE] += it.dur
		s.place[i] = bestE
		curMax = math.Max(curMax, s.loads[bestE])
	}
	s.found = true
	s.bestMax = curMax
	copy(s.bestPlace, s.place)
	for e := range s.loads {
		s.loads[e] = 0
	}
}
