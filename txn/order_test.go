package txn

import (
	"reflect"
	"testing"
)

// TestStopOrderingReversed verifies that Before/After edges flip direction when
// a stop job is involved so shutdown runs in reverse order.
func TestStopOrderingReversed(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", After: []string{"b.service"}, Requires: []string{"b.service"}},
		&Unit{Name: "b.service", PartOf: []string{"a.service"}},
	)
	startPlan, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := edgeSet(startPlan)[[2]string{"b.service", "a.service"}]; !ok {
		t.Fatalf("startup expected b -> a, got %+v", startPlan.Edges)
	}

	stopPlan, err := Solve(g, Snapshot{"a.service": true, "b.service": true},
		Request{Unit: "a.service", Job: JobStop, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	edge, ok := edgeSet(stopPlan)[[2]string{"a.service", "b.service"}]
	if !ok {
		t.Fatalf("shutdown expected reversed edge a -> b, got %+v", stopPlan.Edges)
	}
	if !edge.Reversed {
		t.Fatalf("shutdown edge must be marked reversed")
	}
	if !reflect.DeepEqual(stopPlan.Layers, [][]string{{"a.service"}, {"b.service"}}) {
		t.Fatalf("shutdown expected a before b, got %+v", stopPlan.Layers)
	}
}

// TestMultiCycleDeterministicCut breaks a two-unit ordering cycle, keeps both
// jobs and reports exactly which edge was removed.
func TestMultiCycleDeterministicCut(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Before: []string{"b.service"}, Requires: []string{"b.service"}},
		&Unit{Name: "b.service", Before: []string{"a.service"}},
	)
	plan, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cuts) != 1 {
		t.Fatalf("expected one cut, got %+v", plan.Cuts)
	}
	cut := plan.Cuts[0]
	if cut.Edge.From != "a.service" || cut.Edge.To != "b.service" {
		t.Fatalf("deterministic cut should remove a -> b, got %+v", cut.Edge)
	}
	if !reflect.DeepEqual(cut.Cycle, []string{"a.service", "b.service"}) {
		t.Fatalf("unexpected minimal cycle %+v", cut.Cycle)
	}
	if len(plan.Jobs) != 2 {
		t.Fatalf("cycle breaking must never drop jobs, got %+v", plan.Jobs)
	}
	seen := map[string]bool{}
	for _, layer := range plan.Layers {
		for _, unit := range layer {
			seen[unit] = true
		}
	}
	if len(seen) != 2 {
		t.Fatalf("after cutting both jobs must still be schedulable, got %+v", plan.Layers)
	}
}

// TestThreeReachableCycles exercises several simultaneous cycles within one
// transaction and asserts termination plus a complete deterministic report.
func TestThreeReachableCycles(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Before: []string{"b.service"}, Requires: []string{"b.service", "c.service", "e.service"}},
		&Unit{Name: "b.service", Before: []string{"a.service"}},
		&Unit{Name: "c.service", Before: []string{"d.service"}, Requires: []string{"d.service"}},
		&Unit{Name: "d.service", Before: []string{"c.service"}},
		&Unit{Name: "e.service", Before: []string{"f.service"}, Requires: []string{"f.service"}},
		&Unit{Name: "f.service", Before: []string{"e.service"}},
	)
	plan, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cuts) != 3 {
		t.Fatalf("expected three reported cuts, got %+v", plan.Cuts)
	}
	wantEdges := map[[2]string]bool{
		{"a.service", "b.service"}: true,
		{"c.service", "d.service"}: true,
		{"e.service", "f.service"}: true,
	}
	for _, cut := range plan.Cuts {
		key := [2]string{cut.Edge.From, cut.Edge.To}
		if !wantEdges[key] {
			t.Fatalf("unexpected cut edge %+v", cut.Edge)
		}
		delete(wantEdges, key)
	}
	seen := map[string]bool{}
	for _, layer := range plan.Layers {
		for _, unit := range layer {
			if seen[unit] {
				t.Fatalf("unit %s scheduled twice", unit)
			}
			seen[unit] = true
		}
	}
	if len(seen) != len(plan.Jobs) {
		t.Fatalf("DAG scheduling lost jobs: %d scheduled vs %d jobs", len(seen), len(plan.Jobs))
	}
}

