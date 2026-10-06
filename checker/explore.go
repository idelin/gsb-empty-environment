package checker

import "fmt"

// FindDeadlock breadth-first explores the composite state space of the two
// machines starting from the initial state and returns a shortest step trace
// to a deadlock state, plus the deadlock state itself. It returns nil if no
// deadlock is reachable. BFS over the finite composite space guarantees the
// trace is shortest; no fairness assumption is made about scheduling.
func FindDeadlock(m [2]Machine) ([]Step, State, bool) {
	start := Initial()
	type node struct {
		s    State
		prev *node
		via  Step // step that produced s from prev
	}
	visited := map[string]bool{start.key(): true}
	root := &node{s: start}
	queue := []*node{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if Deadlock(cur.s, m) {
			var steps []Step
			for n := cur; n.prev != nil; n = n.prev {
				steps = append(steps, n.via)
			}
			for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
				steps[i], steps[j] = steps[j], steps[i]
			}
			return steps, cur.s, true
		}
		for _, st := range EnabledSteps(cur.s, m) {
			next := Apply(cur.s, st.Machine, st.Trans)
			k := next.key()
			if visited[k] {
				continue
			}
			visited[k] = true
			queue = append(queue, &node{s: next, prev: cur, via: st})
		}
	}
	return nil, State{}, false
}

// VerifyTrace replays steps from the initial state, checking at every step
// that the transition is enabled (including queue contents and capacity) and
// that the recorded machine/transition matches a real edge of the machine.
// It returns the resulting state, or an error identifying the illegal step.
func VerifyTrace(m [2]Machine, steps []Step) (State, error) {
	s := Initial()
	for idx, st := range steps {
		if st.Machine < 0 || st.Machine > 1 {
			return s, fmt.Errorf("step %d: invalid machine %d", idx, st.Machine)
		}
		mach := m[st.Machine]
		// The step must correspond to an actual transition edge.
		found := false
		for _, t := range mach.Trans[s.Loc[st.Machine]] {
			if t == st.Trans {
				found = true
				break
			}
		}
		if !found {
			return s, fmt.Errorf("step %d: machine %s has no transition %s from local state %d",
				idx, mach.Name, st.Trans, s.Loc[st.Machine])
		}
		if !Enabled(s, st.Machine, st.Trans) {
			return s, fmt.Errorf("step %d: %s not enabled in %s (queues %v / %v)",
				idx, st.Trans, mach.Name, s.Q[0], s.Q[1])
		}
		s = Apply(s, st.Machine, st.Trans)
	}
	return s, nil
}

// CheckDeadlockTrace verifies that steps form a legal trace from the initial
// state ending in a deadlock state. It returns the final state on success.
func CheckDeadlockTrace(m [2]Machine, steps []Step) (State, error) {
	s, err := VerifyTrace(m, steps)
	if err != nil {
		return s, err
	}
	if Terminal(s, m) {
		return s, fmt.Errorf("trace ends in terminal state %s, not a deadlock", s)
	}
	if n := len(EnabledSteps(s, m)); n > 0 {
		return s, fmt.Errorf("trace ends in %s with %d enabled steps, not a deadlock", s, n)
	}
	return s, nil
}
