package spp

import "testing"

func TestScaleValleyFreeTreeUnique(t *testing.T) {
	// 8-AS customer/provider tree rooted at origin 0.
	links := []Link{
		{A: 1, B: 0, Relation: RelationCustomerToProvider},
		{A: 2, B: 0, Relation: RelationCustomerToProvider},
		{A: 3, B: 1, Relation: RelationCustomerToProvider},
		{A: 4, B: 1, Relation: RelationCustomerToProvider},
		{A: 5, B: 2, Relation: RelationCustomerToProvider},
		{A: 6, B: 3, Relation: RelationCustomerToProvider},
		{A: 7, B: 5, Relation: RelationCustomerToProvider},
	}
	g, err := NewASGraph([]int{0, 1, 2, 3, 4, 5, 6, 7}, links)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	inst, err := Build(g, PolicyConfig{Origin: 0, StrictGR: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !inst.Report.IsClean() {
		t.Fatalf("expected clean validation: %+v", inst.Report.Violations)
	}
	res, err := inst.Solve(SolveOptions{})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Truncated {
		t.Fatal("search budget exhausted on a small tree")
	}
	if res.Outcome != OutcomeUnique {
		t.Fatalf("expected unique SPA, got %s", res.Outcome)
	}
	if len(inst.DetectWheels(8)) != 0 {
		t.Fatal("a strict valley-free tree must contain no dispute wheel")
	}
	// Every non-origin AS must be routed in the unique fixed point.
	for u, p := range res.Solutions[0] {
		if u != 0 && len(p) == 0 {
			t.Fatalf("AS %d unexpectedly unrouted", u)
		}
	}
}