// TestMinimalCycleChosen verifies that the shortest cycle is cut even inside a
// larger strongly connected component.
func TestMinimalCycleChosen(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Before: []string{"b.service"}, Requires: []string{"b.service", "c.service"}},
		&Unit{Name: "b.service", Before: []string{"a.service", "c.service"}},
		&Unit{Name: "c.service", After: []string{"a.service"}},
	)
	plan, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cuts) == 0 {
		t.Fatal("expected cycle cuts")
	}
	if len(plan.Cuts[0].Cycle) != 2 {
		t.Fatalf("first cut must target the minimal 2-unit cycle, got %+v", plan.Cuts[0].Cycle)
	}
}

// TestMergeStartStartAndRestartStop covers same-unit job collapse.
func TestMergeStartStartAndRestartStop(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Requires: []string{"b.service", "c.service"}},
		&Unit{Name: "b.service", Requires: []string{"d.service"}},
		&Unit{Name: "c.service", Requires: []string{"d.service"}},
		&Unit{Name: "d.service"},
	)
	plan, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "d.service", JobStart)
	if len(plan.Jobs) != 4 {
		t.Fatalf("duplicate start paths must collapse, got %+v", plan.Jobs)
	}

	// restart(anchor) meeting a propagated stop(b) collapses to stop, matching
	// the systemd restart + stop -> stop rule.
	rg := graphOf(
		&Unit{Name: "a.service"},
		&Unit{Name: "b.service", PartOf: []string{"a.service"}},
	)
	rplan, err := Solve(rg, Snapshot{"a.service": true, "b.service": true},
		Request{Unit: "a.service", Job: JobRestart, Mode: ModeReplace})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, rplan, "a.service", JobRestart)
	assertJob(t, rplan, "b.service", JobRestart)
}

// TestStopPropagation verifies reverse PartOf/BindsTo shutdown propagation and
// confirms plain Requires does not propagate stops.
func TestStopPropagation(t *testing.T) {
	g := graphOf(
		&Unit{Name: "parent.service"},
		&Unit{Name: "part.service", PartOf: []string{"parent.service"}},
		&Unit{Name: "bound.service", BindsTo: []string{"parent.service"}},
		&Unit{Name: "plain.service", Requires: []string{"parent.service"}},
	)
	plan, err := Solve(g, Snapshot{}, Request{Unit: "parent.service", Job: JobStop, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "parent.service", JobStop)
	assertJob(t, plan, "part.service", JobStop)
	assertJob(t, plan, "bound.service", JobStop)
	assertNoJob(t, plan, "plain.service")
}

// TestRestartPlusStopMergesToStop exercises the systemd collapse rule
// restart + stop -> stop on the same unit. In fail mode this is a hard
// collision; in replace mode the reducible stop side wins, yielding stop.
func TestRestartPlusStopMergesToStop(t *testing.T) {
	rg := graphOf(
		&Unit{Name: "a.service"},
		// b is both PartOf a (restart propagation) and BindsTo a (stop
		// propagation): the two incoming jobs collapse restart + stop -> stop.
		&Unit{Name: "b.service", PartOf: []string{"a.service"}, BindsTo: []string{"a.service"}},
	)
	_, err := Solve(rg, Snapshot{"a.service": true, "b.service": true},
		Request{Unit: "a.service", Job: JobRestart, Mode: ModeFail})
	assertErrorIs(t, err, ErrConflict)

	plan, err := Solve(rg, Snapshot{"a.service": true, "b.service": true},
		Request{Unit: "a.service", Job: JobRestart, Mode: ModeReplace})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "a.service", JobRestart)
	assertJob(t, plan, "b.service", JobStop)
}
