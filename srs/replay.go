package srs

import "fmt"

// Replay re-simulates a step sequence from start, checking every
// precondition: the rule index is valid, the control state matches,
// the one-shot resources are sufficient (they are consumed, never
// reused), the pattern matches at the recorded position, and the
// result respects the length bound. It returns the final state.
func (sys *System) Replay(start State, steps []Step) (State, error) {
	st := start
	res := make([]int, sys.NumRes)
	copy(res, start.Res)
	st.Res = res
	for i, step := range steps {
		if step.Rule < 0 || step.Rule >= len(sys.Rules) {
			return st, fmt.Errorf("srs: step %d: rule index %d out of range", i, step.Rule)
		}
		r := sys.Rules[step.Rule]
		if st.Ctrl != r.From {
			return st, fmt.Errorf("srs: step %d: rule %q needs control state %d, have %d", i, r.Name, r.From, st.Ctrl)
		}
		if !hasResources(st.Res, r.Need, sys.NumRes) {
			return st, fmt.Errorf("srs: step %d: rule %q needs resources %v, have %v (one-shot resources cannot be reused)", i, r.Name, r.Need, st.Res)
		}
		if step.Pos < 0 || step.Pos+len(r.Left) > len(st.Str) || st.Str[step.Pos:step.Pos+len(r.Left)] != r.Left {
			return st, fmt.Errorf("srs: step %d: rule %q does not match %q at position %d", i, r.Name, st.Str, step.Pos)
		}
		next := st.Str[:step.Pos] + r.Right + st.Str[step.Pos+len(r.Left):]
		if len(next) > sys.MaxLen {
			return st, fmt.Errorf("srs: step %d: result %q exceeds MaxLen %d", i, next, sys.MaxLen)
		}
		st = State{Str: next, Ctrl: r.To, Res: applyResources(st.Res, r.Need, r.Gain, sys.NumRes)}
	}
	return st, nil
}

// VerifyCycle checks cycle evidence: replaying Entry from start must
// reach a state that replaying Cycle returns to exactly — string,
// control state, and resources all included. Because replay enforces
// resource consumption, evidence that secretly depends on reusing a
// one-shot resource is rejected.
func (sys *System) VerifyCycle(start State, entry, cyc []Step) error {
	if len(cyc) == 0 {
		return fmt.Errorf("srs: empty cycle evidence")
	}
	mid, err := sys.Replay(start, entry)
	if err != nil {
		return fmt.Errorf("srs: entry path: %v", err)
	}
	end, err := sys.Replay(mid, cyc)
	if err != nil {
		return fmt.Errorf("srs: cycle: %v", err)
	}
	if !equalStates(mid, end) {
		return fmt.Errorf("srs: cycle evidence does not close: %+v != %+v", mid, end)
	}
	return nil
}

// VerifyDerivation checks that steps form a valid derivation of the
// given length from start.
func (sys *System) VerifyDerivation(start State, steps []Step) error {
	_, err := sys.Replay(start, steps)
	return err
}
