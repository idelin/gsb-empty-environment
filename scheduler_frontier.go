package handshake

import (
	"fmt"
	"sort"
)

func (p *preparedSchedule) frontierBlocker(entries map[stateKey]*searchEntry, safeCache map[uint16]*Violation) *Blocker {
	frontier := make([]FrontierState, 0, len(entries))
	for _, entry := range sortedEntries(entries) {
		reasons := p.deadEndReasons(entry, safeCache)
		if len(reasons) == 0 {
			continue
		}
		frontier = append(frontier, FrontierState{
			ReachableWith: cloneBatches(entry.batches),
			Switched:      p.sortedMaskNodes(entry.mask),
			PendingGroups: p.pendingGroups(entry.mask),
			Reasons:       reasons,
		})
	}
	if len(frontier) == 0 {
		return &Blocker{Kind: "search-exhausted", Detail: "no transition survived all feasibility and safety filters"}
	}
	return &Blocker{
		Kind:     "no-completable-schedule",
		Detail:   "every reachable partial release is a dead end; each frontier entry lists independently checkable reasons",
		Frontier: frontier,
	}
}

func cloneBatches(batches []Batch) []Batch {
	result := make([]Batch, len(batches))
	for index, batch := range batches {
		result[index] = Batch{Window: batch.Window, Nodes: append([]int(nil), batch.Nodes...)}
	}
	return result
}

func (p *preparedSchedule) deadEndReasons(entry *searchEntry, safeCache map[uint16]*Violation) []string {
	var reasons []string
	for _, units := range nonEmptySubsets(p.eligibleUnits(entry.mask)) {
		nodes := flattenUnits(units)
		nextMask := entry.mask | p.nodesToMask(nodes)
		window := p.earliestWindow(p.nodesToMask(nodes), entry.lastWindow)
		label := "batch " + formatNodes(nodes)
		if window < 0 {
			reasons = append(reasons, label+" has no later maintenance window containing all its nodes")
			continue
		}
		if violation := safeCache[nextMask]; violation != nil {
			reasons = append(reasons, label+" creates an unsafe intermediate state")
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "no eligible unit remains while the release is incomplete")
	}
	return uniqueStrings(reasons)
}

func (p *preparedSchedule) sortedMaskNodes(mask uint16) []int {
	var nodes []int
	for index, nodeID := range p.nodeOrder {
		if mask&(1<<index) != 0 {
			nodes = append(nodes, nodeID)
		}
	}
	return nodes
}

func (p *preparedSchedule) pendingGroups(mask uint16) [][]int {
	var groups [][]int
	for groupID, groupMask := range p.groupMask {
		if mask&groupMask == 0 {
			groups = append(groups, append([]int(nil), p.groupNodes[groupID]...))
		}
	}
	return groups
}

