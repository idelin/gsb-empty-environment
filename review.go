package netsec

// AdditionReview is the all-or-nothing result of adding exports to an existing
// material set. Accepted is false if the addition exposes any private path
// that was safe in the baseline.
type AdditionReview struct {
	Accepted     bool       `json:"accepted"`
	NewlyExposed []Exposure `json:"newly_exposed,omitempty"`
	Baseline     *Report    `json:"baseline"`
	Proposed     *Report    `json:"proposed"`
}

// ReviewAddition audits existing and existing+additions, then rejects the
// whole addition if any path becomes newly exposed. It never returns a
// partially applied result.
func ReviewAddition(tree *Tree, existing, additions []Material) (*AdditionReview, error) {
	return NewAuditor(tree).ReviewAddition(existing, additions)
}

// ReviewAddition is the Auditor form of ReviewAddition.
func (a *Auditor) ReviewAddition(existing, additions []Material) (*AdditionReview, error) {
	baseline, err := a.Audit(existing)
	if err != nil {
		return nil, err
	}

	proposedMaterials := make([]Material, 0, len(existing)+len(additions))
	proposedMaterials = append(proposedMaterials, existing...)
	proposedMaterials = append(proposedMaterials, additions...)

	proposed, err := a.Audit(proposedMaterials)
	if err != nil {
		return nil, err
	}

	baselineExposed := make(map[string]bool, len(baseline.Exposed))
	for _, exposure := range baseline.Exposed {
		baselineExposed[exposure.Path.String()] = true
	}

	newlyExposed := make([]Exposure, 0)
	for _, exposure := range proposed.Exposed {
		if baselineExposed[exposure.Path.String()] {
			continue
		}
		newlyExposed = append(newlyExposed, Exposure{
			Path:  exposure.Path.Clone(),
			Chain: cloneChain(exposure.Chain),
		})
	}

	return &AdditionReview{
		Accepted:     len(newlyExposed) == 0,
		NewlyExposed: newlyExposed,
		Baseline:     baseline,
		Proposed:     proposed,
	}, nil
}
