package netsec

import (
	"sort"
	"strings"
)

// defaultSearchBudget bounds the branch-and-bound search for teaching-scale
// scripts. It is generous enough that realistic role unrollings stay exact.
const defaultSearchBudget = 200000

// minimumHittingSets returns every minimum-cardinality set of operations that
// intersects every clash's provenance set. Removing any one such set makes the
// script conflict-free, so each returned set is a minimum conflict operation
// set. The search is bounded by budget (0 selects the default); when the
// budget is exhausted the best set found so far is returned with exact=false.
func minimumHittingSets(groups [][]string, budget int) ([][]string, int, bool) {
	if budget <= 0 {
		budget = defaultSearchBudget
	}

	// Deduplicate and canonicalize clash groups; empty groups mean a clash with
	// no removable operation, which cannot be repaired.
	groupSets := make([]map[string]struct{}, 0, len(groups))
	universe := make(map[string]struct{})
	for _, g := range groups {
		if len(g) == 0 {
			return nil, 0, false
		}
		set := make(map[string]struct{}, len(g))
		for _, op := range g {
			set[op] = struct{}{}
			universe[op] = struct{}{}
		}
		groupSets = append(groupSets, set)
	}

	ops := make([]string, 0, len(universe))
	for op := range universe {
		ops = append(ops, op)
	}
	sort.Strings(ops)

	// Precompute, for each operation, the clash groups it can repair.
	covers := make(map[string][]int, len(ops))
	for i, g := range groupSets {
		for op := range g {
			covers[op] = append(covers[op], i)
		}
	}

	h := &hitSearch{
		groups:  groupSets,
		ops:     ops,
		covers:  covers,
		budget:  budget,
		bestLen: len(ops) + 1,
	}
	// Phase 1: find the minimum hitting-set cardinality.
	h.dive(0, map[int]struct{}{}, nil)
	if h.nodes > h.budget {
		if len(h.best) > 0 {
			return h.best, h.nodes, false
		}
		// The branch-and-bound budget ran out before any complete set was
		// found. Fall back to the trivial set of all provenance operations,
		// which always hits every clash group.
		return [][]string{append([]string(nil), ops...)}, h.nodes, false
	}
	minLen := h.bestLen

	// Phase 2: enumerate every hitting set of that cardinality, dropping the
	// pruning that would discard equal-size alternative solutions.
	h.best = nil
	h.bestLen = minLen
	h.enumerate(0, map[int]struct{}{}, nil)
	h.best = dedupSets(h.best)
	if h.nodes >= h.budget {
		return h.best, h.nodes, false
	}
	return h.best, h.nodes, true
}

type hitSearch struct {
	groups  []map[string]struct{}
	ops     []string
	covers  map[string][]int
	budget  int
	nodes   int
	bestLen int
	best    [][]string
}

func (h *hitSearch) enumerate(opIdx int, hitGroups map[int]struct{}, chosen []string) {
	h.nodes++
	if len(chosen) > h.bestLen || h.nodes > h.budget {
		return
	}
	groupIdx := -1
	for i := range h.groups {
		if _, ok := hitGroups[i]; !ok {
			groupIdx = i
			break
		}
	}
	if groupIdx == -1 {
		if len(chosen) == h.bestLen {
			set := append([]string(nil), chosen...)
			sort.Strings(set)
			h.best = append(h.best, set)
		}
		return
	}
	candidates := make([]string, 0, len(h.groups[groupIdx]))
	for op := range h.groups[groupIdx] {
		candidates = append(candidates, op)
	}
	sort.Strings(candidates)
	for _, op := range candidates {
		idx := opIndex(h.ops, op)
		if idx < opIdx {
			continue
		}
		next := make(map[int]struct{}, len(hitGroups)+len(h.covers[op]))
		for g := range hitGroups {
			next[g] = struct{}{}
		}
		for _, g := range h.covers[op] {
			next[g] = struct{}{}
		}
		h.enumerate(idx+1, next, append(chosen, op))
		if h.nodes > h.budget {
			return
		}
	}
}

func (h *hitSearch) dive(opIdx int, hitGroups map[int]struct{}, chosen []string) {
	h.nodes++
	if len(chosen) >= h.bestLen || h.nodes > h.budget {
		return
	}

	// Pick the first still-unhit clash group; any solution must choose one of
	// its operations, so branching on that group is complete and keeps the
	// search tightly bounded.
	groupIdx := -1
	for i := range h.groups {
		if _, ok := hitGroups[i]; !ok {
			groupIdx = i
			break
		}
	}
	if groupIdx == -1 {
		set := append([]string(nil), chosen...)
		sort.Strings(set)
		switch {
		case len(set) < h.bestLen:
			h.bestLen = len(set)
			h.best = [][]string{set}
		case len(set) == h.bestLen:
			h.best = append(h.best, set)
		}
		return
	}

	candidates := make([]string, 0, len(h.groups[groupIdx]))
	for op := range h.groups[groupIdx] {
		candidates = append(candidates, op)
	}
	sort.Strings(candidates)
	for _, op := range candidates {
		idx := opIndex(h.ops, op)
		if idx < opIdx {
			continue
		}
		next := make(map[int]struct{}, len(hitGroups)+len(h.covers[op]))
		for g := range hitGroups {
			next[g] = struct{}{}
		}
		for _, g := range h.covers[op] {
			next[g] = struct{}{}
		}
		h.dive(idx+1, next, append(chosen, op))
		if h.nodes > h.budget {
			return
		}
	}
}

func dedupSets(sets [][]string) [][]string {
	seen := make(map[string]struct{}, len(sets))
	out := make([][]string, 0, len(sets))
	for _, set := range sets {
		key := strings.Join(set, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, set)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Join(out[i], ",") < strings.Join(out[j], ",")
	})
	return out
}

func opIndex(ops []string, op string) int {
	for i, o := range ops {
		if o == op {
			return i
		}
	}
	return -1
}
