// Package shardplan assigns tests to a fixed number of executors based on
// their estimated runtimes.
//
// Objectives are lexicographic:
//  1. minimize the number of migrations (tests moved off their current
//     executor) subject to a given per-executor load limit;
//  2. among all minimum-migration plans, minimize the maximum load.
//
// Tests that share a non-empty group are always placed on the same
// executor. When no plan fits within the load limit, the result reports
// the capacity floor: the minimum maximum load any plan can achieve.
package shardplan

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// MaxTests is the maximum number of tests the planner accepts.
const MaxTests = 24

// Unassigned marks a test that has no current executor (a new test).
// Such tests never count as migrations.
const Unassigned = -1

// Test describes a single test to be scheduled.
type Test struct {
	Name     string // unique identifier
	Duration int64  // estimated runtime
	Executor int    // current executor, or Unassigned
	Group    string // tests sharing a non-empty group must be co-located
}

// Result is the outcome of Plan.
type Result struct {
	// Feasible reports whether a plan exists within the requested load limit.
	Feasible bool
	// Assignment maps each test name to its executor. Nil when infeasible.
	Assignment map[string]int
	// Migrations is the number of tests moved off their current executor.
	Migrations int
	// MaxLoad is the maximum per-executor load of the returned plan.
	MaxLoad int64
	// CapacityFloor is a lower bound on the per-executor capacity any
	// feasible plan requires: the minimum achievable maximum load.
	CapacityFloor int64
}

// Plan computes an optimal assignment of tests to executors.
//
// If no assignment keeps every executor at or below loadLimit, the result
// is infeasible and CapacityFloor reports the minimum load limit that
// would admit a plan.
func Plan(tests []Test, executors int, loadLimit int64) (Result, error) {
	if len(tests) == 0 {
		return Result{}, errors.New("shardplan: no tests")
	}
	if len(tests) > MaxTests {
		return Result{}, fmt.Errorf("shardplan: at most %d tests supported, got %d", MaxTests, len(tests))
	}
	if executors < 1 {
		return Result{}, fmt.Errorf("shardplan: executors must be >= 1, got %d", executors)
	}
	seen := make(map[string]bool, len(tests))
	for _, t := range tests {
		if t.Name == "" {
			return Result{}, errors.New("shardplan: test name must not be empty")
		}
		if seen[t.Name] {
			return Result{}, fmt.Errorf("shardplan: duplicate test name %q", t.Name)
		}
		seen[t.Name] = true
		if t.Duration < 0 {
			return Result{}, fmt.Errorf("shardplan: test %q has negative duration", t.Name)
		}
		if t.Executor >= executors {
			return Result{}, fmt.Errorf("shardplan: test %q has executor %d out of range", t.Name, t.Executor)
		}
	}

	items := buildItems(tests)
	floor := capacityFloor(items, executors)
	res := Result{CapacityFloor: floor}
	if loadLimit < floor {
		return res, nil
	}

	s := newSolver(items, executors, loadLimit)
	s.solve()

	res.Feasible = true
	res.Migrations = s.bestMig
	res.MaxLoad = s.bestMax
	res.Assignment = make(map[string]int, len(tests))
	for i, it := range s.items {
		for _, ti := range it.tests {
			res.Assignment[tests[ti].Name] = s.bestAssign[i]
		}
	}
	return res, nil
}

// item is a schedulable unit: one test, or all tests of a group merged.
type item struct {
	dur    int64
	tests  []int       // indices into the original tests slice
	homes  map[int]int // current executor -> number of member tests
	placed int         // member tests with a current executor
}

// migCost returns how many member tests migrate if placed on executor e.
// Tests without a current executor never count as migrations.
func (it item) migCost(e int) int {
	return it.placed - it.homes[e]
}

func buildItems(tests []Test) []item {
	byGroup := make(map[string]int)
	var items []item
	for i, t := range tests {
		idx := -1
		if t.Group != "" {
			if j, ok := byGroup[t.Group]; ok {
				idx = j
			} else {
				idx = len(items)
				byGroup[t.Group] = idx
			}
		}
		if idx < 0 {
			idx = len(items)
		}
		if idx == len(items) {
			items = append(items, item{homes: make(map[int]int)})
		}
		it := &items[idx]
		it.dur += t.Duration
		it.tests = append(it.tests, i)
		if t.Executor != Unassigned {
			it.placed++
			it.homes[t.Executor]++
		}
	}
	// Most-constrained-first ordering: descending duration, stable tie-break.
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].dur != items[b].dur {
			return items[a].dur > items[b].dur
		}
		return items[a].tests[0] < items[b].tests[0]
	})
	return items
}

