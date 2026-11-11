package netsec

import "fmt"

type checker struct {
	spec   Spec
	policy OverflowPolicy
	budget Budget

	visited        map[string]bool
	violatingTrace []Event
	stats          SearchStats
}

// candidate groups one enabled transition with the queued message that enables
// it. An internal transition carries a nil head.
type candidate struct {
	epName string
	tr     Transition
	head   *queuedMsg
}

func (c *checker) endpoint(name string) *Endpoint {
	for i := range c.spec.Endpoints {
		if c.spec.Endpoints[i].Name == name {
			return &c.spec.Endpoints[i]
		}
	}
	return nil
}

func (c *checker) capacity(channel string) int {
	for _, ch := range c.spec.Channels {
		if ch.Name == channel {
			return ch.Capacity
		}
	}
	return 0
}

// endpointReceiving returns the endpoint whose incoming channel is channel,
// or nil when the spec has no such endpoint.
func (c *checker) endpointReceiving(channel string) *Endpoint {
	for i := range c.spec.Endpoints {
		if c.spec.Endpoints[i].Incoming == channel {
			return &c.spec.Endpoints[i]
		}
	}
	return nil
}

func (c *checker) enabledMoves(gs globalState) []candidate {
	var moves []candidate
	for i := range c.spec.Endpoints {
		ep := &c.spec.Endpoints[i]
		rt := gs.endpoints[ep.Name]
		if rt.terminal {
			continue
		}
		ch := gs.channels[ep.Incoming]
		var head *queuedMsg
		if len(ch.queue) > 0 {
			head = &ch.queue[0]
		}
		for _, tr := range ep.Transitions {
			if tr.From != rt.state {
				continue
			}
			switch {
			case tr.OnRecv == "":
				moves = append(moves, candidate{epName: ep.Name, tr: tr})
			case head != nil && head.Tag == tr.OnRecv:
				moves = append(moves, candidate{epName: ep.Name, tr: tr, head: head})
			}
		}
	}
	return moves
}

// Check exhaustively explores the reachable state space of spec under policy,
// bounded by budget, and reports whether any endpoint can enter a terminal
// success state without receiving every message required by its binding.
// Exhausting the budget yields Unknown, never a premature Safe.
func Check(spec Spec, policy OverflowPolicy, budget Budget) Report {
	if err := spec.validate(); err != nil {
		return Report{Verdict: Unknown, Policy: policy, Reason: "invalid spec: " + err.Error()}
	}
	if budget.MaxStates < 1 || budget.MaxSteps < 1 {
		return Report{Verdict: Unknown, Policy: policy, Reason: "budget limits must be >= 1"}
	}

	c := &checker{
		spec:    spec,
		policy:  policy,
		budget:  budget,
		visited: map[string]bool{},
	}

	init := newInitialState(spec)
	verdict, reason := c.dfs(init, nil)

	rep := Report{
		Verdict: verdict,
		Policy:  policy,
		Reason:  reason,
		Stats:   c.stats,
	}
	if verdict == Unsafe {
		rep.Trace = append([]Event(nil), c.violatingTrace...)
	}
	return rep
}

func (c *checker) dfs(gs globalState, trace []Event) (Verdict, string) {
	key := gs.key()
	if c.visited[key] {
		return Safe, ""
	}
	if c.stats.VisitedStates >= c.budget.MaxStates {
		return Unknown, fmt.Sprintf("state budget exhausted at %d states", c.stats.VisitedStates)
	}
	c.visited[key] = true
	c.stats.VisitedStates++

	if len(trace) > c.stats.MaxDepth {
		c.stats.MaxDepth = len(trace)
	}
	if len(trace) >= c.budget.MaxSteps {
		return Unknown, fmt.Sprintf("step budget exhausted at %d steps", len(trace))
	}

	moves := c.enabledMoves(gs)
	if len(moves) == 0 {
		return Safe, ""
	}

	for _, move := range moves {
		next, events := c.apply(gs, move)
		newTrace := append(append([]Event(nil), trace...), events...)
		c.stats.FiredTransitions++

		for _, ev := range events {
			if ev.Kind == EvSuccess && (len(ev.Missing) > 0 || len(ev.MissingTags) > 0) {
				c.violatingTrace = newTrace
				return Unsafe, fmt.Sprintf(
					"endpoint %q entered success state %q without required messages (missing seqs %v, missing tags %v)",
					ev.Endpoint, ev.State, ev.Missing, ev.MissingTags)
			}
		}

		verdict, reason := c.dfs(next, newTrace)
		if verdict != Safe {
			return verdict, reason
		}
	}
	return Safe, ""
}
