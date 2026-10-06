package livecheck

import "fmt"

// Verify independently replays a counterexample against the graph and
// confirms that it genuinely witnesses a violation. It trusts nothing
// from the search that produced ce: every step is re-checked against
// the graph's edges and the obligation set is recomputed from scratch.
func Verify(g *Graph, ce *Counterexample) error {
	if ce == nil {
		return fmt.Errorf("nil counterexample")
	}
	node := g.Initial()
	pend := make(map[string]bool)

	step := func(i int, s Step) error {
		if s.From != node {
			return fmt.Errorf("step %d: run is at %q but step leaves %q", i, node, s.From)
		}
		if !hasEdge(g, s.From, s.To, s.Action) {
			return fmt.Errorf("step %d: no edge %s", i, s)
		}
		switch s.Action.Kind {
		case Request:
			pend[s.Action.ID] = true
		case Response:
			delete(pend, s.Action.ID)
		}
		node = s.To
		return nil
	}

	for i, s := range ce.Prefix {
		if err := step(i, s); err != nil {
			return fmt.Errorf("prefix: %v", err)
		}
	}

	if ce.Terminal {
		if len(ce.Cycle) != 0 {
			return fmt.Errorf("terminal counterexample must not carry a cycle")
		}
		if len(g.Edges(node)) != 0 {
			return fmt.Errorf("claimed terminal state %q has outgoing edges", node)
		}
		if !pend[ce.PendingID] {
			return fmt.Errorf("id %q is not owed a response at terminal state %q", ce.PendingID, node)
		}
		return nil
	}

	if len(ce.Cycle) == 0 {
		return fmt.Errorf("non-terminal counterexample must carry a cycle")
	}
	if !pend[ce.PendingID] {
		return fmt.Errorf("id %q is not owed a response where the cycle begins", ce.PendingID)
	}
	startNode := node
	startPend := make(map[string]bool, len(pend))
	for id := range pend {
		startPend[id] = true
	}
	for i, s := range ce.Cycle {
		if err := step(i, s); err != nil {
			return fmt.Errorf("cycle: %v", err)
		}
		if s.Action.Kind == Response && s.Action.ID == ce.PendingID {
			return fmt.Errorf("cycle: step %d answers the supposedly perpetually owed id %q", i, ce.PendingID)
		}
	}
	if node != startNode {
		return fmt.Errorf("cycle ends at %q, not at its start state %q", node, startNode)
	}
	if len(pend) != len(startPend) {
		return fmt.Errorf("cycle does not reproduce the pending-id set")
	}
	for id := range pend {
		if !startPend[id] {
			return fmt.Errorf("cycle does not reproduce the pending-id set")
		}
	}
	return nil
}

func hasEdge(g *Graph, from, to string, a Action) bool {
	for _, e := range g.Edges(from) {
		if e.To == to && e.Action == a {
			return true
		}
	}
	return false
}
