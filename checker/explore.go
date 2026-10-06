package checker

// System pairs the two machines.
type System struct {
	A *Machine
	B *Machine
}

// Initial returns the start configuration with empty queues.
func (sys *System) Initial() State {
	return State{A: sys.A.Initial, B: sys.B.Initial}
}

// enabled lists the transitions of machine who that can fire in s.
// who==0: machine A, in-queue QBA, out-queue QAB.
// who==1: machine B, in-queue QAB, out-queue QBA.
func (sys *System) enabled(s State, who int) []Transition {
	var m *Machine
	var cur string
	var in, out []string
	if who == 0 {
		m, cur, in, out = sys.A, s.A, s.QBA, s.QAB
	} else {
		m, cur, in, out = sys.B, s.B, s.QAB, s.QBA
	}
	var en []Transition
	for _, t := range m.Outgoing(cur) {
		switch t.Act.Kind {
		case Tau:
			en = append(en, t)
		case Send:
			if len(out) < QueueCap {
				en = append(en, t)
			}
		case Recv:
			if len(in) > 0 && in[0] == t.Act.Message {
				en = append(en, t)
			}
		}
	}
	return en
}

// apply fires transition t of machine who in s, returning the next state.
// Callers must ensure t is enabled (Replay checks this).
func apply(s State, who int, t Transition) State {
	next := State{
		A:   s.A,
		B:   s.B,
		QAB: append([]string(nil), s.QAB...),
		QBA: append([]string(nil), s.QBA...),
	}
	if who == 0 {
		next.A = t.To
	} else {
		next.B = t.To
	}
	switch t.Act.Kind {
	case Send:
		if who == 0 {
			next.QAB = append(next.QAB, t.Act.Message)
		} else {
			next.QBA = append(next.QBA, t.Act.Message)
		}
	case Recv:
		if who == 0 {
			next.QBA = next.QBA[1:]
		} else {
			next.QAB = next.QAB[1:]
		}
	}
	return next
}

// Deadlock reports whether s is a deadlock: NOT a terminated configuration
// (at least one machine could in principle still run) yet no transition of
// either machine is enabled.
func (sys *System) Deadlock(s State) bool {
	if s.Terminal(sys.A, sys.B) {
		return false
	}
	return len(sys.enabled(s, 0)) == 0 && len(sys.enabled(s, 1)) == 0
}

// Result summarizes an exhaustive exploration.
type Result struct {
	Explored  int    // number of distinct global states visited
	Deadlock  bool   // whether a deadlock state was found
	Trace     []Step // shortest trace from Initial to the deadlock state
	DeadState State  // the deadlock state (valid iff Deadlock)
}

// Explore performs BFS over the finite product state space and returns the
// shortest trace to a deadlock state, if any. Because BFS expands states in
// order of depth, the first deadlock found has a shortest trace. No fairness
// assumption is made: every enabled interleaving is a branch.
func (sys *System) Explore() Result {
	init := sys.Initial()
	type node struct {
		s    State
		prev int // index into nodes, -1 for root
		step Step
	}
	nodes := []node{{s: init, prev: -1}}
	seen := map[string]bool{init.Key(): true}
	queue := []int{0}

	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		s := nodes[i].s

		if sys.Deadlock(s) {
			// Reconstruct trace from root to i.
			var trace []Step
			for j := i; nodes[j].prev >= 0; j = nodes[j].prev {
				trace = append(trace, nodes[j].step)
			}
			for l, r := 0, len(trace)-1; l < r; l, r = l+1, r-1 {
				trace[l], trace[r] = trace[r], trace[l]
			}
			return Result{Explored: len(seen), Deadlock: true, Trace: trace, DeadState: s}
		}

		for who := 0; who < 2; who++ {
			for _, t := range sys.enabled(s, who) {
				next := apply(s, who, t)
				if seen[next.Key()] {
					continue
				}
				seen[next.Key()] = true
				step := Step{Who: who, Transition: t, After: next}
				nodes = append(nodes, node{s: next, prev: i, step: step})
				queue = append(queue, len(nodes)-1)
			}
		}
	}
	return Result{Explored: len(seen)}
}
