package migrationaudit

import "sort"

func (indexed *indexedProblem) buildResult(order []int, states []*auditState, targetPeak int, lowerMemory *feasibilityMemory, optimalMemory *optimizationMemory, upperStats SearchStats, lowerStats SearchStats, optimizationStats SearchStats) *Result {
	prefixes := make([]PrefixSnapshot, 0, len(states)-1)
	total := 0
	initialExposures := indexed.exposureAt(states[0])
	peak, peakGroup := indexed.worstExposure(initialExposures)
	peakStep := 0
	for step := 1; step < len(states); step++ {
		state := states[step]
		exposures := indexed.exposureAt(state)
		worstCount, worstGroup := indexed.worstExposure(exposures)
		prefix := indexed.snapshot(step, order[step-1], state, exposures, worstCount, worstGroup)
		prefixes = append(prefixes, prefix)
		total += worstCount
		if worstCount > peak {
			peak = worstCount
			peakStep = step
			peakGroup = worstGroup
		}
	}

	var peakDigests []DigestID
	var peakAccounts []AccountID
	var peakGroupID ObserverGroupID
	if peakGroup >= 0 {
		peakExposure := indexed.exposureAt(states[peakStep])[peakGroup]
		peakGroupID = indexed.problem.ObserverGroups[peakGroup].ID
		peakDigests = make([]DigestID, 0, len(peakExposure.lowCostDigests))
		for _, digestIndex := range peakExposure.lowCostDigests {
			peakDigests = append(peakDigests, indexed.problem.Digests[digestIndex].ID)
		}
		for _, accountIndex := range bitSetIndexes(peakExposure.exposedAccounts) {
			peakAccounts = append(peakAccounts, indexed.accountOrder[accountIndex])
		}
	}

	actionOrder := make([]int, len(order))
	for index, actionIndex := range order {
		actionOrder[index] = indexed.problem.Actions[actionIndex].ID
	}
	initialInclusive := peak

	return &Result{
		Feasible:                       true,
		Order:                          actionOrder,
		Prefixes:                       prefixes,
		Peak:                           peak,
		PeakStep:                       peakStep,
		PeakGroupID:                    peakGroupID,
		PeakDigestIDs:                  peakDigests,
		PeakAccountIDs:                 peakAccounts,
		TotalPrefixExposure:            total,
		InitialInclusivePrefixExposure: initialInclusive + total,
		Certificate:                    indexed.certificate(states[0].structuredKey(), targetPeak, lowerMemory, optimalMemory),
		Stats: SearchStats{
			ReachableStates:              upperStats.ReachableStates + lowerStats.ReachableStates + optimizationStats.ReachableStates,
			FeasibilityCertificateStates: upperStats.ReachableStates + lowerStats.ReachableStates,
			OptimizationStates:           optimizationStats.ReachableStates,
			TransitionChecks:             upperStats.TransitionChecks + lowerStats.TransitionChecks + optimizationStats.TransitionChecks,
			PrunedByExposureCap:          upperStats.PrunedByExposureCap + lowerStats.PrunedByExposureCap + optimizationStats.PrunedByExposureCap,
		},
	}
}

func (indexed *indexedProblem) snapshot(step int, lastAction int, state *auditState, exposures []groupExposure, worstCount int, worstGroup int) PrefixSnapshot {
	executed := make([]int, 0, state.executed.count())
	for _, actionIndex := range bitSetIndexes(state.executed) {
		executed = append(executed, indexed.problem.Actions[actionIndex].ID)
	}
	groupSnapshots := make([]GroupExposureSnapshot, len(exposures))
	for index, exposure := range exposures {
		groupSnapshots[index] = indexed.groupSnapshot(exposure)
	}
	actionID := indexed.problem.Actions[lastAction].ID
	return PrefixSnapshot{
		Step:                     step,
		ActionID:                 actionID,
		ExecutedActionIDs:        executed,
		WorstGroupID:             indexed.problem.ObserverGroups[worstGroup].ID,
		WorstExposedAccountCount: worstCount,
		GroupExposures:           groupSnapshots,
	}
}

func (indexed *indexedProblem) groupSnapshot(exposure groupExposure) GroupExposureSnapshot {
	observed := make([]DigestID, 0, len(exposure.observedDigests))
	for _, digestIndex := range exposure.observedDigests {
		observed = append(observed, indexed.problem.Digests[digestIndex].ID)
	}
	lowCost := make([]DigestID, 0, len(exposure.lowCostDigests))
	for _, digestIndex := range exposure.lowCostDigests {
		lowCost = append(lowCost, indexed.problem.Digests[digestIndex].ID)
	}
	accounts := make([]AccountID, 0, exposure.exposedAccounts.count())
	for _, accountIndex := range bitSetIndexes(exposure.exposedAccounts) {
		accounts = append(accounts, indexed.accountOrder[accountIndex])
	}
	costs := make([]FamilySaltCost, 0)
	for familyIndex, known := range exposure.minimumKnown {
		if !known {
			continue
		}
		key := indexed.familySaltList[familyIndex]
		costs = append(costs, FamilySaltCost{
			Family:      key.Family,
			SaltDomain:  key.SaltDomain,
			MinimumCost: exposure.minimumCosts[familyIndex],
		})
	}
	return GroupExposureSnapshot{
		GroupID:                indexed.problem.ObserverGroups[exposure.groupIndex].ID,
		ObservedDigests:        observed,
		LowCostObservedDigests: lowCost,
		ExposedAccounts:        accounts,
		MinimumCosts:           costs,
	}
}

func (indexed *indexedProblem) certificate(initialKey stateKey, targetPeak int, lowerMemory *feasibilityMemory, optimalMemory *optimizationMemory) *Certificate {
	lowerCap := -1
	feasibility := make(map[string]FeasibilityCertificateEntry)
	if lowerMemory != nil {
		lowerCap = lowerMemory.cap
		for key, entry := range lowerMemory.entries {
			feasibility[key.String()] = FeasibilityCertificateEntry{
				StateKey:       key.String(),
				Feasible:       entry.feasible,
				ChosenActionID: indexed.actionID(entry.chosenAction),
				LegalActionIDs: indexed.actionIDs(entry.legalActions),
			}
		}
	}
	optimization := make(map[string]OptimizationCertificateEntry, len(optimalMemory.entries))
	for key, entry := range optimalMemory.entries {
		successorKey := ""
		if entry.chosenAction >= 0 {
			successorKey = entry.successorKey.String()
		}
		optimization[key.String()] = OptimizationCertificateEntry{
			StateKey:             key.String(),
			Feasible:             entry.feasible,
			RemainingExposureSum: entry.remainingSum,
			ChosenActionID:       indexed.actionID(entry.chosenAction),
			TransitionExposure:   entry.transitionExposure,
			SuccessorStateKey:    successorKey,
			LegalActionIDs:       indexed.actionIDs(entry.legalActions),
		}
	}
	return &Certificate{
		TargetPeak:          targetPeak,
		LowerCap:            lowerCap,
		LowerCapFeasibility: feasibility,
		Optimization:        optimization,
		InitialStateKey:     initialKey.String(),
	}
}

func (indexed *indexedProblem) actionID(actionIndex int) int {
	if actionIndex < 0 {
		return -1
	}
	return indexed.problem.Actions[actionIndex].ID
}

func (indexed *indexedProblem) actionIDs(actionIndexes []int) []int {
	ids := make([]int, len(actionIndexes))
	for index, actionIndex := range actionIndexes {
		ids[index] = indexed.actionID(actionIndex)
	}
	sort.Ints(ids)
	return ids
}
