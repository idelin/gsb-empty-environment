package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// maxInputs is the hard limit on the number of raw inputs.
const maxInputs = 30

// rawInput is one input sample as decoded from JSON.
type rawInput struct {
	ID       string   `json:"id"`
	Content  string   `json:"content"`
	Cost     int64    `json:"cost"`
	Edges    []string `json:"edges"`
	Failure  string   `json:"failure"`
	Failures []string `json:"failures"`
}

func (r rawInput) failureSet() map[string]bool {
	s := map[string]bool{}
	if r.Failure != "" {
		s[r.Failure] = true
	}
	for _, f := range r.Failures {
		if f != "" {
			s[f] = true
		}
	}
	return s
}

// candidate is a set of duplicate inputs merged by content. Coverage edges and
// failure categories observed by any of the merged inputs are unioned so that
// no individually observed coverage is lost.
type candidate struct {
	ids     []string // all original ids, ids[0] is the representative
	content string
	cost    int64 // min cost among merged duplicates
	edges   map[string]bool
	fails   map[string]bool
	cover   bits // mask over the requirement universe (edges + failure categories)
}

// parseInputs decodes either a bare JSON array or an object with an "inputs"
// array, and validates the input count limit.
func parseInputs(data []byte) ([]rawInput, error) {
	var arr []rawInput
	if err := json.Unmarshal(data, &arr); err != nil {
		var obj struct {
			Inputs []rawInput `json:"inputs"`
		}
		if err2 := json.Unmarshal(data, &obj); err2 != nil || obj.Inputs == nil {
			return nil, fmt.Errorf("cannot parse input: %v", err)
		}
		arr = obj.Inputs
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("no inputs given")
	}
	if len(arr) > maxInputs {
		return nil, fmt.Errorf("too many inputs: got %d, max is %d", len(arr), maxInputs)
	}
	for i := range arr {
		if arr[i].ID == "" {
			arr[i].ID = fmt.Sprintf("input-%d", i)
		}
		if arr[i].Cost < 0 {
			return nil, fmt.Errorf("input %q: negative cost %d", arr[i].ID, arr[i].Cost)
		}
	}
	return arr, nil
}

// duplicatePair records that merged was folded into kept.
type duplicatePair struct {
	Kept   string `json:"kept"`
	Merged string `json:"merged"`
}

// mergeDuplicates groups inputs by content, unioning their observed coverage
// edges and failure categories and keeping the minimum cost.
func mergeDuplicates(inputs []rawInput) ([]*candidate, []duplicatePair) {
	byContent := map[string]*candidate{}
	var order []*candidate
	var dups []duplicatePair
	for _, in := range inputs {
		c, ok := byContent[in.Content]
		if !ok {
			c = &candidate{
				ids:     []string{in.ID},
				content: in.Content,
				cost:    in.Cost,
				edges:   map[string]bool{},
				fails:   map[string]bool{},
			}
			byContent[in.Content] = c
			order = append(order, c)
		} else {
			dups = append(dups, duplicatePair{Kept: c.ids[0], Merged: in.ID})
			c.ids = append(c.ids, in.ID)
			if in.Cost < c.cost {
				c.cost = in.Cost
			}
		}
		for _, e := range in.Edges {
			c.edges[e] = true
		}
		for f := range in.failureSet() {
			c.fails[f] = true
		}
	}
	return order, dups
}

// buildUniverse assigns every coverage edge and failure category a requirement
// index and fills each candidate's coverage mask.
func buildUniverse(cands []*candidate) (int, map[string]int) {
	reqIndex := map[string]int{}
	n := 0
	for _, c := range cands {
		for e := range c.edges {
			key := "edge:" + e
			if _, ok := reqIndex[key]; !ok {
				reqIndex[key] = n
				n++
			}
		}
		for f := range c.fails {
			key := "fail:" + f
			if _, ok := reqIndex[key]; !ok {
				reqIndex[key] = n
				n++
			}
		}
	}
	for _, c := range cands {
		c.cover = newBits(n)
		for e := range c.edges {
			c.cover.set(reqIndex["edge:"+e])
		}
		for f := range c.fails {
			c.cover.set(reqIndex["fail:"+f])
		}
	}
	return n, reqIndex
}

