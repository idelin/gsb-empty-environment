package main

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
)

// Input is the problem instance: targets must all be covered, forbidden points
// must be avoided by every rectangle.
type Input struct {
	Targets   []Point `json:"targets"`
	Forbidden []Point `json:"forbidden"`
}

// Options tunes the exact search.
type Options struct {
	// DPBudget is the maximum number of targets solved by subset DP.
	// Above this, branch-and-bound is used. Default 22.
	DPBudget int
	// MaxNodes caps branch-and-bound search nodes (0 = unlimited).
	// When the budget is exhausted the search is interrupted and the best
	// solution found so far (if any) is returned with Interrupted=true.
	MaxNodes int64
	// DominationPruneLimit enables O(C^2) domination pruning when the
	// candidate count is at most this. Default 4000. Negative disables.
	DominationPruneLimit int
	// BruteForceLimit caps candidates for BruteForce. Default 22.
	BruteForceLimit int
}

func (o *Options) withDefaults() {
	if o.DPBudget <= 0 {
		o.DPBudget = 22
	}
	if o.DominationPruneLimit == 0 {
		o.DominationPruneLimit = 4000
	}
	if o.BruteForceLimit == 0 {
		o.BruteForceLimit = 22
	}
}

// RectSol is one chosen rectangle with the targets it covers.
type RectSol struct {
	Rect   Rect  `json:"rect"`
	Area   int64 `json:"area"`
	Covers []int `json:"covers"` // target indices
}

// Evidence records, per target, which solution rectangles cover it.
type Evidence struct {
	Target Point `json:"target"`
	Rects  []int `json:"rects"` // indices into Result.Rects
}

// Result is the solver output.
type Result struct {
	Feasible    bool       `json:"feasible"`
	Uncoverable []Point    `json:"uncoverable,omitempty"` // gaps / isolated targets
	Rects       []RectSol  `json:"rects,omitempty"`
	TotalArea   int64      `json:"totalArea"`
	RectCount   int64      `json:"rectCount"`
	Evidence    []Evidence `json:"evidence,omitempty"`
	Method      string     `json:"method,omitempty"`
	Candidates  int        `json:"candidates"`
	Pruned      int        `json:"pruned"`
	Nodes       int64      `json:"nodes"`
	Interrupted bool       `json:"interrupted"`
}

// Solve runs the full pipeline: normalize, enumerate candidates, prune,
// exact search, and evidence assembly.
func Solve(in Input, opt Options) Result {
	opt.withDefaults()
	targets := dedupePoints(in.Targets)
	forbidden := dedupePoints(in.Forbidden)

	res := Result{Feasible: true}

	if len(targets) == 0 {
		res.Method = "trivial"
		return res
	}

	// Targets coinciding with forbidden points can never be covered.
	fset := make(map[Point]bool, len(forbidden))
	for _, f := range forbidden {
		fset[f] = true
	}
	var kept []Point
	for _, t := range targets {
		if fset[t] {
			res.Uncoverable = append(res.Uncoverable, t)
		} else {
			kept = append(kept, t)
		}
	}
	targets = kept
	if len(res.Uncoverable) > 0 {
		res.Feasible = false
	}
	if len(targets) == 0 {
		return res
	}

	cands := enumerateCandidates(targets, forbidden)
	raw := len(cands)
	cands = dedupeCandidates(cands)
	if opt.DominationPruneLimit >= 0 && len(cands) <= opt.DominationPruneLimit {
		cands = pruneDominated(cands)
	}
	res.Candidates = len(cands)
	res.Pruned = raw - len(cands)

	// Coverage check: any target no candidate can cover is a gap / isolated point.
	coverCount := make([]int, len(targets))
	for _, c := range cands {
		for i := range targets {
			if c.cover.test(i) {
				coverCount[i]++
			}
		}
	}
	uncovered := false
	for i, n := range coverCount {
		if n == 0 {
			res.Uncoverable = append(res.Uncoverable, targets[i])
			uncovered = true
		}
	}
	if uncovered {
		res.Feasible = false
		return res
	}
	if !res.Feasible {
		// Already infeasible due to forbidden-target overlap; still report.
		return res
	}

	var chosen []int
	var cost Cost
	if len(targets) <= opt.DPBudget {
		chosen, cost = solveDP(targets, cands)
		res.Method = "dp"
		res.Nodes = 1 << uint(len(targets))
	} else {
		b := newBnB(targets, cands, opt.MaxNodes)
		chosen, cost = b.run()
		res.Method = "bnb"
		res.Nodes = b.nodes
		res.Interrupted = b.interrupted
		if chosen == nil {
			res.Feasible = false // budget exhausted before any full cover
			return res
		}
	}

	res.TotalArea = cost.Area
	res.RectCount = cost.Count
	res.Rects = make([]RectSol, 0, len(chosen))
	for _, ci := range chosen {
		c := cands[ci]
		rs := RectSol{Rect: c.rect, Area: c.area}
		for i := range targets {
			if c.cover.test(i) {
				rs.Covers = append(rs.Covers, i)
			}
		}
		res.Rects = append(res.Rects, rs)
	}
	res.Evidence = buildEvidence(targets, res.Rects)
	return res
}

