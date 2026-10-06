package plancmp

import "fmt"

// replay re-executes the already-executed prefix from the initial state and
// returns the resulting state together with the committed assignment: for
// every variable touched by an executed operation, the value established by
// the last executed operation touching it. It validates that dependencies
// and preconditions held at each step.
func (p *Plan) replay(initial State, executed []string) (State, Assign, error) {
	if err := checkNoDuplicates(executed); err != nil {
		return 0, nil, err
	}
	done := make(map[string]bool, len(executed))
	commits := Assign{}
	state := initial
	for _, name := range executed {
		op, ok := p.Ops[name]
		if !ok {
			return 0, nil, fmt.Errorf("plancmp: executed op %q is not in the plan", name)
		}
		for _, dep := range op.Deps {
			if !done[dep] {
				return 0, nil, fmt.Errorf("plancmp: executed op %q ran before dependency %q", name, dep)
			}
		}
		if !op.Pre.satisfiedBy(state) {
			return 0, nil, fmt.Errorf("plancmp: executed op %q preconditions not met at replay", name)
		}
		state = state.apply(op.Eff)
		for v, val := range op.Eff {
			commits[v] = val
		}
		done[name] = true
	}
	return state, commits, nil
}

// Verify replays the executed prefix followed by the supplementary steps from
// the initial state. It fails if any operation is unknown, runs before its
// dependencies, has unmet preconditions, or silently resets a variable that
// was committed by an executed operation, or if the goal is not satisfied at
// the end. It is used internally to double-check every Reconcile solution and
// is exported so callers can independently audit a proposed sequence.
func (p *Plan) Verify(initial State, executed, steps []string, goal Assign) error {
	if err := p.validate(); err != nil {
		return err
	}
	if err := checkAssign(goal); err != nil {
		return fmt.Errorf("plancmp: goal: %v", err)
	}
	if err := checkNoDuplicates(steps); err != nil {
		return err
	}
	state, commits, err := p.replay(initial, executed)
	if err != nil {
		return err
	}
	done := make(map[string]bool, len(executed)+len(steps))
	for _, name := range executed {
		done[name] = true
	}
	for _, name := range steps {
		op, ok := p.Ops[name]
		if !ok {
			return fmt.Errorf("plancmp: step %q is not in the plan", name)
		}
		if done[name] {
			return fmt.Errorf("plancmp: step %q was already executed", name)
		}
		for _, dep := range op.Deps {
			if !done[dep] {
				return fmt.Errorf("plancmp: step %q runs before dependency %q", name, dep)
			}
		}
		if !op.Pre.satisfiedBy(state) {
			return fmt.Errorf("plancmp: step %q preconditions not met", name)
		}
		for v, val := range op.Eff {
			if cv, ok := commits[v]; ok && cv != val {
				return fmt.Errorf("plancmp: step %q silently resets committed variable %d to %v", name, v, val)
			}
		}
		state = state.apply(op.Eff)
		done[name] = true
	}
	if !goal.satisfiedBy(state) {
		return fmt.Errorf("plancmp: goal not reached after replay")
	}
	return nil
}

func checkNoDuplicates(names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("plancmp: op %q listed twice", name)
		}
		seen[name] = true
	}
	return nil
}