// solver is an exact branch-and-bound over the lexicographic objective
// (number of selected inputs, total cost).
type solver struct {
	cands      []*candidate
	nreq       int
	forced     []int // essentials fixed by reduce()
	forcedCost int64
	bestSel    []int
	bestCnt    int
	bestCost   int64
	nodes      int64
}

func newSolver(cands []*candidate, nreq int) *solver {
	return &solver{cands: cands, nreq: nreq, bestCnt: math.MaxInt32, bestCost: math.MaxInt64}
}

// solve returns the selected candidate indices. It assumes every requirement
// is coverable by at least one candidate.
func (s *solver) solve() []int {
	full := newBits(s.nreq)
	for i := 0; i < s.nreq; i++ {
		full.set(i)
	}
	avail := make([]int, len(s.cands))
	for i := range s.cands {
		avail[i] = i
	}
	uncovered, avail := s.reduce(full, avail)
	s.seedGreedy(uncovered, avail)
	s.search(uncovered, avail, nil, 0)
	return s.bestSel
}

// reduce iteratively applies essential-candidate selection and dominated
// candidate elimination until a fixpoint. Selected essentials are recorded in
// s.forced and prepended to every solution; reduce returns the remaining
// uncovered requirements and the remaining available candidates.
func (s *solver) reduce(uncovered bits, avail []int) (bits, []int) {
	for {
		changed := false
		// Essential: a requirement covered by exactly one available candidate.
		ess, infeasible := s.findEssential(uncovered, avail)
		if infeasible {
			return uncovered, avail
		}
		if ess >= 0 {
			s.forced = append(s.forced, ess)
			s.forcedCost += s.cands[ess].cost
			uncovered = uncovered.andNot(s.cands[ess].cover)
			avail = removeInt(avail, ess)
			changed = true
			continue
		}
		// Dominated: candidate i is never better than candidate j.
		for _, i := range append([]int(nil), avail...) {
			ci := s.cands[i]
			dominated := false
			for _, j := range avail {
				if i == j {
					continue
				}
				cj := s.cands[j]
				if ci.cover.subsetOf(cj.cover) && cj.cost <= ci.cost &&
					(!cj.cover.subsetOf(ci.cover) || cj.cost < ci.cost || j < i) {
					dominated = true
					break
				}
			}
			if dominated {
				avail = removeInt(avail, i)
				changed = true
			}
		}
		if !changed {
			return uncovered, avail
		}
	}
}

// findEssential returns the index of a candidate that is the sole coverer of
// some uncovered requirement, -1 if none. infeasible is true if some
// requirement has no covering candidate at all.
func (s *solver) findEssential(uncovered bits, avail []int) (ess int, infeasible bool) {
	ess = -1
	for r := 0; r < s.nreq; r++ {
		if !uncovered.get(r) {
			continue
		}
		cnt, last := 0, -1
		for _, ci := range avail {
			if s.cands[ci].cover.get(r) {
				cnt++
				last = ci
			}
		}
		if cnt == 0 {
			return -1, true
		}
		if cnt == 1 {
			return last, false
		}
	}
	return -1, false
}

// seedGreedy finds an initial feasible solution to warm up the bound.
func (s *solver) seedGreedy(uncovered bits, avail []int) {
	uncov := uncovered.clone()
	var sel []int
	var cost int64
	av := append([]int(nil), avail...)
	for !uncov.empty() {
		best, bestGain, bestCost := -1, -1, int64(math.MaxInt64)
		for _, ci := range av {
			gain := uncov.and(s.cands[ci].cover).popcount()
			if gain > bestGain || (gain == bestGain && s.cands[ci].cost < bestCost) {
				best, bestGain, bestCost = ci, gain, s.cands[ci].cost
			}
		}
		if best < 0 || bestGain == 0 {
			return // infeasible; leave the bound unset
		}
		sel = append(sel, best)
		cost += s.cands[best].cost
		uncov = uncov.andNot(s.cands[best].cover)
		av = removeInt(av, best)
	}
	s.updateBest(sel, cost)
}

func (s *solver) updateBest(sel []int, cost int64) {
	total := len(s.forced) + len(sel)
	totalCost := s.forcedCost + cost
	if total < s.bestCnt || (total == s.bestCnt && totalCost < s.bestCost) {
		s.bestCnt = total
		s.bestCost = totalCost
		s.bestSel = append(append([]int(nil), s.forced...), sel...)
	}
}