func dedupePoints(pts []Point) []Point {
	seen := make(map[Point]bool, len(pts))
	out := pts[:0]
	for _, p := range pts {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func buildEvidence(targets []Point, rects []RectSol) []Evidence {
	ev := make([]Evidence, len(targets))
	for i, t := range targets {
		ev[i].Target = t
	}
	for ri, r := range rects {
		for _, ti := range r.Covers {
			ev[ti].Rects = append(ev[ti].Rects, ri)
		}
	}
	return ev
}

// ---- subset DP over targets (exact, for small target counts) ----

func solveDP(targets []Point, cands []candidate) ([]int, Cost) {
	n := len(targets)
	size := 1 << uint(n)
	const inf = int64(1) << 62
	area := make([]int64, size)
	count := make([]int32, size)
	choice := make([]int32, size)
	for i := range area {
		area[i] = inf
	}
	area[0] = 0

	// candidates covering each target (single-word masks, n <= 64)
	cover := make([]uint64, len(cands))
	byTarget := make([][]int, n)
	for ci, c := range cands {
		var m uint64
		for i := 0; i < n; i++ {
			if c.cover.test(i) {
				m |= 1 << uint(i)
			}
		}
		cover[ci] = m
		for i := 0; i < n; i++ {
			if m&(1<<uint(i)) != 0 {
				byTarget[i] = append(byTarget[i], ci)
			}
		}
	}

	full := uint64(size - 1)
	for mask := uint64(0); mask < full; mask++ {
		if area[mask] == inf {
			continue
		}
		// first uncovered target
		var t int
		for t = 0; t < n; t++ {
			if mask&(1<<uint(t)) == 0 {
				break
			}
		}
		if t == n {
			continue
		}
		for _, ci := range byTarget[t] {
			nm := mask | cover[ci]
			na := area[mask] + cands[ci].area
			nc := count[mask] + 1
			if na < area[nm] || (na == area[nm] && nc < count[nm]) {
				area[nm] = na
				count[nm] = nc
				choice[nm] = int32(ci)
			}
		}
	}

	// reconstruct
	var chosen []int
	mask := full
	for mask != 0 {
		ci := int(choice[mask])
		chosen = append(chosen, ci)
		mask &^= cover[ci]
	}
	return chosen, Cost{Area: area[full], Count: int64(count[full])}
}

// ---- branch and bound with node budget ----

type bnb struct {
	targets  []Point
	cands    []candidate
	maxNodes int64

	nodes       int64
	interrupted bool

	byTarget [][]int // candidate indices covering each target, sorted by area
	minArea  []int64 // min candidate area per target

	best       Cost
	bestChosen []int
	curChosen  []int
}

func newBnB(targets []Point, cands []candidate, maxNodes int64) *bnb {
	b := &bnb{
		targets:  targets,
		cands:    cands,
		maxNodes: maxNodes,
	}
	b.byTarget = make([][]int, len(targets))
	b.minArea = make([]int64, len(targets))
	singleton := make([]int, len(targets))
	for i := range b.minArea {
		b.minArea[i] = int64(1) << 62
		singleton[i] = -1
	}
	for ci, c := range cands {
		for i := range targets {
			if c.cover.test(i) {
				b.byTarget[i] = append(b.byTarget[i], ci)
				if c.area < b.minArea[i] {
					b.minArea[i] = c.area
				}
				if c.area == 1 {
					singleton[i] = ci
				}
			}
		}
	}
	for i := range b.byTarget {
		list := b.byTarget[i]
		sort.Slice(list, func(a, c int) bool { return cands[list[a]].area < cands[list[c]].area })
	}
	// Seed incumbent: all-singletons is always feasible (targets are not
	// forbidden), with area == count == #targets.
	b.best = Cost{Area: int64(len(targets)), Count: int64(len(targets))}
	for _, ci := range singleton {
		b.bestChosen = append(b.bestChosen, ci)
	}
	return b
}

func (b *bnb) run() ([]int, Cost) {
	covered := newBitset(len(b.targets))
	b.dfs(covered, Cost{})
	if b.bestChosen == nil {
		return nil, Cost{}
	}
	out := make([]int, len(b.bestChosen))
	copy(out, b.bestChosen)
	return out, b.best
}

// lowerBoundArea is admissible: every remaining rectangle has area at least
// the number of yet-uncovered targets it covers, so the remaining area is at
// least the uncovered count; it is also at least the cheapest rectangle
// needed for any single uncovered target.
func (b *bnb) lowerBoundArea(uncovered bitset) int64 {
	lb := int64(popcount(uncovered))
	for i := range b.targets {
		if uncovered.test(i) && b.minArea[i] > lb {
			lb = b.minArea[i]
		}
	}
	return lb
}

func popcount(b bitset) int {
	n := 0
	for _, w := range b.w {
		n += bits.OnesCount64(w)
	}
	return n
}

func (b *bnb) dfs(covered bitset, cur Cost) {
	if b.maxNodes > 0 && b.nodes >= b.maxNodes {
		b.interrupted = true
		return
	}
	b.nodes++

	uncovered := newBitset(len(b.targets))
	allCovered := true
	for i := range b.targets {
		if !covered.test(i) {
			uncovered.set(i)
			allCovered = false
		}
	}
	if allCovered {
		if lessCost(cur, b.best) {
			b.best = cur
			b.bestChosen = append([]int(nil), b.curChosen...)
		}
		return
	}

	// Prune against incumbent. Any completion costs at least the lower
	// bound; on an area tie it still needs at least one more rectangle.
	lb := b.lowerBoundArea(uncovered)
	if cur.Area+lb > b.best.Area {
		return
	}
	if cur.Area+lb == b.best.Area && cur.Count+1 >= b.best.Count {
		return
	}

	// Branch on the uncovered target with fewest covering candidates.
	branchTarget, branchWidth := -1, 0
	for i := range b.targets {
		if !uncovered.test(i) {
			continue
		}
		w := len(b.byTarget[i])
		if branchTarget == -1 || w < branchWidth {
			branchTarget, branchWidth = i, w
		}
	}

	for _, ci := range b.byTarget[branchTarget] {
		c := b.cands[ci]
		nc := Cost{Area: cur.Area + c.area, Count: cur.Count + 1}
		if nc.Area > b.best.Area {
			break // candidates sorted by area; later ones are no cheaper
		}
		next := newBitset(len(b.targets))
		for w := range next.w {
			next.w[w] = covered.w[w] | c.cover.w[w]
		}
		b.curChosen = append(b.curChosen, ci)
		b.dfs(next, nc)
		b.curChosen = b.curChosen[:len(b.curChosen)-1]
		if b.interrupted {
			return
		}
	}
}

// ---- brute-force verification over all candidate subsets (small grids) ----

// BruteForce independently computes the optimum by subset DP over ALL
// enumerated candidates (no dedup, no domination pruning), for cross-checking
// Solve on small grids. Requires <=64 targets and few candidates.
func BruteForce(in Input, opt Options) (Cost, error) {
	opt.withDefaults()
	targets := dedupePoints(in.Targets)
	forbidden := dedupePoints(in.Forbidden)
	fset := make(map[Point]bool, len(forbidden))
	for _, f := range forbidden {
		fset[f] = true
	}
	var kept []Point
	for _, t := range targets {
		if !fset[t] {
			kept = append(kept, t)
		}
	}
	targets = kept
	if len(targets) == 0 {
		if len(dedupePoints(in.Targets)) == 0 {
			return Cost{}, nil
		}
		return Cost{}, errors.New("uncoverable targets")
	}
	if len(targets) > 64 {
		return Cost{}, fmt.Errorf("too many targets for brute force: %d", len(targets))
	}
	cands := enumerateCandidates(targets, forbidden)
	nc := len(cands)
	if nc > opt.BruteForceLimit {
		return Cost{}, fmt.Errorf("too many candidates for brute force: %d > %d", nc, opt.BruteForceLimit)
	}

	size := 1 << uint(nc)
	cov := make([]uint64, size)
	area := make([]int64, size)
	count := make([]int32, size)
	cmask := make([]uint64, nc)
	for ci, c := range cands {
		var m uint64
		for i := range targets {
			if c.cover.test(i) {
				m |= 1 << uint(i)
			}
		}
		cmask[ci] = m
	}
	for s := 1; s < size; s++ {
		lb := s & -s
		ci := bits.TrailingZeros64(uint64(lb))
		prev := s ^ lb
		cov[s] = cov[prev] | cmask[ci]
		area[s] = area[prev] + cands[ci].area
		count[s] = count[prev] + 1
	}
	full := uint64(1)<<uint(len(targets)) - 1
	best := Cost{Area: int64(1) << 62, Count: int64(1) << 62}
	found := false
	for s := 1; s < size; s++ {
		if cov[s] != full {
			continue
		}
		c := Cost{Area: area[s], Count: int64(count[s])}
		if !found || lessCost(c, best) {
			best = c
			found = true
		}
	}
	if !found {
		return Cost{}, errors.New("no covering subset")
	}
	return best, nil
}
