// Package testselect selects a minimum-cost set of tests that covers all
// required faults. Tests may declare prerequisite tests (which are pulled
// in transitively and billed) and mutual-exclusion relations. Selection is
// done by exhaustive subset enumeration, so at most MaxTests tests are
// accepted. Faults that no feasible selection can cover are reported as
// missing.
package testselect

import (
	"fmt"
	"math/bits"
	"sort"
)

// MaxTests is the largest number of tests accepted by Select. The
// enumeration is O(2^MaxTests), which stays practical at 20.
const MaxTests = 20

// Test describes a single test case.
type Test struct {
	ID       string   // unique identifier
	Cost     int64    // non-negative execution cost
	Covers   []string // faults this test covers
	Requires []string // IDs of tests that must also run (transitively)
	Excludes []string // IDs of tests that cannot run together with this one
}

// Result is the outcome of Select.
type Result struct {
	Selected  []string // IDs of chosen tests (including prerequisites), sorted
	TotalCost int64    // sum of costs of all selected tests
	Missing   []string // required faults no feasible selection can cover
}

// Select enumerates every subset of tests, expands it to its prerequisite
// closure, and keeps it when the closure is exclusion-free. Among the valid
// subsets it prefers, in order: the most covered faults, the lowest total
// cost, and the lexicographically smallest sorted ID list.
func Select(tests []Test, faults []string) (*Result, error) {
	if len(tests) > MaxTests {
		return nil, fmt.Errorf("testselect: %d tests exceed the limit of %d", len(tests), MaxTests)
	}

	n := len(tests)
	index := make(map[string]int, n)
	for i, t := range tests {
		if t.ID == "" {
			return nil, fmt.Errorf("testselect: test %d has an empty ID", i)
		}
		if t.Cost < 0 {
			return nil, fmt.Errorf("testselect: test %q has negative cost %d", t.ID, t.Cost)
		}
		if _, dup := index[t.ID]; dup {
			return nil, fmt.Errorf("testselect: duplicate test ID %q", t.ID)
		}
		index[t.ID] = i
	}

	requires := make([][]uint32, n)
	conflict := make([]uint32, n)
	for i, t := range tests {
		for _, r := range t.Requires {
			j, ok := index[r]
			if !ok {
				return nil, fmt.Errorf("testselect: test %q requires unknown test %q", t.ID, r)
			}
			requires[i] = append(requires[i], uint32(j))
		}
		for _, e := range t.Excludes {
			j, ok := index[e]
			if !ok {
				return nil, fmt.Errorf("testselect: test %q excludes unknown test %q", t.ID, e)
			}
			// Mutual exclusion is symmetric regardless of who declares it.
			conflict[i] |= 1 << uint(j)
			conflict[j] |= 1 << uint(i)
		}
	}

	faultIndex := make(map[string]int, len(faults))
	for i, f := range faults {
		if _, dup := faultIndex[f]; !dup {
			faultIndex[f] = i
		}
	}
	words := (len(faults) + 63) / 64
	coverage := make([][]uint64, n)
	for i, t := range tests {
		coverage[i] = make([]uint64, words)
		for _, f := range t.Covers {
			if fi, ok := faultIndex[f]; ok {
				coverage[i][fi/64] |= 1 << uint(fi%64)
			}
		}
	}

	var (
		bestMask uint32
		bestCost int64
		bestBits = -1
	)
	for mask := uint32(0); mask < uint32(1)<<uint(n); mask++ {
		full := closure(mask, requires)
		if !compatible(full, conflict) {
			continue
		}
		covered := make([]uint64, words)
		var cost int64
		for i := 0; i < n; i++ {
			if full&(1<<uint(i)) != 0 {
				cost += tests[i].Cost
				for w := 0; w < words; w++ {
					covered[w] |= coverage[i][w]
				}
			}
		}
		coveredBits := 0
		for w := 0; w < words; w++ {
			coveredBits += bits.OnesCount64(covered[w])
		}
		if coveredBits > bestBits ||
			(coveredBits == bestBits && cost < bestCost) ||
			(coveredBits == bestBits && cost == bestCost && maskLess(full, bestMask, tests)) {
			bestMask, bestCost, bestBits = full, cost, coveredBits
		}
	}

	coveredByBest := make([]uint64, words)
	for i := 0; i < n; i++ {
		if bestMask&(1<<uint(i)) != 0 {
			for w := 0; w < words; w++ {
				coveredByBest[w] |= coverage[i][w]
			}
		}
	}
	res := &Result{TotalCost: bestCost}
	for i := 0; i < n; i++ {
		if bestMask&(1<<uint(i)) != 0 {
			res.Selected = append(res.Selected, tests[i].ID)
		}
	}
	sort.Strings(res.Selected)
	for i, f := range faults {
		if coveredByBest[i/64]&(1<<uint(i%64)) == 0 {
			res.Missing = append(res.Missing, f)
		}
	}
	return res, nil
}

// closure returns mask expanded with every transitively required test.
func closure(mask uint32, requires [][]uint32) uint32 {
	queue := []uint32{}
	for i := uint32(0); i < uint32(len(requires)); i++ {
		if mask&(1<<i) != 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range requires[cur] {
			if mask&(1<<dep) == 0 {
				mask |= 1 << dep
				queue = append(queue, dep)
			}
		}
	}
	return mask
}

// compatible reports whether no selected test conflicts with another.
func compatible(mask uint32, conflict []uint32) bool {
	for i := uint32(0); i < uint32(len(conflict)); i++ {
		if mask&(1<<i) != 0 && conflict[i]&mask != 0 {
			return false
		}
	}
	return true
}

// maskLess compares two selections by their sorted ID lists.
func maskLess(a, b uint32, tests []Test) bool {
	ids := func(mask uint32) []string {
		out := []string{}
		for i := uint32(0); i < uint32(len(tests)); i++ {
			if mask&(1<<i) != 0 {
				out = append(out, tests[i].ID)
			}
		}
		sort.Strings(out)
		return out
	}
	ai, bi := ids(a), ids(b)
	for k := 0; k < len(ai) && k < len(bi); k++ {
		if ai[k] != bi[k] {
			return ai[k] < bi[k]
		}
	}
	return len(ai) < len(bi)
}
