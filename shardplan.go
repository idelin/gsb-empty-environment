// Package shardplan distributes tests across a fixed number of executors
// based on their estimated runtimes.
//
// Objectives, in priority order:
//
//  1. Minimize the number of migrations, i.e. tests whose assigned executor
//     differs from the previous plan, subject to every executor's load
//     staying within the given capacity.
//  2. Among all plans with the fewest migrations, minimize the maximum
//     executor load.
//
// Tests that share a non-empty group are always placed on the same
// executor. At most MaxTests tests are supported. When no assignment fits
// within the capacity, Plan reports the minimal achievable maximum load as
// a lower bound on the capacity required to make the plan feasible.
package shardplan

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// MaxTests is the maximum number of tests Plan accepts.
const MaxTests = 24

// Test describes a single test to schedule.
type Test struct {
	// Name uniquely identifies the test.
	Name string `json:"name"`
	// Duration is the estimated runtime of the test.
	Duration float64 `json:"duration"`
	// Group, when non-empty, forces all tests with the same group name
	// onto the same executor.
	Group string `json:"group,omitempty"`
	// Previous is the executor the test ran on in the previous plan, or
	// -1 if the test is new. Assigning the test to a different executor
	// counts as one migration.
	Previous int `json:"previous"`
}

// Result is the outcome of Plan.
type Result struct {
	// Feasible reports whether an assignment within the capacity exists.
	Feasible bool `json:"feasible"`
	// Assignments maps each test name to its executor index.
	Assignments map[string]int `json:"assignments"`
	// Loads holds the total estimated runtime per executor.
	Loads []float64 `json:"loads"`
	// MaxLoad is the largest executor load.
	MaxLoad float64 `json:"maxLoad"`
	// Migrations is the number of tests whose executor changed relative
	// to the previous plan.
	Migrations int `json:"migrations"`
	// LowerBound is only meaningful when Feasible is false: it is the
	// minimal achievable maximum load, i.e. a lower bound on the capacity
	// required to make the plan feasible. Assignments then holds a
	// suggested plan attaining that bound.
	LowerBound float64 `json:"lowerBound,omitempty"`
}

// Plan assigns tests to executors[0, executors) such that every executor's
// total estimated runtime stays within capacity, minimizing first the
// number of migrations and then the maximum load. If no such assignment
// exists, the returned Result is infeasible and LowerBound holds the
// minimal capacity that would make the plan feasible.
func Plan(tests []Test, executors int, capacity float64) (Result, error) {
	if executors < 1 {
		return Result{}, fmt.Errorf("shardplan: executors must be >= 1, got %d", executors)
	}
	if len(tests) > MaxTests {
		return Result{}, fmt.Errorf("shardplan: at most %d tests supported, got %d", MaxTests, len(tests))
	}
	if capacity < 0 || math.IsNaN(capacity) {
		return Result{}, fmt.Errorf("shardplan: capacity must be >= 0, got %v", capacity)
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
		if t.Duration < 0 || math.IsNaN(t.Duration) {
			return Result{}, fmt.Errorf("shardplan: test %q has invalid duration %v", t.Name, t.Duration)
		}
		if t.Previous < -1 || t.Previous >= executors {
			return Result{}, fmt.Errorf("shardplan: test %q has previous executor %d out of range [-1, %d)", t.Name, t.Previous, executors)
		}
	}

	items := buildItems(tests, executors)
	if len(items) == 0 {
		return Result{Feasible: true, Assignments: map[string]int{}, Loads: make([]float64, executors)}, nil
	}
	if executors == 1 {
		return planSingleExecutor(tests, capacity), nil
	}

	s := newSolver(items, executors, capacity)
	s.greedyCapped()
	s.searchCapped(0)
	if s.found {
		return s.result(tests, true, 0), nil
	}

	// Infeasible under the given capacity: find the minimal achievable
	// maximum load, which is a lower bound on the required capacity.
	m := newSolver(items, executors, 0)
	m.greedyMakespan()
	m.searchMakespan(0)
	return m.result(tests, false, m.bestMax), nil
}

// item is a set of tests that must share an executor (a co-location group,
// or a single ungrouped test).
type item struct {
	tests []int   // indices into the tests slice
	dur   float64 // total duration
	cost  []int   // cost[e]: migrations incurred when placed on executor e
	minC  int     // minimum over cost
	cheap int     // executor uniquely achieving minC, or -1 if tied
	gap   float64 // min extra cost over minC when not placed on cheap
}

// buildItems merges tests into co-location items and precomputes migration
// costs. Items are ordered by decreasing duration for the search.
func buildItems(tests []Test, k int) []item {
	var items []item
	groupIdx := make(map[string]int)
	for i, t := range tests {
		if t.Group != "" {
			if j, ok := groupIdx[t.Group]; ok {
				items[j].tests = append(items[j].tests, i)
				continue
			}
			groupIdx[t.Group] = len(items)
		}
		items = append(items, item{tests: []int{i}})
	}
	for j := range items {
		it := &items[j]
		stay := make([]int, k)
		base := 0
		for _, ti := range it.tests {
			it.dur += tests[ti].Duration
			if tests[ti].Previous >= 0 {
				base++
				stay[tests[ti].Previous]++
			}
		}
		it.cost = make([]int, k)
		it.minC = int(1 << 30)
		for e := 0; e < k; e++ {
			it.cost[e] = base - stay[e]
			if it.cost[e] < it.minC {
				it.minC = it.cost[e]
			}
		}
		it.cheap = -1
		ties := 0
		for e := 0; e < k; e++ {
			if it.cost[e] == it.minC {
				ties++
				it.cheap = e
			}
		}
		if ties != 1 {
			it.cheap = -1
		}
		if it.cheap >= 0 {
			it.gap = math.Inf(1)
			for e := 0; e < k; e++ {
				if e != it.cheap {
					it.gap = math.Min(it.gap, float64(it.cost[e]-it.minC))
				}
			}
		}
	}
	sort.SliceStable(items, func(a, b int) bool {
		if items[a].dur != items[b].dur {
			return items[a].dur > items[b].dur
		}
		return tests[items[a].tests[0]].Name < tests[items[b].tests[0]].Name
	})
	return items
}

// planSingleExecutor handles the trivial single-executor case.
func planSingleExecutor(tests []Test, capacity float64) Result {
	r := Result{Assignments: make(map[string]int, len(tests)), Loads: make([]float64, 1)}
	for _, t := range tests {
		r.Assignments[t.Name] = 0
		r.Loads[0] += t.Duration
	}
	r.MaxLoad = r.Loads[0]
	if r.MaxLoad <= capacity+eps*(1+capacity) {
		r.Feasible = true
	} else {
		r.LowerBound = r.MaxLoad
	}
	return r
}
