package replayhandoff

import (
	"sort"
)

type nodePageSet struct {
	node  NodeID
	pages []int
}

type requirement struct {
	// seenPages contains candidate pages with a seen proof on any required node
	// covering the atom. Selecting any one resolves the atom as reject.
	seenPages int
	// missing covers each currently missing node. Each entry contains candidate
	// pages with a fresh unseen proof covering the atom.
	missing []nodePageSet
}

func (h *Handoff) requirementFor(partition Partition, atom Interval, missing []NodeID) requirement {
	req := requirement{}
	missingNodes := make(map[NodeID]struct{}, len(missing))
	for _, node := range missing {
		missingNodes[node] = struct{}{}
		req.missing = append(req.missing, nodePageSet{node: node})
	}
	for pageIndex, page := range h.pages {
		for _, proof := range page.Proofs {
			if proof.Partition != partition || !proofCoversAtom(proof, atom) {
				continue
			}
			required := false
			for _, node := range h.NodesFor(partition) {
				if node == proof.Node {
					required = true
					break
				}
			}
			if !required {
				continue
			}
			if proofSeesAtom(proof, atom) {
				req.seenPages |= 1 << pageIndex
				continue
			}
			if _, needed := missingNodes[proof.Node]; !needed {
				continue
			}
			if proof.Barrier < h.leaseBarrier(proof.Node) {
				continue
			}
			found := false
			for i := range req.missing {
				if req.missing[i].node == proof.Node {
					req.missing[i].pages = append(req.missing[i].pages, pageIndex)
					found = true
					break
				}
			}
			if !found {
				req.missing = append(req.missing, nodePageSet{node: proof.Node, pages: []int{pageIndex}})
			}
		}
	}
	for i := range req.missing {
		bits := 0
		for _, page := range req.missing[i].pages {
			bits |= 1 << page
		}
		req.missing[i].pages = []int{bits}
	}
	return req
}

func (h *Handoff) minimumPages(reqs []requirement) ([]PageSelection, int64, bool) {
	reqs = dedupeRequirements(reqs)
	if len(reqs) == 0 {
		return nil, 0, true
	}
	pageCount := len(h.pages)
	if pageCount == 0 {
		return nil, 0, false
	}
	if pageCount > MaxCandidatePages {
		return nil, 0, false
	}
	fullMask := (1 << pageCount) - 1
	for _, req := range reqs {
		for _, cover := range req.missing {
			if len(cover.pages) != 1 {
				return nil, 0, false
			}
		}
	}
	if !resolvesRequirements(fullMask, reqs) {
		return nil, 0, false
	}
	bestMask := fullMask
	bestCost := maskCost(h.pages, fullMask)
	bestCount := popcount(fullMask)
	remainingMasks := make([]int, pageCount+1)
	remainingCost := make([]int64, pageCount+1)
	for i := pageCount - 1; i >= 0; i-- {
		remainingMasks[i] = remainingMasks[i+1] | (1 << i)
		cost := h.pages[i].Cost
		if cost < 0 {
			cost = 0
		}
		if i == pageCount-1 {
			remainingCost[i] = cost
		} else if cost < remainingCost[i+1] {
			remainingCost[i] = cost
		} else {
			remainingCost[i] = remainingCost[i+1]
		}
	}
	// Exact branch-and-bound over all subsets. m <= 20, and the remaining-page
	// feasibility test prunes branches that cannot possibly cover an atom.
	var search func(pos int, mask int, cost int64, count int)
	search = func(pos int, mask int, cost int64, count int) {
		if resolvesRequirements(mask, reqs) {
			if betterSubset(h.pages, mask, cost, count, bestMask, bestCost, bestCount) {
				bestMask, bestCost, bestCount = mask, cost, count
			}
			return
		}
		if pos >= pageCount {
			return
		}
		if !resolvesRequirements(mask|remainingMasks[pos], reqs) {
			return
		}
		if cost+remainingCost[pos] > bestCost {
			return
		}
		includeCost := cost
		if h.pages[pos].Cost > 0 {
			includeCost += h.pages[pos].Cost
		}
		search(pos+1, mask|(1<<pos), includeCost, count+1)
		search(pos+1, mask, cost, count)
	}
	search(0, 0, 0, 0)
	if bestMask == fullMask && !resolvesRequirements(bestMask, reqs) {
		return nil, 0, false
	}
	indices := make([]int, 0, bestCount)
	for i := 0; i < pageCount; i++ {
		if bestMask&(1<<i) != 0 {
			indices = append(indices, i)
		}
	}
	selected := make([]PageSelection, 0, len(indices))
	for _, index := range indices {
		selected = append(selected, PageSelection{ID: h.pages[index].ID, Cost: h.pages[index].Cost})
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	return selected, bestCost, true
}

func dedupeRequirements(reqs []requirement) []requirement {
	byKey := make(map[string]requirement, len(reqs))
	for _, req := range reqs {
		key := requirementKey(req)
		if _, exists := byKey[key]; exists {
			continue
		}
		byKey[key] = req
	}
	out := make([]requirement, 0, len(byKey))
	for _, req := range byKey {
		out = append(out, req)
	}
	return out
}

func requirementKey(req requirement) string {
	buf := make([]byte, 0, 16+len(req.missing)*8)
	buf = appendU64(buf, uint64(req.seenPages))
	buf = appendU64(buf, uint64(len(req.missing)))
	for _, cover := range req.missing {
		buf = appendU64(buf, uint64(len(cover.pages)))
		if len(cover.pages) == 1 {
			buf = appendU64(buf, uint64(cover.pages[0]))
		}
	}
	return string(buf)
}

func resolvesRequirements(mask int, reqs []requirement) bool {
	for _, req := range reqs {
		if mask&req.seenPages != 0 {
			continue
		}
		resolved := true
		for _, cover := range req.missing {
			if len(cover.pages) != 1 || mask&cover.pages[0] == 0 {
				resolved = false
				break
			}
		}
		if !resolved {
			return false
		}
	}
	return true
}

func maskCost(pages []ProofPage, mask int) int64 {
	var cost int64
	for i := range pages {
		if mask&(1<<i) != 0 && pages[i].Cost > 0 {
			cost += pages[i].Cost
		}
	}
	return cost
}

func popcount(v int) int {
	count := 0
	for v != 0 {
		v &= v - 1
		count++
	}
	return count
}

func betterSubset(pages []ProofPage, mask int, cost int64, count int, bestMask int, bestCost int64, bestCount int) bool {
	if cost < bestCost {
		return true
	}
	if cost > bestCost {
		return false
	}
	if count < bestCount {
		return true
	}
	if count > bestCount {
		return false
	}
	a := selectedIDs(pages, mask)
	b := selectedIDs(pages, bestMask)
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return true
		}
		if a[i] > b[i] {
			return false
		}
	}
	return len(a) < len(b)
}

func selectedIDs(pages []ProofPage, mask int) []string {
	out := make([]string, 0)
	for i := range pages {
		if mask&(1<<i) != 0 {
			out = append(out, pages[i].ID)
		}
	}
	sort.Strings(out)
	return out
}
