package migrationaudit

type optimizationTransition struct {
	action int
	worst  int
	key    stateKey
}

type optimizationNode struct {
	state      *auditState
	successors []optimizationTransition
}

func evaluateOptimization(indexed *indexedProblem, initial *auditState, cap int) *optimizationMemory {
	memory := &optimizationMemory{entries: map[stateKey]*optimizationEntry{}, cap: cap}
	nodes := map[stateKey]*optimizationNode{}
	levels := make([][]stateKey, len(indexed.problem.Actions)+1)
	discoverOptimization(indexed, memory, nodes, levels, initial, cap)

	for level := len(levels) - 1; level >= 0; level-- {
		for _, key := range levels[level] {
			node := nodes[key]
			entry := memory.entries[key]
			if entry == nil {
				entry = &optimizationEntry{key: key, legalActions: legalActionIDs(indexed, node.state), chosenAction: -1}
				memory.entries[key] = entry
			}
			if node.state.executed.count() == len(indexed.problem.Actions) {
				entry.feasible = true
				entry.chosenAction = -1
				continue
			}
			bestSum := -1
			for _, transition := range node.successors {
				successor := memory.entries[transition.key]
				if successor == nil || !successor.feasible {
					continue
				}
				candidate := transition.worst + successor.remainingSum
				if bestSum < 0 || candidate < bestSum || (candidate == bestSum && entry.chosenAction >= 0 && indexed.problem.Actions[transition.action].ID < indexed.problem.Actions[entry.chosenAction].ID) {
					bestSum = candidate
					entry.feasible = true
					entry.remainingSum = candidate
					entry.chosenAction = transition.action
					entry.transitionExposure = transition.worst
					entry.successorKey = transition.key
				}
			}
		}
	}
	return memory
}

func discoverOptimization(indexed *indexedProblem, memory *optimizationMemory, nodes map[stateKey]*optimizationNode, levels [][]stateKey, initial *auditState, cap int) {
	nodes[initial.structuredKey()] = &optimizationNode{state: initial}
	levels[0] = append(levels[0], initial.structuredKey())
	for level := 0; level < len(levels)-1; level++ {
		for _, key := range levels[level] {
			node := nodes[key]
			memory.stats.ReachableStates++
			legal := legalActionIDs(indexed, node.state)
			memory.entries[key] = &optimizationEntry{key: key, legalActions: legal, chosenAction: -1}
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
				node.successors = append(node.successors, optimizationTransition{action: actionIndex, worst: worst, key: nextKey})
				if _, ok := nodes[nextKey]; !ok {
					nodes[nextKey] = &optimizationNode{state: next}
					levels[level+1] = append(levels[level+1], nextKey)
				}
			}
		}
	}
}
