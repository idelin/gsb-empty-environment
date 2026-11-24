package replayhandoff

import "sort"

func (r *Receiver) analyze(material HandoffMaterial, candidate *Frame, state *deviceWindow) analysis {
	result := analysis{
		Decision: Decision{
			Device:     material.Device,
			Generation: material.Generation,
		},
		bound:     r.bound,
		conflicts: map[Conflict]struct{}{},
	}

	if material.ReorderBound != 0 {
		result.bound = material.ReorderBound
		if result.bound > r.half {
			result.addConflict(Conflict{Code: ConflictInvalidReorderBound, Detail: "reorder bound cannot exceed half the wire modulus"})
		}
	}
	if len(material.Checkpoints) == 0 {
		result.Outcome = OutcomeNeedAnchor
		result.NeedAnchorLow = candidateLow(candidate)
		return result
	}

	anchorValues := make([]uint64, 0, len(material.Checkpoints))
	for _, checkpoint := range material.Checkpoints {
		if !r.cfg.Verifier(material.Device, material.Generation, checkpoint.FullSequence, checkpoint.Signature) {
			result.addConflict(Conflict{Code: ConflictBadSignature, Full: checkpoint.FullSequence, Detail: "checkpoint signature verification failed"})
			continue
		}
		anchorValues = append(anchorValues, checkpoint.FullSequence)
		result.anchors = append(result.anchors, checkpoint.FullSequence)
	}
	result.anchors = sortedUnique(result.anchors)
	if result.hasConflict() {
		result.Outcome = OutcomeConflict
		result.Conflicts = result.conflictSlice()
		return result
	}

	minAnchor, maxAnchor := anchorValues[0], anchorValues[0]
	for _, anchor := range anchorValues[1:] {
		if anchor < minAnchor {
			minAnchor = anchor
		}
		if anchor > maxAnchor {
			maxAnchor = anchor
		}
	}
	if maxAnchor-minAnchor > result.bound {
		result.addConflict(Conflict{Code: ConflictInvalidReorderBound, Full: maxAnchor, Detail: "checkpoints exceed the reorder horizon"})
	}
	result.horizonLow = saturatingSub(minAnchor, result.bound)
	result.horizonHigh = saturatingAdd(maxAnchor, result.bound)
	if result.horizonLow > result.horizonHigh {
		result.addConflict(Conflict{Code: ConflictNoCompatibleSequence, Detail: "checkpoint reorder horizon is empty"})
	}

	seenNodes := map[NodeID]struct{}{}
	nodeChains := map[NodeID][]int{}
	for _, log := range material.NodeLogs {
		if _, exists := seenNodes[log.Node]; exists {
			result.addConflict(Conflict{Code: ConflictDuplicateNodeLog, Node: log.Node, Detail: "node log appears more than once"})
			continue
		}
		seenNodes[log.Node] = struct{}{}
		for _, entry := range log.Entries {
			if entry.LowSequence >= r.modulus {
				result.addConflict(Conflict{Code: ConflictLowOutOfRange, Node: log.Node, Low: entry.LowSequence, Detail: "low sequence exceeds wire modulus"})
				continue
			}
			index := len(result.variables)
			result.variables = append(result.variables, variable{node: log.Node, entry: entry})
			nodeChains[log.Node] = append(nodeChains[log.Node], index)
		}
	}

	for i := range result.variables {
		result.variables[i].allowed = matchingValues(result.variables[i].entry.LowSequence, result.horizonLow, result.horizonHigh, r.modulus)
		if len(result.variables[i].allowed) == 0 {
			result.addConflict(Conflict{
				Code:   ConflictNoCompatibleSequence,
				Node:   result.variables[i].node,
				Low:    result.variables[i].entry.LowSequence,
				Detail: "observation has no full sequence inside the reorder horizon",
			})
		}
	}

	for node, chain := range nodeChains {
		if !feasibleChain(result.variables, chain) {
			result.addConflict(Conflict{Code: ConflictUnsatisfiableOrder, Node: node, Detail: "node observations cannot be assigned strictly increasing full sequences"})
		}
	}

	if candidate != nil {
		if candidate.Device != material.Device || candidate.Generation != material.Generation {
			result.addConflict(Conflict{Code: ConflictMissingMaterial, Low: candidate.LowSequence, Detail: "frame device or key generation does not match handoff material"})
			result.Outcome = OutcomeConflict
			result.Conflicts = result.conflictSlice()
			return result
		}
		return r.analyzeCandidate(result, candidate, state)
	}
	return r.analyzeImport(result, state)
}

