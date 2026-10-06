package petrinet

import "fmt"

// Check runs the reachability search and, when a sequence is found,
// independently re-executes it step by step to confirm it reaches the
// target. A verification failure is reported as an error.
func (n *Net) Check(initial, target []int) (*Result, error) {
	res, err := n.Reachable(initial, target)
	if err != nil {
		return nil, err
	}
	if res.Reachable {
		if err := n.Verify(initial, target, res.Sequence); err != nil {
			return nil, fmt.Errorf("petrinet: verification failed: %v", err)
		}
	}
	return res, nil
}

// Verify independently replays a firing sequence step by step: every step
// must be enabled (enough tokens to consume, capacity respected after
// production), and the final marking must equal target.
func (n *Net) Verify(initial, target []int, seq []int) error {
	if err := n.Validate(); err != nil {
		return err
	}
	if err := n.CheckMarking(initial); err != nil {
		return fmt.Errorf("initial marking: %v", err)
	}
	if err := n.CheckMarking(target); err != nil {
		return fmt.Errorf("target marking: %v", err)
	}
	m := append([]int(nil), initial...)
	for i, t := range seq {
		if t < 0 || t >= len(n.Transitions) {
			return fmt.Errorf("step %d: invalid transition index %d", i, t)
		}
		next, ok := n.Fire(m, t)
		if !ok {
			return fmt.Errorf("step %d: transition %q is disabled at marking %v", i, n.Transitions[t].Name, m)
		}
		m = next
	}
	for p := range m {
		if m[p] != target[p] {
			return fmt.Errorf("final marking %v does not match target %v", m, target)
		}
	}
	return nil
}
