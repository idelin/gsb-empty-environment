package sdf

import "testing"

func TestPinnedCapacitiesRespected(t *testing.T) {
	p := pipelineProblem()
	feed(p, 4, 1, 4, 1)
	// Pin every edge; the solver must only evaluate the pinned vector.
	pinned := []int{3, 2, 2, 2}
	for eid := range p.Edges {
		p.Edges[eid].Cap = pinned[eid]
	}
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFeasible {
		t.Fatalf("status=%s reason=%s", r.Status, r.Reason)
	}
	for eid := range pinned {
		if r.Cap[eid] != pinned[eid] {
			t.Fatalf("edge %d cap=%d want pinned %d", eid, r.Cap[eid], pinned[eid])
		}
	}
	if r.Cert.Enumeration != 1 {
		t.Fatalf("enumeration=%d want 1 for a fully pinned vector", r.Cert.Enumeration)
	}
	if err := r.Schedule.Verify(p, 3); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestPinnedBelowLowerBoundRejected(t *testing.T) {
	p := pipelineProblem()
	feed(p, 4, 1, 4, 1)
	p.Edges[0].Cap = 1 // input edge lower bound is Cons+1 = 2
	r, err := Solve(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status == StatusFeasible {
		t.Fatalf("infeasible pin reported feasible: %v", r.Cap)
	}
}
