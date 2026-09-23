package main

import (
	"bytes"
	"fmt"
	"sort"
)

// selection is the outcome of dictionary selection.
type selection struct {
	dict      [][]byte
	dictBytes int // serialized dictionary size (the overhead charged to ratios)
	trainCost int // dictBytes + sum of encoded train sizes (model cost)
	exact     bool
}

// dictSerializedLen returns the size of the serialized dictionary file.
func dictSerializedLen(dict [][]byte) int { return len(marshalDict(dict)) }

// trainCostOf computes the total cost of a dictionary plus the parse costs of
// all samples. An empty dictionary costs nothing: no dictionary file exists.
func trainCostOf(samples [][]byte, dict [][]byte, exactMode bool) int {
	di := buildDictIndex(dict)
	total := 0
	if len(dict) > 0 {
		total = dictSerializedLen(dict)
	}
	for _, s := range samples {
		if exactMode {
			total += exactParseCost(s, di)
		} else {
			total += fastParseCost(s, di)
		}
	}
	return total
}

// sortDictCanonical sorts entries so the serialized dictionary is
// deterministic regardless of how it was assembled.
func sortDictCanonical(dict [][]byte) {
	sort.Slice(dict, func(i, j int) bool { return bytes.Compare(dict[i], dict[j]) < 0 })
}

// selectExact enumerates every subset of candidates that fits the budget and
// returns the dictionary minimizing the true joint train cost (dictionary
// overhead plus optimal-parse encoding). Overlapping candidates are never
// credited twice: each subset is scored by actually parsing with it.
func selectExact(samples [][]byte, cands []candidate, budget, maxExact int) (selection, error) {
	if len(cands) > maxExact {
		return selection{}, fmt.Errorf("exact mode: %d candidates exceed limit %d; raise -maxexact or use -mode heuristic", len(cands), maxExact)
	}
	best := selection{dict: nil, trainCost: trainCostOf(samples, nil, true), exact: true}
	n := len(cands)
	for mask := uint64(1); mask < uint64(1)<<uint(n); mask++ {
		var dict [][]byte
		for i := 0; i < n; i++ {
			if mask&(uint64(1)<<uint(i)) != 0 {
				dict = append(dict, cands[i].phrase)
			}
		}
		if len(dict) > maxDictEntries {
			continue
		}
		if dictSerializedLen(dict) > budget {
			continue
		}
		cost := trainCostOf(samples, dict, true)
		if cost < best.trainCost {
			sortDictCanonical(dict)
			best = selection{dict: dict, dictBytes: dictSerializedLen(dict), trainCost: cost, exact: true}
		}
	}
	return best, nil
}

// selectHeuristic greedily adds candidates by standalone gain, then prunes
// entries whose removal does not increase the true joint cost. It returns a
// feasible dictionary (within budget) but does not promise optimality.
func selectHeuristic(samples [][]byte, cands []candidate, budget int) selection {
	sorted := make([]candidate, len(cands))
	copy(sorted, cands)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].gain != sorted[j].gain {
			return sorted[i].gain > sorted[j].gain
		}
		return bytes.Compare(sorted[i].phrase, sorted[j].phrase) < 0
	})
	var dict [][]byte
	used := dictSerializedLen(nil) // fixed file overhead counts toward the budget
	for _, c := range sorted {
		if c.gain <= 0 || len(dict) >= maxDictEntries {
			continue
		}
		if used+dictEntryCost(len(c.phrase)) > budget {
			continue
		}
		dict = append(dict, c.phrase)
		used += dictEntryCost(len(c.phrase))
	}
	sortDictCanonical(dict)
	// Prune: drop entries that do not pay for themselves in the joint parse.
	cur := trainCostOf(samples, dict, false)
	for pass := 0; pass < 4; pass++ {
		changed := false
		for i := 0; i < len(dict); i++ {
			trial := make([][]byte, 0, len(dict)-1)
			trial = append(trial, dict[:i]...)
			trial = append(trial, dict[i+1:]...)
			if c := trainCostOf(samples, trial, false); c <= cur {
				dict = trial
				cur = c
				changed = true
				i--
			}
		}
		if !changed {
			break
		}
	}
	// Never keep a dictionary that loses to no dictionary at all.
	if empty := trainCostOf(samples, nil, false); empty <= cur {
		dict = nil
		cur = empty
	}
	dictBytes := 0
	if len(dict) > 0 {
		dictBytes = dictSerializedLen(dict)
	}
	return selection{dict: dict, dictBytes: dictBytes, trainCost: cur}
}

// savingsUpperBound is an upper bound on the savings any dictionary of
// phrases within the candidate length range could achieve on the train set
// under the given budget. Each phrase can be used at most countNonOverlapping
// times and each use saves at most len-matchTokenCost bytes, so a dictionary's
// savings are bounded by the corresponding 0/1 knapsack value; the fractional
// knapsack relaxation (plus 2 header bytes per sample of run-merging slack)
// bounds that from above. It is a modeling bound, not a guarantee.
func savingsUpperBound(samples [][]byte, cands []candidate, budget int) int {
	type item struct {
		value, weight int
		phrase        []byte
	}
	items := make([]item, 0, len(cands))
	for _, c := range cands {
		if v := c.count * (len(c.phrase) - matchTokenCost); v > 0 {
			items = append(items, item{v, dictEntryCost(len(c.phrase)), c.phrase})
		}
	}
	// Sort by value density descending; deterministic tie-breaks.
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.value*b.weight != b.value*a.weight {
			return a.value*b.weight > b.value*a.weight
		}
		if a.value != b.value {
			return a.value > b.value
		}
		return bytes.Compare(a.phrase, b.phrase) < 0
	})
	ub, capacity := 0, budget
	for _, it := range items {
		if capacity <= 0 {
			break
		}
		if it.weight <= capacity {
			ub += it.value
			capacity -= it.weight
		} else {
			ub += it.value * capacity / it.weight // fractional relaxation
			capacity = 0
		}
	}
	for _, s := range samples {
		if len(s) > 0 {
			ub += litRunHeaderCost
		}
	}
	return ub
}
