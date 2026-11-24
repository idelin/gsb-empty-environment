package migrationaudit

type feasibilityNode struct {
	state      *auditState
	successors []stateKey
}

func evaluateFeasibility(indexed *indexedProblem, initial *auditState, cap int) *feasibilityMemory {
	memory := &feasibilityMemory{entries: map[stateKey]*feasibilityEntry{}, cap: cap}
	nodes := map[stateKey]*feasibilityNode{}
	levels := make([][]stateKey, len(indexed.problem.Actions)+1)
	discoverFeasibility(indexed, memory, nodes, levels, initial, cap)

	for level := len(levels) - 1; level >= 0; level-- {
		for _, key := range levels[level] {
			node := nodes[key]
			entry := memory.entries[key]
			if entry == nil {
				entry = &feasibilityEntry{key: key, legalActions: legalActionIDs(indexed, node.state), chosenAction: -1}
				memory.entries[key] = entry
			}
			if node.state.executed.count() == len(indexed.problem.Actions) {
				entry.feasible = true
				entry.chosenAction = -1
				continue
			}
			for _, successorKey := range node.successors {
				successor := memory.entries[successorKey]
				if successor != nil && successor.feasible {
					entry.feasible = true
					entry.chosenAction = transitionAction(indexed, node.state, nodes[successorKey].state)
					break
				}
			}
		}
	}
	return memory
}

func discoverFeasibility(indexed *indexedProblem, memory *feasibilityMemory, nodes map[stateKey]*feasibilityNode, levels [][]stateKey, initial *auditState, cap int) {
	nodes[initial.structuredKey()] = &feasibilityNode{state: initial}
	levels[0] = append(levels[0], initial.structuredKey())
	for level := 0; level < len(levels)-1; level++ {
		for _, key := range levels[level] {
			node := nodes[key]
			memory.stats.ReachableStates++
			legal := legalActionIDs(indexed, node.state)
			memory.entries[key] = &feasibilityEntry{key: key, legalActions: legal, chosenAction: -1}
			for _, actionIndex := range legal {
				next := indexed.applyAction(node.state, actionIndex)
				memory.stats.TransitionChecks++
				if !indexed.validatorAccepts(next) {
					continue
				}
				exposures := indexed.exposureAt(next)
				worst, _ := indexed.worstExposure(exposures)
				if worst > cap {
					memory.stats.PrunedByExposureCap++
					continue
				}
				nextKey := next.structuredKey()
				node.successors = append(node.successors, nextKey)
				if _, ok := nodes[nextKey]; !ok {
					nodes[nextKey] = &feasibilityNode{state: next}
					levels[level+1] = append(levels[level+1], nextKey)
				}
			}
		}
	}
}

func transitionAction(indexed *indexedProblem, current *auditState, next *auditState) int {
	for _, actionIndex := range indexed.legalActions(current) {
		if indexed.applyAction(current, actionIndex).structuredKey() == next.structuredKey() {
			return actionIndex
		}
	}
	return -1
}
