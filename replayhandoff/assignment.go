package replayhandoff

func feasibleChain(variables []variable, chain []int) bool {
	if len(chain) == 0 {
		return true
	}

	previous := uint64(0)
	first := true
	for _, index := range chain {
		next, ok := smallestGreater(variables[index].allowed, previous, first)
		if !ok {
			return false
		}
		previous = next
		first = false
	}
	return true
}

func smallestGreater(values []uint64, previous uint64, first bool) (uint64, bool) {
	for _, value := range values {
		if first || value > previous {
			return value, true
		}
	}
	return 0, false
}

func (a analysis) buildAssignment(candidateLow, candidateFull uint64) (CompleteAssignment, bool) {
	nodeOrder := make([]NodeID, 0)
	chains := map[NodeID][]int{}
	for i, variable := range a.variables {
		if _, ok := chains[variable.node]; !ok {
			nodeOrder = append(nodeOrder, variable.node)
		}
		chains[variable.node] = append(chains[variable.node], i)
	}

	baseCount := len(a.variables)
	candidateIndex := baseCount
	allVariables := make([]variable, baseCount+1)
	copy(allVariables, a.variables)
	allVariables[candidateIndex] = variable{
		node:    "",
		entry:   LogEntry{LowSequence: candidateLow, Admitted: true},
		allowed: []uint64{candidateFull},
	}
	chosen := make([]uint64, len(allVariables))
	flatOrder := make([]int, 0, len(allVariables))
	flatOrder = append(flatOrder, candidateIndex)
	for _, node := range nodeOrder {
		flatOrder = append(flatOrder, chains[node]...)
	}

	var dfs func(flatPosition int) bool
	dfs = func(flatPosition int) bool {
		if flatPosition == len(flatOrder) {
			return true
		}
		index := flatOrder[flatPosition]
		for _, value := range allVariables[index].allowed {
			if allVariables[index].node != "" && chosen[candidateIndex] != 0 &&
				allVariables[index].entry.LowSequence == candidateLow &&
				!allVariables[index].entry.Admitted && value == chosen[candidateIndex] {
				continue
			}
			if allVariables[index].node == "" {
				blocked := false
				for other := range chosen {
					if allVariables[other].entry.LowSequence == candidateLow &&
						!allVariables[other].entry.Admitted && chosen[other] == value {
						blocked = true
						break
					}
				}
				if blocked {
					continue
				}
			}
			if !chainOK(a.variables, chains, chosen, index, value) {
				continue
			}
			chosen[index] = value
			if dfs(flatPosition + 1) {
				return true
			}
			chosen[index] = 0
		}
		return false
	}
	if !dfs(0) {
		return CompleteAssignment{}, false
	}

	assignment := CompleteAssignment{
		ReorderBound:   a.bound,
		HorizonLow:     a.horizonLow,
		HorizonHigh:    a.horizonHigh,
		Checkpoints:    append([]uint64(nil), a.anchors...),
		HasCandidate:   true,
		CandidateLow:   candidateLow,
		CandidateFull:  candidateFull,
		CandidateFresh: true,
	}
	assignment.Entries = make([]AssignedEntry, 0, len(allVariables))
	for i, variable := range allVariables {
		if i == candidateIndex {
			continue
		}
		assignment.Entries = append(assignment.Entries, AssignedEntry{
			Node:     variable.node,
			Low:      variable.entry.LowSequence,
			Full:     chosen[i],
			Admitted: variable.entry.Admitted,
		})
	}
	return assignment, true
}

func (a analysis) buildImportAssignment(fixedLow, fixedFull uint64) (CompleteAssignment, bool) {
	variables := make([]variable, len(a.variables))
	copy(variables, a.variables)
	fixed := false
	for i := range variables {
		if variables[i].entry.LowSequence == fixedLow && containsUint64(variables[i].allowed, fixedFull) {
			variables[i].allowed = []uint64{fixedFull}
			fixed = true
		}
	}
	if !fixed {
		return CompleteAssignment{}, false
	}

	nodeOrder := make([]NodeID, 0)
	chains := map[NodeID][]int{}
	for i, variable := range variables {
		if _, ok := chains[variable.node]; !ok {
			nodeOrder = append(nodeOrder, variable.node)
		}
		chains[variable.node] = append(chains[variable.node], i)
	}

	chosen := make([]uint64, len(variables))
	conflictGroups := buildConflictGroups(variables)
	flatOrder := make([]int, 0, len(variables))
	for _, node := range nodeOrder {
		flatOrder = append(flatOrder, chains[node]...)
	}

	var dfs func(flatPosition int) bool
	dfs = func(flatPosition int) bool {
		if flatPosition == len(flatOrder) {
			return true
		}
		index := flatOrder[flatPosition]
		for _, value := range variables[index].allowed {
			if !chainOK(variables, chains, chosen, index, value) ||
				!conflictOK(variables, conflictGroups, chosen, index, value) {
				continue
			}
			chosen[index] = value
			if dfs(flatPosition + 1) {
				return true
			}
			chosen[index] = 0
		}
		return false
	}
	if !dfs(0) {
		return CompleteAssignment{}, false
	}

	assignment := CompleteAssignment{
		ReorderBound: a.bound,
		HorizonLow:   a.horizonLow,
		HorizonHigh:  a.horizonHigh,
		Checkpoints:  append([]uint64(nil), a.anchors...),
		HasCandidate: false,
	}
	for i, variable := range variables {
		assignment.Entries = append(assignment.Entries, AssignedEntry{
			Node:     variable.node,
			Low:      variable.entry.LowSequence,
			Full:     chosen[i],
			Admitted: variable.entry.Admitted,
		})
	}
	return assignment, true
}

func buildConflictGroups(variables []variable) map[uint64][]int {
	groups := map[uint64][]int{}
	for i, variable := range variables {
		groups[variable.entry.LowSequence] = append(groups[variable.entry.LowSequence], i)
	}

	for low, indices := range groups {
		admitted := false
		rejected := false
		for _, index := range indices {
			if variables[index].entry.Admitted {
				admitted = true
			} else {
				rejected = true
			}
		}
		if !(admitted && rejected) {
			delete(groups, low)
		}
	}
	return groups
}

func chainOK(variables []variable, chains map[NodeID][]int, chosen []uint64, index int, value uint64) bool {
	for _, chain := range chains {
		position := -1
		for i, chainIndex := range chain {
			if chainIndex == index {
				position = i
				break
			}
		}
		if position < 0 {
			continue
		}
		if position > 0 {
			previous := chain[position-1]
			if chosen[previous] != 0 && chosen[previous] >= value {
				return false
			}
		}
		if position+1 < len(chain) {
			next := chain[position+1]
			if chosen[next] != 0 && chosen[next] <= value {
				return false
			}
		}
	}
	return true
}

func conflictOK(variables []variable, groups map[uint64][]int, chosen []uint64, index int, value uint64) bool {
	low := variables[index].entry.LowSequence
	if variables[index].node == "" {
		for other, otherVariable := range variables {
			if other == index {
				continue
			}
			if otherVariable.entry.LowSequence == low &&
				otherVariable.entry.Admitted != variables[index].entry.Admitted &&
				chosen[other] == value {
				return false
			}
		}
		return true
	}
	for _, other := range groups[low] {
		if other == index || chosen[other] == 0 {
			continue
		}
		if variables[other].entry.Admitted == variables[index].entry.Admitted {
			continue
		}
		if chosen[other] == value {
			return false
		}
	}
	return true
}
