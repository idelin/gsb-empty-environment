package main

import "fmt"

// Replay independently re-executes a trace against the spec from scratch
// and reports the resulting unreleased resources. It is a deliberately
// separate implementation from the explorer: it re-validates script
// order, action existence, outcome availability, and guards step by step.
// A non-nil error means the trace is not a valid execution of the spec.
func Replay(spec *Spec, trace []Step) (unrel []string, err error) {
	// Fresh resource valuation from the declared initial state.
	res := make(map[string]string, len(spec.Resources))
	for _, r := range spec.Resources {
		res[r] = spec.Initial[r]
	}

	// Per-script program counters, keyed by script name.
	pc := make(map[string]int, len(spec.Scripts))
	byName := make(map[string]Script, len(spec.Scripts))
	for _, sc := range spec.Scripts {
		byName[sc.Name] = sc
	}

	for i, step := range trace {
		sc, ok := byName[step.Script]
		if !ok {
			return nil, fmt.Errorf("step %d: unknown script %q", i, step.Script)
		}
		pos := pc[step.Script]
		if pos >= len(sc.Actions) {
			return nil, fmt.Errorf("step %d: script %q already finished", i, step.Script)
		}
		want := sc.Actions[pos]
		if want != step.Action {
			return nil, fmt.Errorf("step %d: script %q order violated: expected action %q, got %q",
				i, step.Script, want, step.Action)
		}
		act, ok := spec.Actions[step.Action]
		if !ok {
			return nil, fmt.Errorf("step %d: unknown action %q", i, step.Action)
		}

		// Find the recorded public outcome among the action's transitions
		// and require its guard to hold in the current state.
		fired := false
		for _, tr := range act.Transitions {
			if tr.Outcome != step.Outcome {
				continue
			}
			match := true
			for r, wantState := range tr.When {
				if res[r] != wantState {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			for r, newState := range tr.Set {
				res[r] = newState
			}
			fired = true
			break
		}
		if !fired {
			return nil, fmt.Errorf("step %d: no enabled %q transition with outcome %q for action %q",
				i, step.Outcome, step.Outcome, step.Action)
		}
		pc[step.Script] = pos + 1
	}

	// A counterexample trace must be complete: every script ran to its end.
	for _, sc := range spec.Scripts {
		if pc[sc.Name] != len(sc.Actions) {
			return nil, fmt.Errorf("trace incomplete: script %q stopped at %d/%d actions",
				sc.Name, pc[sc.Name], len(sc.Actions))
		}
	}

	return unreleased(spec, res), nil
}

// VerifyCounterexample replays a violation trace and confirms it is a
// genuine, complete execution that ends with unreleased resources.
func VerifyCounterexample(spec *Spec, res Result) error {
	if res.Status != StatusViolation {
		return fmt.Errorf("nothing to verify: status is %q", res.Status)
	}
	unrel, err := Replay(spec, res.Trace)
	if err != nil {
		return fmt.Errorf("replay rejected trace: %v", err)
	}
	if len(unrel) == 0 {
		return fmt.Errorf("replay ended with all resources released; trace is not a counterexample")
	}
	return nil
}
