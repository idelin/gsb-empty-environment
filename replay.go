package plancmp

import "fmt"

// Replay applies the operation sequence seq to initial, checking every step:
// dependencies must be satisfied by preApplied or earlier sequence entries,
// preconditions must hold, and no step may silently reset a committed
// variable (a variable in committed must keep its value from initial).
// It returns the final state, or the first violation as an error.
func Replay(initial State, committed State, plan *Plan, preApplied, seq []string) (State, error) {
	byName := make(map[string]*Op, len(plan.Ops))
	for i := range plan.Ops {
		byName[plan.Ops[i].Name] = &plan.Ops[i]
	}
	applied := make(map[string]bool, len(preApplied)+len(seq))
	for _, name := range preApplied {
		applied[name] = true
	}
	lockedOn := committed & initial
	lockedOff := committed &^ initial

	state := initial
	for step, name := range seq {
		op, ok := byName[name]
		if !ok {
			return state, fmt.Errorf("plancmp: replay step %d: unknown operation %q", step, name)
		}
		if applied[name] {
			return state, fmt.Errorf("plancmp: replay step %d: operation %q applied twice", step, name)
		}
		for _, dep := range op.Deps {
			if !applied[dep] {
				return state, fmt.Errorf("plancmp: replay step %d: operation %q applied before dependency %q", step, name, dep)
			}
		}
		if !appliesTo(op, state) {
			return state, fmt.Errorf("plancmp: replay step %d: preconditions of operation %q not met in state %#x", step, name, state)
		}
		if !respectsCommitment(op, lockedOn, lockedOff) {
			return state, fmt.Errorf("plancmp: replay step %d: operation %q silently resets a committed variable", step, name)
		}
		state = apply(op, state)
		applied[name] = true
	}
	return state, nil
}
