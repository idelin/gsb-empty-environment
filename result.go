package main

import "sort"

// selectedOut describes one retained (possibly merged) input.
type selectedOut struct {
	ID        string   `json:"id"`
	MergedIDs []string `json:"merged_ids,omitempty"`
	Cost      int64    `json:"cost"`
	Edges     []string `json:"edges"`
	Failures  []string `json:"failures,omitempty"`
}

// lowerBoundOut certifies how good the exact solution is.
type lowerBoundOut struct {
	MinInputs int   `json:"min_inputs"`
	MinCost   int64 `json:"min_cost"`
}

// result is the JSON document emitted by the trimmer.
type result struct {
	Selected         []selectedOut   `json:"selected"`
	NumSelected      int             `json:"num_selected"`
	TotalCost        int64           `json:"total_cost"`
	LowerBound       lowerBoundOut   `json:"lower_bound"`
	Optimal          bool            `json:"optimal"`
	NumInputs        int             `json:"num_inputs"`
	NumMerged        int             `json:"num_merged_candidates"`
	DuplicatesMerged []duplicatePair `json:"duplicates_merged,omitempty"`
	NumRequirements  int             `json:"num_requirements"`
	SearchNodes      int64           `json:"search_nodes"`
}

// trim runs the whole pipeline: merge duplicates, build the requirement
// universe, solve exactly, and compute an independent lower bound.
func trim(inputs []rawInput) (*result, error) {
	cands, dups := mergeDuplicates(inputs)
	nreq, _ := buildUniverse(cands)

	s := newSolver(cands, nreq)
	sel := s.solve()
	if sel == nil {
		// Should not happen: every requirement has at least one candidate.
		sel = []int{}
	}

	// Independent lower bound on the full (merged) instance.
	lbSolver := newSolver(cands, nreq)
	full := newBits(nreq)
	for i := 0; i < nreq; i++ {
		full.set(i)
	}
	all := make([]int, len(cands))
	for i := range cands {
		all[i] = i
	}
	lbCount := lbSolver.packingLB(full, all)
	if clb := ceilDiv(nreq, lbSolver.maxCover(full, all)); clb > lbCount {
		lbCount = clb
	}
	lbCost := lbSolver.costLB(lbCount, all)

	res := &result{
		Selected:         []selectedOut{},
		NumInputs:        len(inputs),
		NumMerged:        len(cands),
		NumRequirements:  nreq,
		DuplicatesMerged: dups,
		LowerBound:       lowerBoundOut{MinInputs: lbCount, MinCost: lbCost},
		Optimal:          true,
		SearchNodes:      s.nodes,
	}
	var total int64
	for _, ci := range sel {
		c := cands[ci]
		total += c.cost
		out := selectedOut{
			ID:       c.ids[0],
			Cost:     c.cost,
			Edges:    sortedKeys(c.edges),
			Failures: sortedKeys(c.fails),
		}
		if len(c.ids) > 1 {
			out.MergedIDs = append([]string(nil), c.ids[1:]...)
		}
		res.Selected = append(res.Selected, out)
	}
	sort.Slice(res.Selected, func(a, b int) bool { return res.Selected[a].ID < res.Selected[b].ID })
	res.NumSelected = len(res.Selected)
	res.TotalCost = total
	return res, nil
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
