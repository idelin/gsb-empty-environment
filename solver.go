package corpustrim

import (
	"fmt"
	"sort"
)

// Input is one corpus entry: a test/sample identified by its content,
// carrying the coverage edges it exercises, its execution cost, and the
// failure categories it has been observed to trigger.
type Input struct {
	ID                string   `json:"id"`
	Content           string   `json:"content"`
	Cost              int64    `json:"cost"`
	Coverage          []string `json:"coverage"`
	FailureCategory   string   `json:"failure_category,omitempty"`
	FailureCategories []string `json:"failure_categories,omitempty"`
}

// KeptItem is one selected (merged) input in the solution.
type KeptItem struct {
	IDs               []string `json:"ids"`
	Content           string   `json:"content"`
	Cost              int64    `json:"cost"`
	Coverage          []string `json:"coverage"`
	FailureCategories []string `json:"failure_categories"`
}

// Bound is a proved lower bound on the optimum.
type Bound struct {
	Samples int   `json:"samples"`
	Cost    int64 `json:"cost"`
}

// Result is the outcome of Trim.
type Result struct {
	Exact             bool       `json:"exact"`
	Samples           int        `json:"samples"`
	TotalCost         int64      `json:"total_cost"`
	Kept              []KeptItem `json:"kept"`
	LowerBound        Bound      `json:"lower_bound"`
	DuplicatesMerged  int        `json:"duplicates_merged"`
	CoverageEdges     int        `json:"coverage_edges"`
	FailureCategories int        `json:"failure_categories"`
	SearchNodes       int64      `json:"search_nodes"`
}

// group is a set of duplicate inputs merged by content.
type group struct {
	ids     []string
	content string
	cost    int64 // cheapest observed execution among duplicates
	cover   bitset
}

const maxInputs = 30

// Trim selects a minimum-cardinality (then minimum-cost) subset of inputs
// whose coverage edges and failure categories jointly cover everything
// observed in the whole corpus. It returns the exact optimum together with
// an independently computed lower bound.
func Trim(inputs []Input) (*Result, error) {
	if len(inputs) == 0 {
		return &Result{Exact: true, Kept: []KeptItem{}}, nil
	}
	if len(inputs) > maxInputs {
		return nil, fmt.Errorf("too many inputs: %d (max %d)", len(inputs), maxInputs)
	}
	for i, in := range inputs {
		if in.Cost < 0 {
			return nil, fmt.Errorf("input %d (%q): negative cost %d", i, in.ID, in.Cost)
		}
	}

	// Universe of elements to preserve: coverage edges + failure categories.
	elemIndex := map[string]int{}
	elemName := []string{}
	indexOf := func(name string) int {
		if idx, ok := elemIndex[name]; ok {
			return idx
		}
		idx := len(elemName)
		elemIndex[name] = idx
		elemName = append(elemName, name)
		return idx
	}

	// Merge duplicates by content, unioning everything each copy observed.
	byContent := map[string]*group{}
	order := []string{}
	dupes := 0
	for i, in := range inputs {
		id := in.ID
		if id == "" {
			id = fmt.Sprintf("input-%d", i)
		}
		g, ok := byContent[in.Content]
		if !ok {
			g = &group{content: in.Content, cost: in.Cost}
			byContent[in.Content] = g
			order = append(order, in.Content)
		} else {
			dupes++
			if in.Cost < g.cost {
				g.cost = in.Cost
			}
		}
		g.ids = append(g.ids, id)
	}
	// Second pass: build coverage masks (universe size known only after
	// all elements are registered, so register first, then fill).
	for _, in := range inputs {
		for _, e := range in.Coverage {
			indexOf("cov:" + e)
		}
		if in.FailureCategory != "" {
			indexOf("fail:" + in.FailureCategory)
		}
		for _, c := range in.FailureCategories {
			indexOf("fail:" + c)
		}
	}
	universe := newBitset(len(elemName))
	for i := 0; i < len(elemName); i++ {
		universe.set(i)
	}
	groups := make([]*group, 0, len(order))
	for _, key := range order {
		g := byContent[key]
		g.cover = newBitset(len(elemName))
		groups = append(groups, g)
	}
	for _, in := range inputs {
		g := byContent[in.Content]
		for _, e := range in.Coverage {
			g.cover.set(elemIndex["cov:"+e])
		}
		if in.FailureCategory != "" {
			g.cover.set(elemIndex["fail:"+in.FailureCategory])
		}
		for _, c := range in.FailureCategories {
			g.cover.set(elemIndex["fail:"+c])
		}
	}

	// Preprocessing: drop groups that can never belong to an optimum.
	groups = pruneGroups(groups)

	s := newSolver(groups, len(elemName))

	// Lower bound computed at the root, before any search.
	lbSamples := s.countLB(universe, s.allAvail)
	lbCost := s.costLB(s.allAvail, lbSamples)

	chosen := s.solve(universe)

	res := &Result{
		Exact:             true,
		Samples:           len(chosen),
		Kept:              []KeptItem{},
		DuplicatesMerged:  dupes,
		CoverageEdges:     countPrefix(elemName, "cov:"),
		FailureCategories: countPrefix(elemName, "fail:"),
		SearchNodes:       s.nodes,
	}
	res.LowerBound = Bound{Samples: lbSamples, Cost: lbCost}
	for _, gi := range chosen {
		g := groups[gi]
		item := KeptItem{
			IDs:               append([]string{}, g.ids...),
			Content:           g.content,
			Cost:              g.cost,
			Coverage:          []string{},
			FailureCategories: []string{},
		}
		sort.Strings(item.IDs)
		g.cover.foreach(func(e int) {
			name := elemName[e]
			if len(name) >= 4 && name[:4] == "cov:" {
				item.Coverage = append(item.Coverage, name[4:])
			} else {
				item.FailureCategories = append(item.FailureCategories, name[5:])
			}
		})
		res.TotalCost += g.cost
		res.Kept = append(res.Kept, item)
	}
	return res, nil
}

