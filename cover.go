package diaglab

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"
)

// MaxCandidates bounds the exact solver (subset enumeration is 2^n).
const MaxCandidates = 20

type CoverRequest struct {
	G          *Graph
	Observed   []string // observed failed nodes
	Candidates []string // candidate root nodes, at most MaxCandidates
}

type CoveredObservation struct {
	Observed string
	Path     []string // witness path from the root to the observation
}

type RootEvidence struct {
	Root   string
	Covers []CoveredObservation
}

type CoverResult struct {
	Roots            []string
	Evidence         []RootEvidence
	Uncovered        []string
	MinSize          int
	VerifiedOptimal  bool     // brute-force subset enumeration agrees
	CycleCheckPassed bool     // no optimal node-level solution splits a cycle
	CycleReports     []string // human-readable cycle verification notes
}

// MinRootCover compresses SCCs, then finds a minimum set of candidate roots
// whose reachable set includes every observed failure.
func MinRootCover(req CoverRequest) (*CoverResult, error) {
	g := req.G
	if len(req.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates given")
	}
	if len(req.Candidates) > MaxCandidates {
		return nil, fmt.Errorf("too many candidates: %d > %d", len(req.Candidates), MaxCandidates)
	}
	for _, o := range req.Observed {
		if !g.Has(o) {
			return nil, fmt.Errorf("observed node %q not in graph", o)
		}
	}
	for _, c := range req.Candidates {
		if !g.Has(c) {
			return nil, fmt.Errorf("candidate %q not in graph", c)
		}
	}

	cond := g.Condense()

	// Bit positions over distinct observed components.
	obsCompIdx := map[int]int{}
	var obsCompOf map[string]int = cond.CompOf
	for _, o := range req.Observed {
		c := obsCompOf[o]
		if _, ok := obsCompIdx[c]; !ok {
			obsCompIdx[c] = len(obsCompIdx)
		}
	}
	if len(obsCompIdx) > 63 {
		return nil, fmt.Errorf("too many distinct observed components: %d", len(obsCompIdx))
	}
	full := uint64(1)<<uint(len(obsCompIdx)) - 1

	// Group candidates by component; one component = one potential root.
	type candGroup struct {
		comp  int
		nodes []string
		mask  uint64
	}
	groupByComp := map[int]*candGroup{}
	var compOrder []int
	for _, cn := range req.Candidates {
		c := cond.CompOf[cn]
		gr, ok := groupByComp[c]
		if !ok {
			gr = &candGroup{comp: c}
			groupByComp[c] = gr
			compOrder = append(compOrder, c)
		}
		gr.nodes = append(gr.nodes, cn)
	}
	sort.Ints(compOrder)
	var groups []*candGroup
	for _, c := range compOrder {
		gr := groupByComp[c]
		sort.Strings(gr.nodes)
		for rc := range cond.ReachableComps(c) {
			if bit, ok := obsCompIdx[rc]; ok {
				gr.mask |= 1 << uint(bit)
			}
		}
		groups = append(groups, gr)
	}

	// Exact minimum set cover over component masks (branch and bound).
	masks := make([]uint64, len(groups))
	for i, gr := range groups {
		masks[i] = gr.mask
	}
	bestSize, bestIdx := minSetCover(masks, full)

	res := &CoverResult{}
	if bestSize < 0 {
		for _, o := range req.Observed {
			res.Uncovered = append(res.Uncovered, o)
		}
		sort.Strings(res.Uncovered)
		res.MinSize = -1
	} else {
		res.MinSize = bestSize
		for _, i := range bestIdx {
			res.Roots = append(res.Roots, groups[i].nodes[0])
		}
		sort.Strings(res.Roots)
	}

	// Coverage evidence: assign each observed node to the first root that
	// reaches it, with a witness path in the original graph.
	if bestSize >= 0 {
		assigned := map[string]bool{}
		evByRoot := map[string]*RootEvidence{}
		for _, r := range res.Roots {
			evByRoot[r] = &RootEvidence{Root: r}
		}
		for _, o := range req.Observed {
			if assigned[o] {
				continue
			}
			for _, r := range res.Roots {
				if path, ok := g.Path(r, o); ok {
					ev := evByRoot[r]
					ev.Covers = append(ev.Covers, CoveredObservation{Observed: o, Path: path})
					assigned[o] = true
					break
				}
			}
		}
		for _, o := range req.Observed {
			if !assigned[o] {
				res.Uncovered = append(res.Uncovered, o)
			}
		}
		for _, r := range res.Roots {
			res.Evidence = append(res.Evidence, *evByRoot[r])
		}
	}

	// Verification 1: exhaustive subset enumeration over the ORIGINAL
	// candidate nodes (no compression) must agree on the minimum size.
	nodeMasks := make([]uint64, len(req.Candidates))
	for i, cn := range req.Candidates {
		nodeMasks[i] = masks[indexOfComp(compOrder, cond.CompOf[cn])]
	}
	bruteSize, bruteSols := bruteForceCover(nodeMasks, full)
	res.VerifiedOptimal = bruteSize == bestSize

	// Verification 2: a cycle must never be counted as several independent
	// roots. Candidates in one SCC have identical reachability, so any
	// node-level optimal solution uses at most one of them.
	res.CycleCheckPassed = true
	compOfCand := make(map[string]int)
	for _, cn := range req.Candidates {
		compOfCand[cn] = cond.CompOf[cn]
	}
	for _, sol := range bruteSols {
		seenComp := map[int]bool{}
		for _, ci := range sol {
			c := compOfCand[req.Candidates[ci]]
			if seenComp[c] {
				res.CycleCheckPassed = false
			}
			seenComp[c] = true
		}
	}
	for _, gr := range groups {
		if len(cond.Comps[gr.comp]) > 1 && len(gr.nodes) > 0 {
			res.CycleReports = append(res.CycleReports, fmt.Sprintf(
				"SCC {%s} is one root unit: %d candidate(s) [%s] share identical coverage (mask 0b%06b); counted at most once",
				strings.Join(cond.Comps[gr.comp], ","), len(gr.nodes),
				strings.Join(gr.nodes, ","), gr.mask))
		}
	}
	if res.CycleCheckPassed {
		res.CycleReports = append(res.CycleReports,
			"subset enumeration: no minimum node-level solution picks two members of the same SCC")
	}
	return res, nil
}