func (r *Receiver) analyzeImport(result analysis, state *deviceWindow) analysis {
	for _, variable := range result.variables {
		if variable.entry.Admitted {
			continue
		}
		for _, full := range variable.allowed {
			if state.isOld(full, r.half) {
				result.addConflict(Conflict{
					Code:   ConflictConflictingState,
					Node:   variable.node,
					Low:    variable.entry.LowSequence,
					Full:   full,
					Detail: "unadmitted receipt maps to an already accepted sequence",
				})
			}
		}
	}
	if result.hasConflict() {
		result.Outcome = OutcomeConflict
		result.Conflicts = result.conflictSlice()
		return result
	}

	for _, variable := range result.variables {
		if variable.entry.Admitted && len(variable.allowed) > 1 {
			for _, full := range variable.allowed {
				assignment, ok := result.buildImportAssignment(variable.entry.LowSequence, full)
				if ok {
					result.Alternatives = append(result.Alternatives, assignment)
				}
			}
			result.Outcome = OutcomeNeedAnchor
			result.NeedAnchorLow = variable.entry.LowSequence
			return result
		}
	}

	for _, variable := range result.variables {
		if !variable.entry.Admitted {
			continue
		}
		for _, full := range variable.allowed {
			if state.isOld(full, r.half) {
				result.addConflict(Conflict{Code: ConflictConflictingState, Node: variable.node, Low: variable.entry.LowSequence, Full: full, Detail: "admitted receipt maps to an old sequence"})
			}
		}
	}
	if result.hasConflict() {
		result.Outcome = OutcomeConflict
		result.Conflicts = result.conflictSlice()
		return result
	}

	result.Outcome = OutcomeAccepted
	return result
}

func (r *Receiver) analyzeCandidate(result analysis, candidate *Frame, state *deviceWindow) analysis {
	if candidate.LowSequence >= r.modulus {
		result.addConflict(Conflict{Code: ConflictLowOutOfRange, Low: candidate.LowSequence, Detail: "low sequence exceeds wire modulus"})
		result.Outcome = OutcomeConflict
		result.Conflicts = result.conflictSlice()
		return result
	}

	candidateValues := matchingValues(candidate.LowSequence, result.horizonLow, result.horizonHigh, r.modulus)
	if len(candidateValues) == 0 {
		result.Outcome = OutcomeNeedAnchor
		result.NeedAnchorLow = candidate.LowSequence
		return result
	}

	var freshValues, oldValues []uint64
	for _, full := range candidateValues {
		assignment, ok := result.buildAssignment(candidate.LowSequence, full)
		if !ok {
			continue
		}
		assignment.CandidateFresh = !state.isOld(full, r.half)
		result.Alternatives = append(result.Alternatives, assignment)
		if assignment.CandidateFresh {
			freshValues = append(freshValues, full)
		} else {
			oldValues = append(oldValues, full)
		}
	}

	sort.Slice(result.Alternatives, func(i, j int) bool {
		return result.Alternatives[i].CandidateFull < result.Alternatives[j].CandidateFull
	})
	if result.hasConflict() {
		result.Outcome = OutcomeConflict
		result.Conflicts = result.conflictSlice()
		return result
	}
	if len(result.Alternatives) == 0 {
		result.Outcome = OutcomeConflict
		result.Conflicts = []Conflict{{
			Code:   ConflictNoCompatibleSequence,
			Low:    candidate.LowSequence,
			Detail: "candidate cannot satisfy node ordering and state constraints",
		}}
		return result
	}

	if len(freshValues) == 0 {
		result.Outcome = OutcomeRejected
		result.FullSequences = oldValues
		return result
	}
	if len(oldValues) > 0 {
		result.Outcome = OutcomeNeedAnchor
		result.NeedAnchorLow = candidate.LowSequence
		return result
	}

	result.Outcome = OutcomeAccepted
	result.FullSequences = freshValues
	return result
}
