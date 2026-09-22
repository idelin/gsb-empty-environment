package main

import "fmt"

// ShrinkStep records one shrink attempt: the event tentatively removed and
// whether the removal was kept.
type ShrinkStep struct {
	Removed  Action
	Accepted bool
	Reason   string
}

// Shrink greedily removes events from a failing trace while preserving the
// exact same safety-violation signature. A removal is rejected when:
//   - the candidate no longer replays (an event dependency was broken), or
//   - the candidate no longer fails, or fails differently (a timeout /
//     liveness symptom is never accepted as the same safety error).
//
// The result is 1-minimal: removing any single remaining event either breaks
// replay or eliminates the violation.
func Shrink(orig []Action) ([]Action, []ShrinkStep, error) {
	res, err := Replay(orig)
	if err != nil {
		return nil, nil, fmt.Errorf("original trace does not replay: %w", err)
	}
	sig := Violation(res.Decisions)
	if sig == "" {
		return nil, nil, fmt.Errorf("original trace is not a safety violation")
	}

	var steps []ShrinkStep
	trace := append([]Action(nil), orig...)
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(trace); i++ {
			cand := make([]Action, 0, len(trace)-1)
			cand = append(cand, trace[:i]...)
			cand = append(cand, trace[i+1:]...)
			cres, err := Replay(cand)
			step := ShrinkStep{Removed: trace[i]}
			switch {
			case err != nil:
				step.Reason = "rejected: " + err.Error()
			case Violation(cres.Decisions) != sig:
				step.Reason = "rejected: failure not preserved (got " + describe(cres) + ")"
			default:
				step.Accepted = true
				step.Reason = "accepted: safety violation preserved"
				trace = cand
				changed = true
			}
			steps = append(steps, step)
			if step.Accepted {
				break // restart the pass on the shorter trace
			}
		}
	}
	return trace, steps, nil
}
