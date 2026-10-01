package main

import "testing"

func simResolve(t *testing.T, sc *Scenario) []resolvedFlow {
	t.Helper()
	if err := sc.validate(); err != nil {
		t.Fatalf("invalid scenario: %v", err)
	}
	return resolveFlows(sc, buildTopology(sc, nil))
}

func TestSimUnderCapacityDeliversAll(t *testing.T) {
	sc := &Scenario{
		Links: []Link{{ID: "L", From: "A", To: "B", Capacity: 2, Mode: ModeUnidirectional}},
		Flows: []Flow{{ID: "F", Src: "A", Dst: "B", Demand: 1, Route: []string{"A", "B"}}},
	}
	resolved := simResolve(t, sc)
	res := simulate(resolved, 100)
	if res.injected[0] != 100 {
		t.Fatalf("injected = %d, want 100", res.injected[0])
	}
	if res.delivered[0] != res.injected[0] {
		t.Fatalf("delivered = %d, injected = %d; backlog should drain each tick",
			res.delivered[0], res.injected[0])
	}
	if res.links["L|fwd"].maxBacklog != 0 {
		t.Fatalf("maxBacklog = %d, want 0", res.links["L|fwd"].maxBacklog)
	}
}

func TestSimOverloadBacklogGrows(t *testing.T) {
	sc := &Scenario{
		Links: []Link{{ID: "L", From: "A", To: "B", Capacity: 2, Mode: ModeUnidirectional}},
		Flows: []Flow{{ID: "F", Src: "A", Dst: "B", Demand: 3, Route: []string{"A", "B"}}},
	}
	resolved := simResolve(t, sc)
	res := simulate(resolved, 100)
	sl := res.links["L|fwd"]
	if res.injected[0] != 300 {
		t.Fatalf("injected = %d, want 300", res.injected[0])
	}
	if sl.forwarded != 200 {
		t.Fatalf("forwarded = %d, want 200 (cap 2 x 100 ticks)", sl.forwarded)
	}
	if sl.backlog != 100 {
		t.Fatalf("end backlog = %d, want 100", sl.backlog)
	}
	if sl.maxBacklog != 100 {
		t.Fatalf("maxBacklog = %d, want 100", sl.maxBacklog)
	}
}

func TestSimFractionalRate(t *testing.T) {
	sc := &Scenario{
		Links: []Link{{ID: "L", From: "A", To: "B", Capacity: 10, Mode: ModeUnidirectional}},
		Flows: []Flow{{ID: "F", Src: "A", Dst: "B", Demand: 0.5, Route: []string{"A", "B"}}},
	}
	resolved := simResolve(t, sc)
	res := simulate(resolved, 10)
	if res.injected[0] != 5 {
		t.Fatalf("injected = %d, want 5 (0.5 pkt/tick x 10 ticks)", res.injected[0])
	}
	if res.delivered[0] != 5 {
		t.Fatalf("delivered = %d, want 5", res.delivered[0])
	}
}

func TestSimSharedLinkSumsFlows(t *testing.T) {
	// Two flows in opposite directions over one shared link: combined
	// demand 3 > capacity 2, so backlog must grow.
	sc := &Scenario{
		Links: []Link{{ID: "L", From: "A", To: "B", Capacity: 2, Mode: ModeShared}},
		Flows: []Flow{
			{ID: "F1", Src: "A", Dst: "B", Demand: 1.5, Route: []string{"A", "B"}},
			{ID: "F2", Src: "B", Dst: "A", Demand: 1.5, Route: []string{"B", "A"}},
		},
	}
	resolved := simResolve(t, sc)
	res := simulate(resolved, 100)
	sl := res.links["L|shared"]
	if sl.backlog <= 0 {
		t.Fatalf("shared link with combined demand 3 > cap 2 must build backlog, got %d", sl.backlog)
	}
	if sl.forwarded != 200 {
		t.Fatalf("forwarded = %d, want 200", sl.forwarded)
	}
}

func TestSimMultiHopAndUnreachable(t *testing.T) {
	sc := &Scenario{
		Links: []Link{
			{ID: "L1", From: "A", To: "B", Capacity: 5, Mode: ModeUnidirectional},
			{ID: "L2", From: "B", To: "C", Capacity: 5, Mode: ModeUnidirectional},
		},
		Flows: []Flow{
			{ID: "OK", Src: "A", Dst: "C", Demand: 2, Route: []string{"A", "B", "C"}},
			{ID: "BAD", Src: "C", Dst: "A", Demand: 1, Route: []string{"C", "A"}},
		},
	}
	resolved := simResolve(t, sc)
	res := simulate(resolved, 50)
	if res.injected[1] != 0 {
		t.Fatalf("unreachable flow must inject 0 packets, got %d", res.injected[1])
	}
	if res.delivered[0] != 100 {
		t.Fatalf("delivered = %d, want 100 (2 pkt/tick x 50 ticks)", res.delivered[0])
	}
}
