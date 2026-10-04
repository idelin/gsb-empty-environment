// Package binpack solves the bin packing problem with item conflicts:
// integer item sizes, a fixed bin capacity, and a conflict graph whose
// edges forbid two items from sharing a bin. It minimizes the number of
// bins and breaks ties by the lexicographically smallest normalized
// grouping. Search is budgeted by the number of expanded nodes; a
// budget-exhausted run never reports its incumbent as optimal.
package binpack

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrInfeasible marks instances that can never yield a valid packing
// (e.g. an item larger than the bin capacity).
var ErrInfeasible = errors.New("binpack: infeasible instance")

// Instance is a bin packing problem with conflicts.
type Instance struct {
	Capacity  int      // fixed capacity of every bin
	Sizes     []int    // Sizes[i] is the integer size of item i
	Conflicts [][2]int // undirected edges: endpoints may not share a bin
}

// Solution is the result of a Solve call. Bins and Loads are the per-bin
// loading evidence: Bins[b] lists the items of bin b in ascending order,
// bins are sorted lexicographically, and Loads[b] is the total size of
// bin b. LowerBound is a valid lower bound on the bin count. Optimal is
// true only when the search exhausted the space within the node budget,
// so Bins is provably optimal; otherwise Bins is just the best incumbent.
type Solution struct {
	Optimal    bool
	Bins       [][]int
	Loads      []int
	LowerBound int
	Nodes      int // expanded search nodes
}

// String renders the per-bin loading evidence.
func (s Solution) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "optimal=%v bins=%d lowerBound=%d nodes=%d",
		s.Optimal, len(s.Bins), s.LowerBound, s.Nodes)
	for i, bin := range s.Bins {
		fmt.Fprintf(&b, "\n  bin %d: items=%v load=%d", i, bin, s.Loads[i])
	}
	return b.String()
}

// Solve searches for a minimum-bin packing of inst. budget caps the
// number of expanded search nodes; a negative budget means unlimited.
// When the budget is exhausted, the returned Solution has Optimal=false
// and carries the best incumbent found so far (never flagged optimal).
func Solve(inst Instance, budget int) (Solution, error) {
	n := len(inst.Sizes)
	if inst.Capacity < 0 {
		return Solution{}, fmt.Errorf("%w: negative capacity %d", ErrInfeasible, inst.Capacity)
	}
	adj := make([][]bool, n)
	for i := range adj {
		adj[i] = make([]bool, n)
	}
	for _, e := range inst.Conflicts {
		a, b := e[0], e[1]
		if a < 0 || a >= n || b < 0 || b >= n {
			return Solution{}, fmt.Errorf("%w: conflict edge (%d,%d) out of range", ErrInfeasible, a, b)
		}
		if a == b {
			return Solution{}, fmt.Errorf("%w: item %d conflicts with itself", ErrInfeasible, a)
		}
		adj[a][b] = true
		adj[b][a] = true
	}
	total := 0
	for i, sz := range inst.Sizes {
		if sz < 0 {
			return Solution{}, fmt.Errorf("%w: item %d has negative size %d", ErrInfeasible, i, sz)
		}
		if sz > inst.Capacity {
			return Solution{}, fmt.Errorf("%w: item %d of size %d exceeds capacity %d",
				ErrInfeasible, i, sz, inst.Capacity)
		}
		total += sz
	}

	// Lower bound: max(capacity bound, conflict clique bound). The clique
	// bound is what keeps zero-size conflicting items honest.
	lb := 0
	if inst.Capacity > 0 {
		lb = ceilDiv(total, inst.Capacity)
	}
	if c := maxClique(adj); c > lb {
		lb = c
	}

	s := &solver{
		sizes:  inst.Sizes,
		cap:    inst.Capacity,
		adj:    adj,
		order:  orderItems(inst.Sizes),
		budget: budget,
		bestK:  n + 1,
	}
	// Seed the incumbent with a greedy first-fit packing so that even a
	// budget-aborted run returns feasible evidence. Seeding costs no
	// search nodes.
	s.best, s.bestK = s.greedy()
	s.dfs(0)

	sol := Solution{
		Optimal:    !s.aborted,
		Bins:       s.best,
		LowerBound: lb,
		Nodes:      s.nodes,
	}
	for _, bin := range s.best {
		load := 0
		for _, item := range bin {
			load += inst.Sizes[item]
		}
		sol.Loads = append(sol.Loads, load)
	}
	return sol, nil
}

