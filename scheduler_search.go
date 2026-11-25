package handshake

import (
	"sort"
)

type searchEntry struct {
	mask       uint16
	lastWindow int
	batches    []Batch
}

type stateKey struct {
	mask       uint16
	lastWindow int
}

func (p *preparedSchedule) search() (*Plan, *Blocker) {
	allMask := uint16(1)<<len(p.nodeOrder) - 1
	if violation, _ := p.topology.CheckStates([]SwitchSet{NewSwitchSet()}); violation != nil {
		return nil, &Blocker{
			Kind:      "endpoint-or-start-state-unsafe",
			Detail:    "the all-old endpoint itself violates a transition safety property",
			Violation: violation,
		}
	}

	current := map[stateKey]*searchEntry{
		{mask: 0, lastWindow: -1}: {mask: 0, lastWindow: -1, batches: nil},
	}
	safeCache := map[uint16]*Violation{}

	for depth := 0; depth <= len(p.nodeOrder); depth++ {
		if full := p.bestFull(current, allMask); full != nil {
			return p.toPlan(full), nil
		}
		next := map[stateKey]*searchEntry{}
		for _, entry := range sortedEntries(current) {
			for _, units := range nonEmptySubsets(p.eligibleUnits(entry.mask)) {
				nodes := flattenUnits(units)
				nextMask := entry.mask | p.nodesToMask(nodes)
				if violation := p.stateViolation(nextMask, safeCache); violation != nil {
					continue
				}
				for window := entry.lastWindow + 1; window < len(p.windowAllowed); window++ {
					if p.nodesToMask(nodes)&^p.windowAllowed[window] != 0 {
						continue
					}
					candidate := &searchEntry{
						mask:       nextMask,
						lastWindow: window,
						batches:    appendBatches(entry.batches, Batch{Window: window, Nodes: nodes}),
					}
					p.keepBest(next, candidate)
				}
			}
		}
		if len(next) == 0 {
			return nil, p.frontierBlocker(current, safeCache)
		}
		current = next
	}
	return nil, &Blocker{Kind: "search-exhausted", Detail: "no legal schedule found"}
}

func (p *preparedSchedule) stateViolation(mask uint16, cache map[uint16]*Violation) *Violation {
	if violation, computed := cache[mask]; computed {
		return violation
	}
	violation, _ := p.topology.CheckStates([]SwitchSet{p.maskToSwitchSet(mask)})
	cache[mask] = violation
	return violation
}

func (p *preparedSchedule) bestFull(entries map[stateKey]*searchEntry, allMask uint16) *searchEntry {
	var best *searchEntry
	for _, entry := range entries {
		if entry.mask != allMask {
			continue
		}
		if best == nil || entry.lastWindow < best.lastWindow ||
			(entry.lastWindow == best.lastWindow && lexBatchPaths(entry.batches, best.batches)) {
			best = entry
		}
	}
	return best
}

func sortedEntries(entries map[stateKey]*searchEntry) []*searchEntry {
	result := make([]*searchEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].mask != result[j].mask {
			return result[i].mask < result[j].mask
		}
		return result[i].lastWindow < result[j].lastWindow
	})
	return result
}

// eligibleUnits returns the indivisible switch units available from mask. An
// atomic group is one unit, so it can never be split across batches.
func (p *preparedSchedule) eligibleUnits(mask uint16) [][]int {
	var available [][]int
	for groupID, groupMask := range p.groupMask {
		if mask&groupMask != 0 {
			continue
		}
		if p.groupPrereq[groupID]&^mask != 0 {
			continue
		}
		available = append(available, append([]int(nil), p.groupNodes[groupID]...))
	}
	for index, nodeID := range p.nodeOrder {
		bit := uint16(1) << index
		if mask&bit != 0 || p.belongsToGroup(bit) {
			continue
		}
		if !p.singlePrereqSatisfied(nodeID, mask) {
			continue
		}
		available = append(available, []int{nodeID})
	}
	return available
}

func (p *preparedSchedule) belongsToGroup(bit uint16) bool {
	for _, groupMask := range p.groupMask {
		if groupMask&bit != 0 {
			return true
		}
	}
	return false
}

func (p *preparedSchedule) singlePrereqSatisfied(nodeID int, mask uint16) bool {
	beforeBit := uint16(0)
	for _, dependency := range p.dependencies {
		if dependency.After != nodeID {
			continue
		}
		beforeBit |= 1 << p.nodeIndex[dependency.Before]
	}
	return beforeBit&^mask == 0
}

func flattenUnits(units [][]int) []int {
	var nodes []int
	for _, unit := range units {
		nodes = append(nodes, unit...)
	}
	sort.Ints(nodes)
	return nodes
}

func nonEmptySubsets(items [][]int) [][][]int {
	var subsets [][][]int
	for mask := 1; mask < 1<<len(items); mask++ {
		var subset [][]int
		for index := range items {
			if mask&(1<<index) != 0 {
				subset = append(subset, items[index])
			}
		}
		subsets = append(subsets, subset)
	}
	sort.Slice(subsets, func(i, j int) bool {
		return lexNodeLists(flattenUnits(subsets[i]), flattenUnits(subsets[j]))
	})
	return subsets
}

func (p *preparedSchedule) nodesToMask(nodes []int) uint16 {
	var mask uint16
	for _, nodeID := range nodes {
		mask |= 1 << p.nodeIndex[nodeID]
	}
	return mask
}

func (p *preparedSchedule) maskToSwitchSet(mask uint16) SwitchSet {
	state := NewSwitchSet()
	for index, nodeID := range p.nodeOrder {
		if mask&(1<<index) != 0 {
			state[nodeID] = true
		}
	}
	return state
}

func appendBatches(existing []Batch, next Batch) []Batch {
	result := make([]Batch, 0, len(existing)+1)
	result = append(result, existing...)
	result = append(result, Batch{Window: next.Window, Nodes: append([]int(nil), next.Nodes...)})
	return result
}

func (p *preparedSchedule) keepBest(entries map[stateKey]*searchEntry, candidate *searchEntry) {
	key := stateKey{mask: candidate.mask, lastWindow: candidate.lastWindow}
	existing, ok := entries[key]
	if !ok || lexBatchPaths(candidate.batches, existing.batches) {
		entries[key] = candidate
	}
}

func lexNodeLists(left, right []int) bool {
	for index := 0; index < len(left) && index < len(right); index++ {
		if left[index] != right[index] {
			return left[index] < right[index]
		}
	}
	return len(left) < len(right)
}

func lexBatchPaths(left, right []Batch) bool {
	for index := 0; index < len(left) && index < len(right); index++ {
		if lexNodeLists(left[index].Nodes, right[index].Nodes) {
			return true
		}
		if lexNodeLists(right[index].Nodes, left[index].Nodes) {
			return false
		}
	}
	return len(left) < len(right)
}

func (p *preparedSchedule) toPlan(entry *searchEntry) *Plan {
	batches := make([]Batch, len(entry.batches))
	for index, batch := range entry.batches {
		batches[index] = Batch{Window: batch.Window, Nodes: append([]int(nil), batch.Nodes...)}
	}
	return &Plan{Batches: batches, WindowCount: entry.lastWindow + 1}
}
