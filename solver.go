// Package binsolve solves bin packing with integer item sizes, a fixed bin
// capacity, and a pairwise conflict graph: conflicting items may never share
// a bin. It minimizes the number of bins; ties are broken by the
// lexicographically smallest normalized grouping (each bin sorted ascending,
// bins sorted lexicographically). Search is budgeted by expanded nodes and
// never reports the first incumbent as optimal.
package binsolve

import (
	"fmt"
	"sort"
)

// exhaustiveThreshold is the maximum item count solved by exhaustive set
// partition enumeration (restricted growth strings in natural index order).
const exhaustiveThreshold = 12

// Problem describes one bin packing instance.
type Problem struct {
	Sizes     []int    // integer size of each item, 0 <= Sizes[i] <= Capacity
	Capacity  int      // fixed capacity of every bin
	Conflicts [][2]int // unordered pairs of item indices that may not share a bin
}

// BinEvidence is the per-bin loading proof for one packed bin.
type BinEvidence struct {
	Items []int // item indices in ascending order
	Load  int   // sum of item sizes, always <= Capacity
}

// Solution is the solver output. Bins is the normalized grouping and
// Evidence its per-bin loading proof. Optimal is true only when the search
// completed within the node budget; an incumbent found before the budget ran
// out is returned with Optimal == false and BudgetExhausted == true.
type Solution struct {
	Feasible        bool
	Optimal         bool
	BudgetExhausted bool
	Reason          string // set when Feasible is false
	Bins            [][]int
	Evidence        []BinEvidence
	LowerBound      int // valid lower bound on the bin count
	NodesExpanded   int
}

// Solve dispatches to exhaustive partition enumeration for small instances
// and to branch and bound (size-descending order) otherwise. nodeBudget caps
// the number of expanded search nodes; nodeBudget < 0 means unlimited.
func Solve(p Problem, nodeBudget int) Solution {
	inst, err := newInstance(p)
	if err != nil {
		return Solution{Feasible: false, Reason: err.Error()}
	}
	lb := inst.lowerBound()
	var sol Solution
	if inst.n <= exhaustiveThreshold {
		sol = solveOrdered(inst, naturalOrder(inst.n), nodeBudget)
	} else {
		sol = solveOrdered(inst, sizeDescendingOrder(inst), nodeBudget)
	}
	sol.LowerBound = lb
	if sol.Feasible {
		sol.Evidence = buildEvidence(inst, sol.Bins)
	}
	return sol
}

// Verify checks that s is a valid loading of p: every item packed exactly
// once, every bin within capacity, no conflicting pair sharing a bin, bins in
// normalized form, evidence consistent, and the bin count not below the
// reported lower bound.
func (s Solution) Verify(p Problem) error {
	inst, verr := newInstance(p)
	if !s.Feasible {
		if verr == nil {
			return fmt.Errorf("solution claims infeasible but the problem is valid")
		}
		return nil
	}
	if verr != nil {
		return fmt.Errorf("problem is invalid (%v) but solution claims feasible", verr)
	}
	if s.Optimal && s.BudgetExhausted {
		return fmt.Errorf("optimal claimed despite exhausted budget")
	}
	seen := make([]bool, inst.n)
	for bi, bin := range s.Bins {
		if len(bin) == 0 {
			return fmt.Errorf("bin %d is empty", bi)
		}
		load := 0
		for _, it := range bin {
			if it < 0 || it >= inst.n {
				return fmt.Errorf("bin %d packs unknown item %d", bi, it)
			}
			if seen[it] {
				return fmt.Errorf("item %d packed more than once", it)
			}
			seen[it] = true
			load += inst.sizes[it]
		}
		if load > inst.capacity {
			return fmt.Errorf("bin %d load %d exceeds capacity %d", bi, load, inst.capacity)
		}
		for x := 0; x < len(bin); x++ {
			for y := x + 1; y < len(bin); y++ {
				if inst.conflict[bin[x]][bin[y]] {
					return fmt.Errorf("bin %d packs conflicting items %d and %d", bi, bin[x], bin[y])
				}
			}
		}
		if bi < len(s.Evidence) {
			ev := s.Evidence[bi]
			if ev.Load != load || !equalInts(ev.Items, bin) {
				return fmt.Errorf("bin %d evidence does not match loading", bi)
			}
		}
	}
	for it, ok := range seen {
		if !ok {
			return fmt.Errorf("item %d is not packed", it)
		}
	}
	if !isCanonical(s.Bins) {
		return fmt.Errorf("bins are not in normalized canonical order")
	}
	if len(s.Bins) < s.LowerBound {
		return fmt.Errorf("bin count %d below reported lower bound %d", len(s.Bins), s.LowerBound)
	}
	return nil
}