// Verify checks that sol is a valid packing of inst: every item packed
// exactly once, capacities and conflicts respected, and the bin count
// consistent with the reported lower bound.
func (inst Instance) Verify(sol Solution) error {
	n := len(inst.Sizes)
	seen := make([]int, n)
	for b, bin := range sol.Bins {
		load := 0
		for i, item := range bin {
			if item < 0 || item >= n {
				return fmt.Errorf("bin %d: item index %d out of range", b, item)
			}
			seen[item]++
			if seen[item] > 1 {
				return fmt.Errorf("bin %d: item %d packed more than once", b, item)
			}
			load += inst.Sizes[item]
			for _, other := range bin[i+1:] {
				if inst.conflicts(item, other) {
					return fmt.Errorf("bin %d: conflicting items %d and %d share a bin", b, item, other)
				}
			}
		}
		if load > inst.Capacity {
			return fmt.Errorf("bin %d: load %d exceeds capacity %d", b, load, inst.Capacity)
		}
		if b < len(sol.Loads) && sol.Loads[b] != load {
			return fmt.Errorf("bin %d: reported load %d, actual %d", b, sol.Loads[b], load)
		}
	}
	for item := 0; item < n; item++ {
		if seen[item] == 0 {
			return fmt.Errorf("item %d was not packed", item)
		}
	}
	if len(sol.Loads) != len(sol.Bins) {
		return fmt.Errorf("loads cover %d bins, evidence covers %d", len(sol.Loads), len(sol.Bins))
	}
	if len(sol.Bins) < sol.LowerBound {
		return fmt.Errorf("bin count %d below reported lower bound %d", len(sol.Bins), sol.LowerBound)
	}
	return nil
}

func (inst Instance) conflicts(a, b int) bool {
	for _, e := range inst.Conflicts {
		if (e[0] == a && e[1] == b) || (e[0] == b && e[1] == a) {
			return true
		}
	}
	return false
}

type solver struct {
	sizes   []int
	cap     int
	adj     [][]bool
	order   []int // item indices, decreasing size
	budget  int
	nodes   int
	aborted bool

	bestK int
	best  [][]int // normalized incumbent

	loads []int
	bins  [][]int
}

// greedy computes a first-fit packing in the solver's item order.
func (s *solver) greedy() ([][]int, int) {
	var bins [][]int
	var loads []int
	for _, item := range s.order {
		placed := false
		for j := range bins {
			if loads[j]+s.sizes[item] > s.cap || !s.fits(bins[j], item) {
				continue
			}
			bins[j] = append(bins[j], item)
			loads[j] += s.sizes[item]
			placed = true
			break
		}
		if !placed {
			bins = append(bins, []int{item})
			loads = append(loads, s.sizes[item])
		}
	}
	return normalize(bins), len(bins)
}

func (s *solver) fits(bin []int, item int) bool {
	for _, m := range bin {
		if s.adj[item][m] {
			return false
		}
	}
	return true
}

