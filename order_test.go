package handoff

import (
	"testing"
)

func TestPerEgressBoundedReorderWindow(t *testing.T) {
	spec := testSpec("e1", "e2")
	spec.Window = 1
	p, err := NewProtocol(spec)
	if err != nil {
		t.Fatal(err)
	}
	st1 := Statement{PlanID: spec.PlanID, ID: "one", Generation: 1}.Canonical()
	st2 := Statement{PlanID: spec.PlanID, ID: "two", Generation: 2}.Canonical()
	p.AddStatementBroadcast(st1)
	p.AddStatementBroadcast(st2)

	traces, err := p.EnumerateTraces(100000)
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) == 0 {
		t.Fatal("no traces")
	}
	for _, trace := range traces {
		positions := make(map[EventID]int)
		for i, id := range trace {
			positions[id] = i
		}
		for _, e := range spec.Egresses {
			d1 := EventID("deliver:one:" + string(e))
			d2 := EventID("deliver:two:" + string(e))
			if positions[d2] < positions[d1] {
				t.Fatalf("window=1 allowed %s before %s in %v", d2, d1, trace)
			}
		}
	}

	spec.Window = 2
	p2, err := NewProtocol(spec)
	if err != nil {
		t.Fatal(err)
	}
	p2.AddStatementBroadcast(st1)
	p2.AddStatementBroadcast(st2)
	traces2, err := p2.EnumerateTraces(100000)
	if err != nil {
		t.Fatal(err)
	}
	foundOvertake := false
	for _, trace := range traces2 {
		positions := make(map[EventID]int)
		for i, id := range trace {
			positions[id] = i
		}
		for _, e := range spec.Egresses {
			d1 := EventID("deliver:one:" + string(e))
			d2 := EventID("deliver:two:" + string(e))
			if positions[d2] < positions[d1] {
				foundOvertake = true
			}
		}
	}
	if !foundOvertake {
		t.Fatal("window=2 did not permit one overtake")
	}
}

func TestPartialOrderForcesBarrier(t *testing.T) {
	spec := testSpec("e1")
	plan, err := BuildSafePlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	traces, err := plan.Protocol.EnumerateTraces(1000)
	if err != nil {
		t.Fatal(err)
	}
	ack := EventID("ack:" + plan.PrepositionStatement.ID + ":e1")
	for _, trace := range traces {
		ackPos, cutPos := -1, -1
		for i, id := range trace {
			if id == ack {
				ackPos = i
			}
			if id == plan.CutEvent {
				cutPos = i
			}
		}
		if ackPos < 0 || cutPos < 0 || ackPos > cutPos {
			t.Fatalf("partial order violated: ack=%d cut=%d trace=%v", ackPos, cutPos, trace)
		}
	}
}
