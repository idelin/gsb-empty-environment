package replayhandoff

type exhaustiveResult struct {
	assignments []exhaustiveAssignment
	fresh       []uint64
	old         []uint64
}

type exhaustiveAssignment struct {
	values map[nodeEntryKey]uint64
}

type nodeEntryKey struct {
	node NodeID
	low  uint64
	pos  int
}

func exhaustiveInterpret(material HandoffMaterial, candidateLow, bound, modulus uint64, window *deviceWindow) exhaustiveResult {
	horizonLow, horizonHigh := exhaustiveHorizon(material, bound, modulus)
	variables := exhaustiveVariables(material, modulus, horizonLow, horizonHigh)
	chosen := make([]uint64, len(variables))
	result := exhaustiveResult{}

	var enumerate func(index int)
	enumerate = func(index int) {
		if index == len(variables) {
			if exhaustiveValid(material, variables, chosen, horizonLow, horizonHigh, modulus) {
				assignment := exhaustiveAssignment{values: map[nodeEntryKey]uint64{}}
				for i, variable := range variables {
					assignment.values[variable.key] = chosen[i]
				}
				result.assignments = append(result.assignments, assignment)
			}
			return
		}

		for _, value := range variables[index].values {
			chosen[index] = value
			enumerate(index + 1)
		}
	}
	enumerate(0)

	candidateValues := matchingValues(candidateLow, horizonLow, horizonHigh, modulus)
	for _, full := range candidateValues {
		if exhaustiveCandidatePossible(result.assignments, variables, material, candidateLow, full) {
			if window != nil && window.isOld(full, modulus/2) {
				result.old = append(result.old, full)
			} else {
				result.fresh = append(result.fresh, full)
			}
		}
	}
	return result
}

type exhaustiveVariable struct {
	key    nodeEntryKey
	entry  LogEntry
	values []uint64
}

func exhaustiveHorizon(material HandoffMaterial, bound, modulus uint64) (uint64, uint64) {
	minAnchor := material.Checkpoints[0].FullSequence
	maxAnchor := minAnchor
	for _, checkpoint := range material.Checkpoints[1:] {
		if checkpoint.FullSequence < minAnchor {
			minAnchor = checkpoint.FullSequence
		}
		if checkpoint.FullSequence > maxAnchor {
			maxAnchor = checkpoint.FullSequence
		}
	}
	return saturatingSub(minAnchor, bound), saturatingAdd(maxAnchor, bound)
}

func exhaustiveVariables(material HandoffMaterial, modulus, horizonLow, horizonHigh uint64) []exhaustiveVariable {
	variables := make([]exhaustiveVariable, 0)
	for _, log := range material.NodeLogs {
		for position, entry := range log.Entries {
			variables = append(variables, exhaustiveVariable{
				key: nodeEntryKey{
					node: log.Node,
					low:  entry.LowSequence,
					pos:  position,
				},
				entry:  entry,
				values: matchingValues(entry.LowSequence, horizonLow, horizonHigh, modulus),
			})
		}
	}
	return variables
}

func exhaustiveValid(material HandoffMaterial, variables []exhaustiveVariable, chosen []uint64, horizonLow, horizonHigh, modulus uint64) bool {
	for i, variable := range variables {
		if chosen[i] < horizonLow || chosen[i] > horizonHigh || chosen[i]%modulus != variable.entry.LowSequence {
			return false
		}
	}

	for _, log := range material.NodeLogs {
		previous := uint64(0)
		first := true
		for position := range log.Entries {
			key := nodeEntryKey{node: log.Node, low: log.Entries[position].LowSequence, pos: position}
			value := exhaustiveChosen(variables, chosen, key)
			if !first && value <= previous {
				return false
			}
			previous = value
			first = false
		}
	}

	for i := range variables {
		for j := i + 1; j < len(variables); j++ {
			if variables[i].entry.LowSequence != variables[j].entry.LowSequence || chosen[i] != chosen[j] {
				continue
			}
			if variables[i].entry.Admitted != variables[j].entry.Admitted {
				return false
			}
		}
	}
	return true
}

func exhaustiveChosen(variables []exhaustiveVariable, chosen []uint64, key nodeEntryKey) uint64 {
	for i, variable := range variables {
		if variable.key == key {
			return chosen[i]
		}
	}
	return 0
}

func exhaustiveCandidatePossible(assignments []exhaustiveAssignment, variables []exhaustiveVariable, material HandoffMaterial, candidateLow, candidateFull uint64) bool {
assignmentLoop:
	for _, log := range material.NodeLogs {
		for position, entry := range log.Entries {
			if entry.LowSequence != candidateLow || entry.Admitted {
				continue
			}
			for _, assignment := range assignments {
				key := nodeEntryKey{node: log.Node, low: entry.LowSequence, pos: position}
				if assignment.values[key] == candidateFull {
					continue assignmentLoop
				}
			}
		}
	}
	return len(assignments) > 0
}