// dfs assigns s.order[pos] to an existing bin or a fresh one, enumerating
// every feasible partition (branch and bound over set partitions).
func (s *solver) dfs(pos int) {
	if s.aborted {
		return
	}
	if s.budget >= 0 && s.nodes >= s.budget {
		s.aborted = true
		return
	}
	s.nodes++
	k := len(s.bins)
	if k > s.bestK {
		return
	}
	// Capacity relaxation bound: remaining items must fit into the free
	// space of open bins plus extra fresh bins.
	rem := 0
	for i := pos; i < len(s.order); i++ {
		rem += s.sizes[s.order[i]]
	}
	free := 0
	for _, l := range s.loads {
		free += s.cap - l
	}
	if need := rem - free; need > 0 && s.cap > 0 && k+ceilDiv(need, s.cap) > s.bestK {
		return
	}
	if pos == len(s.order) {
		cand := normalize(s.bins)
		if k < s.bestK || lessGrouping(cand, s.best) {
			s.bestK = k
			s.best = cand
		}
		return
	}
	item := s.order[pos]
	sz := s.sizes[item]
	for j := 0; j < k; j++ {
		if s.loads[j]+sz > s.cap || !s.fits(s.bins[j], item) {
			continue
		}
		s.loads[j] += sz
		s.bins[j] = append(s.bins[j], item)
		s.dfs(pos + 1)
		s.bins[j] = s.bins[j][:len(s.bins[j])-1]
		s.loads[j] -= sz
		if s.aborted {
			return
		}
	}
	if k+1 <= s.bestK {
		s.loads = append(s.loads, sz)
		s.bins = append(s.bins, []int{item})
		s.dfs(pos + 1)
		s.bins = s.bins[:k]
		s.loads = s.loads[:k]
	}
}

// orderItems sorts item indices by decreasing size, ties by index.
func orderItems(sizes []int) []int {
	order := make([]int, len(sizes))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		if sizes[order[a]] != sizes[order[b]] {
			return sizes[order[a]] > sizes[order[b]]
		}
		return order[a] < order[b]
	})
	return order
}

// normalize sorts items within each bin and bins lexicographically,
// yielding the canonical grouping used for tie-breaking.
func normalize(bins [][]int) [][]int {
	out := make([][]int, 0, len(bins))
	for _, bin := range bins {
		if len(bin) == 0 {
			continue
		}
		nb := make([]int, len(bin))
		copy(nb, bin)
		sort.Ints(nb)
		out = append(out, nb)
	}
	sort.Slice(out, func(i, j int) bool { return lessInts(out[i], out[j]) })
	return out
}

func lessInts(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func lessGrouping(a, b [][]int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if lessInts(a[i], b[i]) {
			return true
		}
		if lessInts(b[i], a[i]) {
			return false
		}
	}
	return len(a) < len(b)
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

// maxClique returns the size of a maximum clique of the conflict graph
// (Tomita's algorithm with greedy-coloring bounds). Any clique is a set
// of pairwise-conflicting items, so its size lower-bounds the bin count.
func maxClique(adj [][]bool) int {
	n := len(adj)
	if n == 0 {
		return 0
	}
	best := 0
	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	var expand func(cand []int, size int)
	expand = func(cand []int, size int) {
		if len(cand) == 0 {
			if size > best {
				best = size
			}
			return
		}
		// Greedy coloring of the candidate subgraph: color classes are
		// independent sets, so colors upper-bound any clique extension.
		remaining := make([]int, len(cand))
		copy(remaining, cand)
		order := make([]int, 0, len(cand))
		colors := make([]int, 0, len(cand))
		for color := 1; len(remaining) > 0; color++ {
			blocked := make([]bool, n)
			next := remaining[:0]
			for _, v := range remaining {
				if !blocked[v] {
					order = append(order, v)
					colors = append(colors, color)
					for u := 0; u < n; u++ {
						if adj[v][u] {
							blocked[u] = true
						}
					}
				} else {
					next = append(next, v)
				}
			}
			remaining = next
		}
		for i := len(order) - 1; i >= 0; i-- {
			if size+colors[i] <= best {
				return
			}
			v := order[i]
			nc := make([]int, 0, i)
			for j := 0; j < i; j++ {
				if adj[v][order[j]] {
					nc = append(nc, order[j])
				}
			}
			expand(nc, size+1)
		}
	}
	expand(all, 0)
	return best
}