func formatNodes(nodes []int) string {
	text := "["
	for index, node := range nodes {
		if index > 0 {
			text += ","
		}
		text += fmt.Sprintf("%d", node)
	}
	return text + "]"
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// VerifyPlan independently re-checks a plan against windows, atomic groups,
// dependencies, one-switch-per-node and transition safety.
func VerifyPlan(input SchedulingInput, plan *Plan) error {
	prepared, err := prepareSchedule(input)
	if err != nil {
		return err
	}
	if plan == nil || len(plan.Batches) == 0 {
		return fmt.Errorf("verification: plan is empty")
	}
	switched := map[int]bool{}
	previousWindow := -1
	var states []SwitchSet
	current := NewSwitchSet()
	for batchIndex, batch := range plan.Batches {
		if batch.Window <= previousWindow {
			return fmt.Errorf("verification: batch %d does not use a strictly increasing window", batchIndex)
		}
		previousWindow = batch.Window
		mask := prepared.nodesToMask(batch.Nodes)
		if mask&^prepared.windowAllowed[batch.Window] != 0 {
			return fmt.Errorf("verification: batch %d contains a node not allowed in window %d", batchIndex, batch.Window)
		}
		for _, nodeID := range batch.Nodes {
			if switched[nodeID] {
				return fmt.Errorf("verification: node %d switches more than once", nodeID)
			}
			switched[nodeID] = true
		}
		for groupID, groupMask := range prepared.groupMask {
			touched := mask & groupMask
			if touched != 0 && touched != groupMask {
				return fmt.Errorf("verification: atomic group %v is split", prepared.groupNodes[groupID])
			}
		}
		current = current.With(batch.Nodes...)
		states = append(states, current)
	}
	for _, dependency := range prepared.dependencies {
		if !switched[dependency.Before] || !switched[dependency.After] {
			return fmt.Errorf("verification: dependency endpoint missing")
		}
	}
	for _, nodeID := range prepared.nodeOrder {
		if !switched[nodeID] {
			return fmt.Errorf("verification: node %d is never switched", nodeID)
		}
	}
	if violation, _ := prepared.topology.CheckStates(states); violation != nil {
		return fmt.Errorf("verification: unsafe state at fallback start %d for capabilities %v", violation.Start, violation.Capabilities)
	}
	return nil
}

// VerifyBlocker independently checks a no-plan blocker certificate. It
// confirms every frontier state is reachable and genuinely has no legal next
// batch, and verifies static window claims directly.
func VerifyBlocker(input SchedulingInput, blocker *Blocker) error {
	if blocker == nil {
		return fmt.Errorf("verification: nil blocker")
	}
	prepared, err := prepareSchedule(input)
	if err != nil {
		return err
	}
	switch blocker.Kind {
	case "node-has-no-window":
		for _, nodeID := range blocker.Nodes {
			index, ok := prepared.nodeIndex[nodeID]
			if !ok {
				return fmt.Errorf("verification: unknown blocker node %d", nodeID)
			}
			if prepared.earliestWindow(1<<index, -1) >= 0 {
				return fmt.Errorf("verification: node %d does have a window", nodeID)
			}
		}
		return nil
	case "group-has-no-window":
		mask := prepared.nodesToMask(blocker.Group)
		if prepared.earliestWindow(mask, -1) >= 0 {
			return fmt.Errorf("verification: atomic group does have a window")
		}
		return nil
	case "no-completable-schedule":
		return verifyFrontier(prepared, blocker)
	default:
		return nil
	}
}

func verifyFrontier(prepared *preparedSchedule, blocker *Blocker) error {
	if len(blocker.Frontier) == 0 {
		return fmt.Errorf("verification: empty frontier cannot prove impossibility")
	}
	for index, state := range blocker.Frontier {
		mask := prepared.nodesToMask(state.Switched)
		if err := verifyReachableBatches(prepared, mask, state.ReachableWith); err != nil {
			return fmt.Errorf("verification: frontier entry %d is not reachable: %w", index, err)
		}
		if len(prepared.eligibleUnits(mask)) == 0 && mask != uint16(1)<<len(prepared.nodeOrder)-1 {
			return fmt.Errorf("verification: frontier entry %d omitted a missing-unit reason", index)
		}
		for _, units := range nonEmptySubsets(prepared.eligibleUnits(mask)) {
			nodes := flattenUnits(units)
			window := prepared.earliestWindow(prepared.nodesToMask(nodes), lastWindowOf(state.ReachableWith))
			if window < 0 {
				continue
			}
			nextMask := mask | prepared.nodesToMask(nodes)
			violation, _ := prepared.topology.CheckStates([]SwitchSet{prepared.maskToSwitchSet(nextMask)})
			if violation == nil {
				return fmt.Errorf("verification: frontier entry %d has an unexplored legal batch %v", index, nodes)
			}
		}
	}
	return nil
}

func verifyReachableBatches(prepared *preparedSchedule, finalMask uint16, batches []Batch) error {
	mask := uint16(0)
	previousWindow := -1
	for _, batch := range batches {
		if batch.Window <= previousWindow {
			return fmt.Errorf("non-increasing window")
		}
		previousWindow = batch.Window
		batchMask := prepared.nodesToMask(batch.Nodes)
		if batchMask&^prepared.windowAllowed[batch.Window] != 0 {
			return fmt.Errorf("window does not permit batch")
		}
		for groupID, groupMask := range prepared.groupMask {
			touched := batchMask & groupMask
			if touched != 0 && touched != groupMask {
				return fmt.Errorf("atomic group %d split", groupID)
			}
		}
		mask |= batchMask
	}
	if mask != finalMask {
		return fmt.Errorf("reachable batches produce a different mask")
	}
	return nil
}

func lastWindowOf(batches []Batch) int {
	if len(batches) == 0 {
		return -1
	}
	return batches[len(batches)-1].Window
}
