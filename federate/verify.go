package federate

import "context"

// Verification is the result of running the same query through the federated
// plan and the full-pull reference evaluator.
type Verification struct {
	Plan        *Plan
	Distributed *Result
	Reference   *Result
	Stats       *Stats
	Match       bool
	Diff        string
}

// Verify plans, executes and compares a query against the reference evaluator.
func Verify(ctx context.Context, cat *Catalog, q *Query) (*Verification, error) {
	plan, err := NewPlanner(cat).Plan(q)
	if err != nil {
		return nil, err
	}
	distributed, stats, err := ExecutePlan(ctx, cat, plan)
	if err != nil {
		return nil, err
	}
	reference, err := Reference(ctx, cat, q)
	if err != nil {
		return nil, err
	}
	diff := DiffResults(reference, distributed)
	return &Verification{
		Plan:        plan,
		Distributed: distributed,
		Reference:   reference,
		Stats:       stats,
		Match:       diff == "",
		Diff:        diff,
	}, nil
}