func (s *solver) search(uncovered bits, avail []int, sel []int, cost int64) {
	s.nodes++
	// Essential extraction inside the search tree.
	for {
		if uncovered.empty() {
			s.updateBest(sel, cost)
			return
		}
		ess, infeasible := s.findEssential(uncovered, avail)
		if infeasible {
			return
		}
		if ess < 0 {
			break
		}
		sel = append(sel, ess)
		cost += s.cands[ess].cost
		uncovered = uncovered.andNot(s.cands[ess].cover)
		avail = removeInt(avail, ess)
	}

	// Lower bounds on the number of additional inputs needed.
	lb := s.packingLB(uncovered, avail)
	if clb := ceilDiv(uncovered.popcount(), s.maxCover(uncovered, avail)); clb > lb {
		lb = clb
	}
	done := len(s.forced) + len(sel)
	if done+lb > s.bestCnt {
		return
	}
	if done+lb == s.bestCnt && cost+s.costLB(lb, avail) >= s.bestCost-s.forcedCost {
		return
	}

	// Branch on the requirement with the fewest covering candidates.
	branchReq, branchCands := s.branchReq(uncovered, avail)
	_ = branchReq
	// Try the most promising candidates first: more marginal coverage, cheaper.
	sort.Slice(branchCands, func(a, b int) bool {
		ca, cb := s.cands[branchCands[a]], s.cands[branchCands[b]]
		ga, gb := uncovered.and(ca.cover).popcount(), uncovered.and(cb.cover).popcount()
		if ga != gb {
			return ga > gb
		}
		return ca.cost < cb.cost
	})
	for _, ci := range branchCands {
		s.search(uncovered.andNot(s.cands[ci].cover), removeInt(avail, ci),
			append(append([]int(nil), sel...), ci), cost+s.cands[ci].cost)
	}
}

// branchReq picks an uncovered requirement covered by the fewest available
// candidates and returns it together with those candidates.
func (s *solver) branchReq(uncovered bits, avail []int) (int, []int) {
	bestReq := -1
	var bestList []int
	for r := 0; r < s.nreq; r++ {
		if !uncovered.get(r) {
			continue
		}
		var list []int
		for _, ci := range avail {
			if s.cands[ci].cover.get(r) {
				list = append(list, ci)
			}
		}
		if bestReq < 0 || len(list) < len(bestList) {
			bestReq, bestList = r, list
			if len(list) == 2 {
				break // cannot do better after essential extraction
			}
		}
	}
	return bestReq, bestList
}

// packingLB greedily builds a set of uncovered requirements no two of which
// can be covered by a single available candidate; its size is a lower bound on
// the number of additional inputs required.
func (s *solver) packingLB(uncovered bits, avail []int) int {
	remaining := uncovered.clone()
	lb := 0
	for !remaining.empty() {
		r := remaining.first()
		lb++
		kill := newBits(s.nreq)
		for _, ci := range avail {
			if s.cands[ci].cover.get(r) {
				kill = kill.or(s.cands[ci].cover)
			}
		}
		remaining = remaining.andNot(kill)
	}
	return lb
}

// maxCover returns the maximum number of uncovered requirements any single
// available candidate covers.
func (s *solver) maxCover(uncovered bits, avail []int) int {
	m := 0
	for _, ci := range avail {
		if g := uncovered.and(s.cands[ci].cover).popcount(); g > m {
			m = g
		}
	}
	return m
}

// costLB returns a lower bound on the additional cost of any solution that
// adds at least k more inputs: the sum of the k cheapest available costs.
func (s *solver) costLB(k int, avail []int) int64 {
	if k <= 0 {
		return 0
	}
	costs := make([]int64, len(avail))
	for i, ci := range avail {
		costs[i] = s.cands[ci].cost
	}
	sort.Slice(costs, func(a, b int) bool { return costs[a] < costs[b] })
	var sum int64
	for i := 0; i < k && i < len(costs); i++ {
		sum += costs[i]
	}
	return sum
}

func ceilDiv(a, b int) int {
	if b <= 0 {
		return math.MaxInt32
	}
	return (a + b - 1) / b
}

func removeInt(s []int, v int) []int {
	out := make([]int, 0, len(s)-1)
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