func indexOfComp(sorted []int, v int) int {
	return sort.SearchInts(sorted, v)
}

// minSetCover returns the minimum number of masks whose union is full, plus
// one witnessing index set. Returns -1 if impossible. Branch and bound
// branching on the first uncovered element.
func minSetCover(masks []uint64, full uint64) (int, []int) {
	if full == 0 {
		return 0, nil
	}
	best := -1
	var bestSet []int

	// Greedy upper bound.
	covered := uint64(0)
	used := make([]bool, len(masks))
	var greedy []int
	for covered != full {
		bi, badd := -1, 0
		for i, m := range masks {
			if used[i] {
				continue
			}
			if add := bits.OnesCount64(m &^ covered); add > badd {
				bi, badd = i, add
			}
		}
		if bi < 0 {
			break
		}
		used[bi] = true
		covered |= masks[bi]
		greedy = append(greedy, bi)
	}
	if covered == full {
		best = len(greedy)
		bestSet = greedy
	}

	chosen := []int{}
	var bt func(covered uint64)
	bt = func(covered uint64) {
		if covered == full {
			if best < 0 || len(chosen) < best {
				best = len(chosen)
				bestSet = append([]int(nil), chosen...)
			}
			return
		}
		if best >= 0 && len(chosen)+1 >= best {
			return
		}
		// Lower bound: remaining bits / max additional coverage.
		remaining := bits.OnesCount64(full &^ covered)
		maxAdd := 0
		for i, m := range masks {
			if used[i] {
				continue
			}
			if a := bits.OnesCount64(m &^ covered); a > maxAdd {
				maxAdd = a
			}
		}
		if maxAdd == 0 {
			return
		}
		need := (remaining + maxAdd - 1) / maxAdd
		if best >= 0 && len(chosen)+need >= best {
			return
		}
		// Branch on the first uncovered element.
		var bit int
		for bit = 0; bit < 64; bit++ {
			if full&(1<<uint(bit)) != 0 && covered&(1<<uint(bit)) == 0 {
				break
			}
		}
		for i, m := range masks {
			if used[i] || m&(1<<uint(bit)) == 0 {
				continue
			}
			used[i] = true
			chosen = append(chosen, i)
			bt(covered | m)
			chosen = chosen[:len(chosen)-1]
			used[i] = false
		}
	}
	bt(0)
	return best, bestSet
}

// bruteForceCover enumerates all 2^n subsets and returns the minimum cover
// size plus every minimum-size solution (as index sets).
func bruteForceCover(masks []uint64, full uint64) (int, [][]int) {
	n := len(masks)
	best := -1
	var sols [][]int
	for s := 1; s < (1 << uint(n)); s++ {
		sz := bits.OnesCount64(uint64(s))
		if best >= 0 && sz > best {
			continue
		}
		var union uint64
		for i := 0; i < n; i++ {
			if s&(1<<uint(i)) != 0 {
				union |= masks[i]
			}
		}
		if union != full {
			continue
		}
		if best < 0 || sz < best {
			best = sz
			sols = nil
		}
		var sol []int
		for i := 0; i < n; i++ {
			if s&(1<<uint(i)) != 0 {
				sol = append(sol, i)
			}
		}
		sols = append(sols, sol)
	}
	return best, sols
}