// capacityFloor computes the minimum achievable maximum load (the optimal
// makespan) for the items on k identical executors.
func capacityFloor(items []item, k int) int64 {
	durs := make([]int64, len(items))
	var total, longest int64
	for i, it := range items {
		durs[i] = it.dur
		total += it.dur
		if it.dur > longest {
			longest = it.dur
		}
	}
	sort.Slice(durs, func(a, b int) bool { return durs[a] > durs[b] })

	// Initial incumbent: longest-processing-time greedy.
	loads := make([]int64, k)
	best := int64(0)
	for _, d := range durs {
		e := 0
		for i := 1; i < k; i++ {
			if loads[i] < loads[e] {
				e = i
			}
		}
		loads[e] += d
		if loads[e] > best {
			best = loads[e]
		}
	}
	lower := (total + int64(k) - 1) / int64(k)
	if longest > lower {
		lower = longest
	}
	if best == lower {
		return best
	}

	cur := make([]int64, k)
	var dfs func(i int)
	dfs = func(i int) {
		if i == len(durs) {
			m := int64(0)
			for _, l := range cur {
				if l > m {
					m = l
				}
			}
			if m < best {
				best = m
			}
			return
		}
		if best == lower {
			return
		}
		tried := make(map[int64]bool, k)
		for e := 0; e < k; e++ {
			if tried[cur[e]] {
				continue // executors with equal load are interchangeable
			}
			tried[cur[e]] = true
			if cur[e]+durs[i] >= best {
				continue
			}
			cur[e] += durs[i]
			dfs(i + 1)
			cur[e] -= durs[i]
		}
	}
	dfs(0)
	return best
}

// solver searches for the lexicographically best (migrations, max load)
// assignment within the load limit.
type solver struct {
	items []item
	k     int
	limit int64

	loads  []int64
	assign []int

	bestMig    int
	bestMax    int64
	bestAssign []int
}

func newSolver(items []item, k int, limit int64) *solver {
	s := &solver{
		items:   items,
		k:       k,
		limit:   limit,
		loads:   make([]int64, k),
		assign:  make([]int, len(items)),
		bestMig: math.MaxInt,
		bestMax: math.MaxInt64,
	}
	s.greedy()
	return s
}

// greedy seeds the search with a decent incumbent: each item goes to the
// feasible executor with the fewest migrations, breaking ties by the
// resulting load.
func (s *solver) greedy() {
	for i, it := range s.items {
		bestE, bestCost, bestLoad := -1, math.MaxInt, int64(math.MaxInt64)
		for e := 0; e < s.k; e++ {
			if s.loads[e]+it.dur > s.limit {
				continue
			}
			cost := it.migCost(e)
			if cost < bestCost || (cost == bestCost && s.loads[e] < bestLoad) {
				bestE, bestCost, bestLoad = e, cost, s.loads[e]
			}
		}
		if bestE < 0 {
			// Greedy dead end; leave the incumbent at infinity and let
			// the exact search find a plan from scratch.
			for j := 0; j < s.k; j++ {
				s.loads[j] = 0
			}
			return
		}
		s.assign[i] = bestE
		s.loads[bestE] += it.dur
	}
	mig := 0
	for i, it := range s.items {
		mig += it.migCost(s.assign[i])
	}
	s.bestMig = mig
	s.bestMax = maxLoad(s.loads)
	s.bestAssign = append([]int(nil), s.assign...)
	for j := 0; j < s.k; j++ {
		s.loads[j] = 0
	}
}

func maxLoad(loads []int64) int64 {
	m := int64(0)
	for _, l := range loads {
		if l > m {
			m = l
		}
	}
	return m
}

func (s *solver) solve() {
	s.dfs(0, 0)
}

// forcedMigrations lower-bounds the extra migrations any completion of the
// current partial assignment needs. It also reports whether every
// remaining item still has at least one feasible executor.
func (s *solver) forcedMigrations(pos int) (int, bool) {
	lb := 0
	for j := pos; j < len(s.items); j++ {
		it := s.items[j]
		best := math.MaxInt
		for e := 0; e < s.k; e++ {
			if s.loads[e]+it.dur > s.limit {
				continue
			}
			if c := it.migCost(e); c < best {
				best = c
			}
		}
		if best == math.MaxInt {
			return 0, false
		}
		lb += best
	}
	return lb, true
}

// sameHomes reports whether executors e1 and e2 look identical to every
// item not yet placed.
func (s *solver) sameHomes(e1, e2, pos int) bool {
	for j := pos; j < len(s.items); j++ {
		if s.items[j].homes[e1] != s.items[j].homes[e2] {
			return false
		}
	}
	return true
}

func (s *solver) dfs(pos, mig int) {
	if pos == len(s.items) {
		m := maxLoad(s.loads)
		if mig < s.bestMig || (mig == s.bestMig && m < s.bestMax) {
			s.bestMig = mig
			s.bestMax = m
			s.bestAssign = append(s.bestAssign[:0], s.assign...)
		}
		return
	}
	lb, ok := s.forcedMigrations(pos)
	if !ok {
		return
	}
	if mig+lb > s.bestMig {
		return
	}
	if mig+lb == s.bestMig && maxLoad(s.loads) >= s.bestMax {
		return
	}

	it := s.items[pos]
	// Try executors with fewer migrations first to find good incumbents early.
	// Executors are interchangeable only when they carry the same load and
	// every remaining item sees the same migration cost on them.
	order := make([]int, 0, s.k)
	seen := make([]int, 0, s.k)
	for e := 0; e < s.k; e++ {
		if s.loads[e]+it.dur > s.limit {
			continue
		}
		dup := false
		for _, p := range seen {
			if s.loads[p] == s.loads[e] && s.sameHomes(p, e, pos) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		seen = append(seen, e)
		order = append(order, e)
	}
	sort.Slice(order, func(a, b int) bool {
		ca, cb := it.migCost(order[a]), it.migCost(order[b])
		if ca != cb {
			return ca < cb
		}
		return s.loads[order[a]] < s.loads[order[b]]
	})
	for _, e := range order {
		s.assign[pos] = e
		s.loads[e] += it.dur
		s.dfs(pos+1, mig+it.migCost(e))
		s.loads[e] -= it.dur
	}
}
