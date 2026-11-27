package spp

import "testing"

func TestLoadBadGadgetSpec(t *testing.T) {
	spec, err := LoadSpec("../testdata/bad_gadget.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	inst, err := BuildFromSpec(spec)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	res, err := inst.Solve(SolveOptions{})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Outcome != OutcomeNoStable {
		t.Fatalf("expected no stable assignment from JSON spec, got %s", res.Outcome)
	}
	if len(inst.DetectWheels(4)) == 0 {
		t.Fatal("JSON gadget must expose a dispute wheel")
	}
}

func TestLoadValleyFreeSpec(t *testing.T) {
	spec, err := LoadSpec("../testdata/valley_free.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	inst, err := BuildFromSpec(spec)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !inst.Report.IsClean() {
		t.Fatalf("expected clean validation: %+v", inst.Report.Violations)
	}
	// Explicitly verify the Gao-Rexford export rule in isolation.
	if ev := SimulateExportAudit(inst.Graph, inst.Ranked); len(ev) != 0 {
		t.Fatalf("expected zero illegal exports, got %+v", ev)
	}
	res, err := inst.Solve(SolveOptions{})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Outcome != OutcomeUnique {
		t.Fatalf("expected unique stable, got %s", res.Outcome)
	}
}

func TestIllegalExportDetected(t *testing.T) {
	// AS 2 learns a route from its provider 1 and exports it laterally to its
	// peer 3: that peer re-export is a Gao-Rexford violation.
	g, err := NewASGraph([]int{1, 2, 3, 4}, []Link{
		{A: 1, B: 4, Relation: RelationCustomerToProvider},
		{A: 2, B: 1, Relation: RelationCustomerToProvider}, // 2 customer of 1
		{A: 2, B: 3, Relation: RelationPeer},
	})
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	inst, err := Build(g, PolicyConfig{Origin: 4, StrictGR: false})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ev := SimulateExportAudit(g, inst.Ranked)
	if len(ev) == 0 {
		t.Fatal("expected an illegal peer/provider re-export violation")
	}
	found := false
	for _, v := range ev {
		if v.Kind == "illegal-export" && v.AS == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected AS 2 illegal-export, got %+v", ev)
	}
}
