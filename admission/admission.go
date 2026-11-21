package admission

// Admit evaluates a single release against all of its constraints without
// retaining any state. It returns an error only for malformed input; privacy
// outcomes are returned through Report.Decision. No individual correspondence
// appears in the report.
func Admit(req PublishRequest, opts ...Options) (Report, error) {
	o := DefaultOptions()
	if len(opts) > 0 {
		o = opts[0]
	}
	p, err := build(req, o)
	if err != nil {
		return Report{}, err
	}
	return solveProblem(p), nil
}

func solveProblem(p *problem) Report {
	report := Report{Threshold: p.threshold}

	if len(p.left) != len(p.right) {
		report.Decision = DecisionInfeasible
		report.Reason = "unequal numbers of L and R batches: no complete pairing exists"
		return report
	}
	if isolated := p.isolatedBatch(); isolated != "" {
		report.Decision = DecisionInfeasible
		report.Reason = "batch " + isolated + " has no candidate edge: no complete pairing exists"
		return report
	}

	s := newSolver(p)
	if !s.feasible(-1, false) {
		report.Decision = DecisionInfeasible
		report.Reason = "no complete pairing satisfies every quota and shared-decision constraint"
		return report
	}

	below := false
	for _, batchID := range p.protected {
		entry := s.ambiguity(batchID)
		report.Batches = append(report.Batches, entry)
		if entry.PossibleJointCategories < p.threshold {
			below = true
		}
	}

	if below {
		report.Decision = DecisionBelowThreshold
		report.Reason = "one or more protected batches have fewer possible joint categories than the privacy threshold"
		return report
	}
	report.Decision = DecisionPublishable
	return report
}

// ambiguity counts, for one protected batch, the distinct joint categories and
// counterpart-side categories compatible with at least one globally feasible
// complete pairing. It never counts merely locally incident edges.
func (s *solver) ambiguity(batchID string) BatchReport {
	p := s.p
	jointSeen := make(map[string]bool)
	counterpartSeen := make(map[string]bool)

	for ei, e := range p.edges {
		if e.L != batchID && e.R != batchID {
			continue
		}
		gi := p.edgeGroup[ei]
		// The edge can realize its joint category only if a global pairing
		// exists while its whole decision group is accepted.
		if !s.feasible(gi, true) {
			continue
		}
		jointSeen[e.JointCategory] = true
		counterpartID := e.R
		if batchID == e.R {
			counterpartID = e.L
		}
		counterpartSeen[p.batches[counterpartID].Category] = true
	}

	return BatchReport{
		BatchID:                       batchID,
		PossibleJointCategories:       len(jointSeen),
		PossibleCounterpartCategories: len(counterpartSeen),
	}
}