type instance struct {
	n        int
	sizes    []int
	capacity int
	conflict [][]bool
}

func newInstance(p Problem) (*instance, error) {
	if p.Capacity < 0 {
		return nil, fmt.Errorf("negative capacity %d", p.Capacity)
	}
	n := len(p.Sizes)
	inst := &instance{
		n:        n,
		sizes:    p.Sizes,
		capacity: p.Capacity,
		conflict: make([][]bool, n),
	}
	for i := range inst.conflict {
		inst.conflict[i] = make([]bool, n)
	}
	for i, sz := range p.Sizes {
		if sz < 0 {
			return nil, fmt.Errorf("item %d has negative size %d", i, sz)
		}
		if sz > p.Capacity {
			return nil, fmt.Errorf("item %d of size %d exceeds capacity %d", i, sz, p.Capacity)
		}
	}
	for _, c := range p.Conflicts {
		a, b := c[0], c[1]
		if a < 0 || a >= n || b < 0 || b >= n {
			return nil, fmt.Errorf("conflict (%d,%d) references unknown item", a, b)
		}
		if a == b {
			return nil, fmt.Errorf("item %d conflicts with itself", a)
		}
		inst.conflict[a][b] = true
		inst.conflict[b][a] = true
	}
	return inst, nil
}

func (inst *instance) conflictsWith(bin []int, item int) bool {
	for _, m := range bin {
		if inst.conflict[item][m] {
			return true
		}
	}
	return false
}

// lowerBound returns max(ceil(totalSize/capacity), maxClique(conflicts)).
// The clique term keeps the bound valid for zero-size conflicting items.
func (inst *instance) lowerBound() int {
	if inst.n == 0 {
		return 0
	}
	sum := 0
	for _, sz := range inst.sizes {
		sum += sz
	}
	lb := 0
	if inst.capacity > 0 {
		lb = ceilDiv(sum, inst.capacity)
	}
	if c := maxClique(inst.conflict); c > lb {
		lb = c
	}
	return lb
}

func maxClique(conflict [][]bool) int {
	n := len(conflict)
	best := 0
	cand := make([]int, n)
	for i := range cand {
		cand[i] = i
	}
	var expand func(cand []int, size int)
	expand = func(cand []int, size int) {
		if size+len(cand) <= best {
			return
		}
		if len(cand) == 0 {
			best = size
			return
		}
		for i := len(cand) - 1; i >= 0; i-- {
			if size+i+1 <= best {
				return
			}
			v := cand[i]
			next := make([]int, 0, i)
			for _, u := range cand[:i] {
				if conflict[v][u] {
					next = append(next, u)
				}
			}
			expand(next, size+1)
		}
	}
	expand(cand, 0)
	return best
}

func naturalOrder(n int) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	return order
}

func sizeDescendingOrder(inst *instance) []int {
	order := naturalOrder(inst.n)
	sort.Slice(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if inst.sizes[ia] != inst.sizes[ib] {
			return inst.sizes[ia] > inst.sizes[ib]
		}
		return ia < ib
	})
	return order
}

// greedy packs items in the given order by first fit. It only seeds the
// incumbent; the result is never reported as optimal.
func (inst *instance) greedy(order []int) [][]int {
	var bins [][]int
	var loads []int
	for _, it := range order {
		placed := false
		for j := range bins {
			if loads[j]+inst.sizes[it] <= inst.capacity && !inst.conflictsWith(bins[j], it) {
				bins[j] = append(bins[j], it)
				loads[j] += inst.sizes[it]
				placed = true
				break
			}
		}
		if !placed {
			bins = append(bins, []int{it})
			loads = append(loads, inst.sizes[it])
		}
	}
	return bins
}