func countPrefix(names []string, prefix string) int {
	n := 0
	for _, s := range names {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}

// pruneGroups removes empty groups and groups strictly dominated by another
// (coverage subset, cost not smaller). Removing them cannot change the
// optimum: any solution using a dominated group can swap in its dominator.
func pruneGroups(groups []*group) []*group {
	kept := make([]*group, 0, len(groups))
	for i, gi := range groups {
		if gi.cover.empty() {
			continue
		}
		dominated := false
		for j, gj := range groups {
			if i == j || gj.cover.empty() {
				continue
			}
			if gi.cover.subsetOf(gj.cover) && gj.cost <= gi.cost {
				// Keep i only on exact ties with lower index to avoid
				// dropping both copies of identical groups.
				if gj.cost == gi.cost && gj.cover.subsetOf(gi.cover) && j > i {
					continue
				}
				dominated = true
				break
			}
		}
		if !dominated {
			kept = append(kept, gi)
		}
	}
	return kept
}

// solver is an exact branch-and-bound over the lexicographic objective
// (number of kept samples, total cost).
type solver struct {
	cover    []bitset // per-group coverage mask
	cost     []int64
	elemIn   []uint64 // per-element mask of groups covering it
	allAvail uint64
	order    []int // group indices sorted for branching

	bestCount int
	bestCost  int64
	bestMask  uint64
	found     bool

	nodes int64
}

func newSolver(groups []*group, numElems int) *solver {
	n := len(groups)
	s := &solver{
		cover:     make([]bitset, n),
		cost:      make([]int64, n),
		elemIn:    make([]uint64, numElems),
		bestCount: n + 1,
	}
	for i, g := range groups {
		s.cover[i] = g.cover
		s.cost[i] = g.cost
		s.allAvail |= 1 << uint(i)
		g.cover.foreach(func(e int) {
			s.elemIn[e] |= 1 << uint(i)
		})
	}
	return s
}

func (s *solver) better(count int, cost int64) bool {
	if !s.found {
		return true
	}
	if count != s.bestCount {
		return count < s.bestCount
	}
	return cost < s.bestCost
}

// countLB is a lower bound on the number of additional groups needed to
// cover `uncovered` using only groups in `avail`: the smallest k such that
// the k largest individual gains sum to at least |uncovered|.
func (s *solver) countLB(uncovered bitset, avail uint64) int {
	need := uncovered.popcount()
	if need == 0 {
		return 0
	}
	gains := make([]int, 0, 64)
	total := 0
	for i := 0; i < len(s.cover); i++ {
		if avail&(1<<uint(i)) == 0 {
			continue
		}
		g := s.cover[i].popcountAnd(uncovered)
		if g > 0 {
			gains = append(gains, g)
			total += g
		}
	}
	if total < need {
		return len(s.cover) + 1 // infeasible
	}
	sort.Sort(sort.Reverse(sort.IntSlice(gains)))
	sum, k := 0, 0
	for sum < need {
		sum += gains[k]
		k++
	}
	return k
}

// costLB is a lower bound on the additional cost of any completion that
// uses at least k more groups from `avail`: the k cheapest available costs.
func (s *solver) costLB(avail uint64, k int) int64 {
	costs := make([]int64, 0, 64)
	for i := 0; i < len(s.cost); i++ {
		if avail&(1<<uint(i)) != 0 {
			costs = append(costs, s.cost[i])
		}
	}
	sort.Slice(costs, func(a, b int) bool { return costs[a] < costs[b] })
	var sum int64
	for i := 0; i < k && i < len(costs); i++ {
		sum += costs[i]
	}
	return sum
}

// solve runs greedy heuristics for an incumbent, then exact search.
func (s *solver) solve(universe bitset) []int {
	s.greedy(universe, false)
	s.greedy(universe, true)
	s.search(universe, s.allAvail, 0, 0, 0)

	chosen := []int{}
	m := s.bestMask
	for m != 0 {
		i := 0
		for m&(1<<uint(i)) == 0 {
			i++
		}
		chosen = append(chosen, i)
		m &^= 1 << uint(i)
	}
	return chosen
}

// greedy builds an incumbent. If byRatio, picks max gain-per-cost,
// otherwise max gain (ties: cheaper).
func (s *solver) greedy(universe bitset, byRatio bool) {
	uncovered := universe.clone()
	avail := s.allAvail
	count := 0
	var cost int64
	var mask uint64
	for !uncovered.empty() {
		best, bestGain := -1, 0
		var bestCost int64
		for i := 0; i < len(s.cover); i++ {
			if avail&(1<<uint(i)) == 0 {
				continue
			}
			g := s.cover[i].popcountAnd(uncovered)
			if g == 0 {
				continue
			}
			take := false
			if best == -1 {
				take = true
			} else if byRatio {
				// compare g/cost(i) vs bestGain/bestCost via cross multiply
				l := g * int(s.cost[best]+1)
				r := bestGain * int(s.cost[i]+1)
				take = l > r || (l == r && s.cost[i] < s.cost[best])
			} else {
				take = g > bestGain || (g == bestGain && s.cost[i] < bestCost)
			}
			if take {
				best, bestGain, bestCost = i, g, s.cost[i]
			}
		}
		if best == -1 {
			return
		}
		avail &^= 1 << uint(best)
		mask |= 1 << uint(best)
		uncovered = uncovered.andNot(s.cover[best])
		count++
		cost += s.cost[best]
	}
	if s.better(count, cost) {
		s.found, s.bestCount, s.bestCost, s.bestMask = true, count, cost, mask
	}
}

// search branches on the hardest uncovered element: every feasible
// completion must include at least one available group covering it.
func (s *solver) search(uncovered bitset, avail uint64, chosen uint64, count int, cost int64) {
	s.nodes++
	if uncovered.empty() {
		if s.better(count, cost) {
			s.found, s.bestCount, s.bestCost, s.bestMask = true, count, cost, chosen
		}
		return
	}
	if s.found && count >= s.bestCount {
		return
	}
	k := s.countLB(uncovered, avail)
	if s.found {
		if count+k > s.bestCount {
			return
		}
		if count+k == s.bestCount && cost+s.costLB(avail, k) >= s.bestCost {
			return
		}
	}

	// Pick the uncovered element covered by the fewest available groups.
	selElem, selMask := -1, uint64(0)
	selCount := len(s.cover) + 1
	uncovered.foreach(func(e int) {
		if selCount == 1 {
			return
		}
		m := s.elemIn[e] & avail
		c := 0
		for x := m; x != 0; x &= x - 1 {
			c++
		}
		if c < selCount {
			selCount, selElem, selMask = c, e, m
		}
	})
	if selElem == -1 || selCount == 0 {
		return // infeasible: some element cannot be covered anymore
	}

	// Branch: try each available group covering selElem, larger gain first.
	cands := make([]int, 0, selCount)
	for x := selMask; x != 0; x &= x - 1 {
		i := 0
		for x&(1<<uint(i)) == 0 {
			i++
		}
		cands = append(cands, i)
	}
	sort.Slice(cands, func(a, b int) bool {
		ga := s.cover[cands[a]].popcountAnd(uncovered)
		gb := s.cover[cands[b]].popcountAnd(uncovered)
		if ga != gb {
			return ga > gb
		}
		return s.cost[cands[a]] < s.cost[cands[b]]
	})
	for _, i := range cands {
		if avail&(1<<uint(i)) == 0 {
			continue // already excluded by an earlier sibling branch
		}
		s.search(uncovered.andNot(s.cover[i]), avail&^(1<<uint(i)), chosen|(1<<uint(i)), count+1, cost+s.cost[i])
		avail &^= 1 << uint(i) // subsequent siblings must not use i
	}
}
