package rescheck

import "fmt"

// Verify independently replays a trace against the spec and confirms it is
// a genuine counterexample: the steps form an order-preserving interleaving
// of the scripts, every recorded transition (including public failure
// transitions) matches the resource machines, all scripts run to
// completion, and at least one resource ends unreleased. It shares no state
// with the explorer, so an explorer bug cannot self-certify a bad trace.
func (s *Spec) Verify(trace []Step) error {
	if err := s.Validate(); err != nil {
		return err
	}
	ms := s.machines()
	ridx := s.resourceIndex()
	sidx := map[string]int{}
	for i := range s.Scripts {
		sidx[s.Scripts[i].Name] = i
	}

	states := make([]string, len(s.Resources))
	for i, r := range s.Resources {
		states[i] = r.Initial
	}
	pcs := make([]int, len(s.Scripts))

	for i, st := range trace {
		si, ok := sidx[st.Script]
		if !ok {
			return fmt.Errorf("step %d: unknown script %q", i, st.Script)
		}
		sc := &s.Scripts[si]
		if pcs[si] >= len(sc.Actions) {
			return fmt.Errorf("step %d: script %q already finished", i, st.Script)
		}
		want := sc.Actions[pcs[si]]
		if want.Resource != st.Resource || want.Op != st.Op {
			return fmt.Errorf("step %d: script %q next action is %s.%s, trace has %s.%s",
				i, st.Script, want.Resource, want.Op, st.Resource, st.Op)
		}
		ri, ok := ridx[st.Resource]
		if !ok {
			return fmt.Errorf("step %d: unknown resource %q", i, st.Resource)
		}
		if states[ri] != st.From {
			return fmt.Errorf("step %d: resource %q is in state %q, trace claims %q",
				i, st.Resource, states[ri], st.From)
		}
		next, result := ms[ri].step(states[ri], st.Op)
		if next != st.To {
			return fmt.Errorf("step %d: %s.%s moves to %q, trace claims %q",
				i, st.Resource, st.Op, next, st.To)
		}
		if result != st.Result {
			return fmt.Errorf("step %d: %s.%s yields result %q, trace claims %q",
				i, st.Resource, st.Op, result, st.Result)
		}
		states[ri] = next
		pcs[si]++
	}

	for i := range s.Scripts {
		if pcs[i] != len(s.Scripts[i].Actions) {
			return fmt.Errorf("script %q did not run to completion (%d/%d actions)",
				s.Scripts[i].Name, pcs[i], len(s.Scripts[i].Actions))
		}
	}
	leaked := leakedResources(s, states)
	if len(leaked) == 0 {
		return fmt.Errorf("trace ends with all resources released; not a counterexample")
	}
	return nil
}