// search enumerates set partitions of the items in the given assignment
// order. Bins are unlabeled: symmetry is broken by numbering bins in order of
// creation, so each partition is visited exactly once.
type search struct {
	inst      *instance
	order     []int
	rem       []int // suffix sums of sizes in assignment order
	budget    int
	nodes     int
	aborted   bool
	bins      [][]int
	loads     []int
	best      [][]int
	bestCount int
}

func solveOrdered(inst *instance, order []int, budget int) Solution {
	s := &search{inst: inst, order: order, budget: budget}
	s.rem = make([]int, inst.n+1)
	for k := inst.n - 1; k >= 0; k-- {
		s.rem[k] = s.rem[k+1] + inst.sizes[order[k]]
	}
	seed := inst.greedy(order)
	s.best = canonical(seed)
	s.bestCount = len(s.best)
	s.expand(0)
	sol := Solution{
		Feasible:      true,
		Bins:          s.best,
		NodesExpanded: s.nodes,
	}
	if s.aborted {
		sol.BudgetExhausted = true
	} else {
		sol.Optimal = true
	}
	return sol
}

func (s *search) expand(k int) {
	if s.aborted {
		return
	}
	s.nodes++
	if s.budget >= 0 && s.nodes > s.budget {
		s.aborted = true
		return
	}
	if k == s.inst.n {
		c := canonical(s.bins)
		if len(c) < s.bestCount || (len(c) == s.bestCount && lessBins(c, s.best)) {
			s.best = c
			s.bestCount = len(c)
		}
		return
	}
	if len(s.loads) > s.bestCount {
		return
	}
	// Capacity bound: remaining size that does not fit into the free space of
	// open bins forces additional bins.
	free := 0
	for _, l := range s.loads {
		free += s.inst.capacity - l
	}
	if rem := s.rem[k]; rem > free && s.inst.capacity > 0 {
		if len(s.loads)+ceilDiv(rem-free, s.inst.capacity) > s.bestCount {
			return
		}
	}
	it := s.order[k]
	sz := s.inst.sizes[it]
	for b := 0; b < len(s.bins); b++ {
		if s.loads[b]+sz > s.inst.capacity || s.inst.conflictsWith(s.bins[b], it) {
			continue
		}
		s.bins[b] = append(s.bins[b], it)
		s.loads[b] += sz
		s.expand(k + 1)
		s.loads[b] -= sz
		s.bins[b] = s.bins[b][:len(s.bins[b])-1]
		if s.aborted {
			return
		}
	}
	// Open one new bin (bins are unlabeled, so a single choice suffices).
	if len(s.bins)+1 <= s.bestCount {
		s.bins = append(s.bins, []int{it})
		s.loads = append(s.loads, sz)
		s.expand(k + 1)
		s.loads = s.loads[:len(s.loads)-1]
		s.bins = s.bins[:len(s.bins)-1]
	}
}

func buildEvidence(inst *instance, bins [][]int) []BinEvidence {
	ev := make([]BinEvidence, len(bins))
	for i, bin := range bins {
		load := 0
		for _, it := range bin {
			load += inst.sizes[it]
		}
		items := make([]int, len(bin))
		copy(items, bin)
		ev[i] = BinEvidence{Items: items, Load: load}
	}
	return ev
}

// canonical normalizes a grouping: items within each bin sorted ascending,
// bins sorted lexicographically.
func canonical(bins [][]int) [][]int {
	out := make([][]int, len(bins))
	for i, bin := range bins {
		cp := make([]int, len(bin))
		copy(cp, bin)
		sort.Ints(cp)
		out[i] = cp
	}
	sort.Slice(out, func(a, b int) bool { return compareInts(out[a], out[b]) < 0 })
	return out
}

func isCanonical(bins [][]int) bool {
	for i, bin := range bins {
		if !sort.IntsAreSorted(bin) {
			return false
		}
		if i > 0 && compareInts(bins[i-1], bin) > 0 {
			return false
		}
	}
	return true
}

// lessBins compares two normalized groupings lexicographically, bin by bin.
func lessBins(a, b [][]int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareInts(a[i], b[i]); c != 0 {
			return c < 0
		}
	}
	return len(a) < len(b)
}

func compareInts(a, b []int) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func equalInts(a, b []int) bool {
	return compareInts(a, b) == 0
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}
