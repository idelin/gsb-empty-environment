package nvmerecov

import "sort"

// Analyze determines whether all observations are explained by one legal
// execution prefix and, if so, reports every distinct outcome for each request
// whose successful receipt was lost.
func Analyze(model Model, observation Observation) (*Analysis, error) {
	m, err := normalizeModel(model)
	if err != nil {
		return nil, err
	}
	cons, finals, err := normalizeObservation(m, observation, nil)
	if err != nil {
		return nil, err
	}

	receiptSeen := make(map[RequestID]bool, len(observation.Receipts))
	for _, receipt := range observation.Receipts {
		receiptSeen[receipt.Request] = true
	}
	missing := make([]int, 0)
	for ri, ref := range m.reqs {
		if !receiptSeen[ref.id] {
			missing = append(missing, ri)
		}
	}

	graph, err := buildGraph(m, cons, finals, false)
	if err != nil {
		return nil, err
	}
	if graph.firstGood < 0 {
		reason := "no legal prefix explains the breakpoint observations"
		if cons.contradiction {
			reason = "the observations contain contradictory receipts, assumptions, or zone facts"
		}
		return &Analysis{Valid: false, Reason: reason}, nil
	}

	prefix, _ := graph.firstValidPrefix()
	missingResults := analyzeGraph(graph, missing)
	sort.Slice(missingResults, func(i, j int) bool {
		return missingResults[i].Request < missingResults[j].Request
	})

	analysis := &Analysis{
		Valid:   true,
		Prefix:  prefix,
		Missing: missingResults,
	}
	// Prefer two prefixes that demonstrate two different executed results,
	// which is the condition that blocks publishing a completion.
	for _, result := range missingResults {
		var executedWitnesses []Prefix
		for i, outcome := range result.Possible {
			if outcome.Kind != OutcomeNotExecuted {
				executedWitnesses = append(executedWitnesses, result.Witnesses[i])
			}
		}
		if len(executedWitnesses) >= 2 {
			analysis.Witnesses = append(analysis.Witnesses, executedWitnesses[0], executedWitnesses[1])
			break
		}
	}
	if len(analysis.Witnesses) == 0 {
		for _, result := range missingResults {
			if len(result.Possible) > 1 && len(result.Witnesses) >= 2 {
				analysis.Witnesses = append(analysis.Witnesses, result.Witnesses[0], result.Witnesses[1])
				break
			}
		}
	}
	return analysis, nil
}

// Recover is an alias for Analyze.
func Recover(model Model, observation Observation) (*Analysis, error) {
	return Analyze(model, observation)
}

// QueryJoint answers a batch of host-supplied hypothetical receipts. It checks
// joint satisfiability, not merely whether each assumption is individually
// possible, and returns one prefix satisfying the observations and all
// assumptions together.
func QueryJoint(model Model, observation Observation, assumptions []Assumption) (*JointResult, error) {
	m, err := normalizeModel(model)
	if err != nil {
		return nil, err
	}
	cons, finals, err := normalizeObservation(m, observation, assumptions)
	if err != nil {
		return nil, err
	}
	graph, err := buildGraph(m, cons, finals, true)
	if err != nil {
		return nil, err
	}
	if graph.firstGood < 0 {
		reason := "no single legal prefix satisfies all assumptions"
		if cons.contradiction {
			reason = "assumptions are contradictory with each other or with observed receipts"
		}
		return &JointResult{Satisfiable: false, Reason: reason}, nil
	}
	prefix, _ := graph.firstValidPrefix()
	return &JointResult{Satisfiable: true, Prefix: prefix}, nil
}
