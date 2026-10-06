package checker

import "fmt"

// Replay re-executes a trace step by step from the system's initial state,
// independently verifying every step: the acting machine must be in the
// transition's From state, the action must be legal against the current
// queue contents (capacity for sends, head message for receives), and the
// recorded post-state must match the recomputed one exactly, queues included.
// It returns the final state, or an error describing the first violation.
func (sys *System) Replay(trace []Step) (State, error) {
	s := sys.Initial()
	for i, st := range trace {
		if st.Who != 0 && st.Who != 1 {
			return s, fmt.Errorf("step %d: invalid actor %d", i, st.Who)
		}
		m := sys.A
		cur := s.A
		if st.Who == 1 {
			m = sys.B
			cur = s.B
		}
		t := st.Transition
		if t.From != cur {
			return s, fmt.Errorf("step %d: machine %s is in state %q but transition starts at %q", i, m.Name, cur, t.From)
		}
		declared := false
		for _, dt := range m.Transitions {
			if dt == t {
				declared = true
				break
			}
		}
		if !declared {
			return s, fmt.Errorf("step %d: transition %s is not declared in machine %s", i, t, m.Name)
		}
		switch t.Act.Kind {
		case Tau:
			// always legal
		case Send:
			out := s.QAB
			if st.Who == 1 {
				out = s.QBA
			}
			if len(out) >= QueueCap {
				return s, fmt.Errorf("step %d: illegal send(%s): outgoing queue full %v", i, t.Act.Message, out)
			}
		case Recv:
			in := s.QBA
			if st.Who == 1 {
				in = s.QAB
			}
			if len(in) == 0 {
				return s, fmt.Errorf("step %d: illegal recv(%s): incoming queue empty", i, t.Act.Message)
			}
			if in[0] != t.Act.Message {
				return s, fmt.Errorf("step %d: illegal recv(%s): incoming queue head is %q", i, t.Act.Message, in[0])
			}
		default:
			return s, fmt.Errorf("step %d: unknown action kind", i)
		}
		next := apply(s, st.Who, t)
		if next.Key() != st.After.Key() {
			return s, fmt.Errorf("step %d: recorded post-state %s does not match recomputed %s", i, st.After, next)
		}
		s = next
	}
	return s, nil
}

// VerifyDeadlockTrace replays a claimed deadlock trace and additionally
// checks that the reached state is genuinely a deadlock.
func (sys *System) VerifyDeadlockTrace(trace []Step) error {
	s, err := sys.Replay(trace)
	if err != nil {
		return err
	}
	if !sys.Deadlock(s) {
		return fmt.Errorf("final state %s is not a deadlock", s)
	}
	return nil
}
