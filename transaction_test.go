package systemd

import (
	"errors"
	"reflect"
	"testing"
)

func requireJob(t *testing.T, plan *Plan, unit string, jobType JobType) {
	t.Helper()
	job, ok := plan.Job(unit)
	if !ok {
		t.Fatalf("expected job for %s, got %#v", unit, plan.Jobs)
	}
	if job.Type != jobType {
		t.Fatalf("job for %s: got %s, want %s", unit, job.Type, jobType)
	}
}

func requireNoJob(t *testing.T, plan *Plan, unit string) {
	t.Helper()
	if plan.HasJob(unit) {
		t.Fatalf("did not expect a job for %s: %#v", unit, plan.Jobs)
	}
}

func requireOrder(t *testing.T, order []string, before, after string) {
	t.Helper()
	beforeIndex, afterIndex := -1, -1
	for i, name := range order {
		if name == before {
			beforeIndex = i
		}
		if name == after {
			afterIndex = i
		}
	}
	if beforeIndex < 0 || afterIndex < 0 || beforeIndex >= afterIndex {
		t.Fatalf("expected %s before %s in %v", before, after, order)
	}
}

func TestRequirementAndOrderingAreOrthogonal(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "app.service", Requires: []string{"db.service"}},
		Unit{Name: "db.service"},
	)

	plan, err := graph.Start("app.service", ModeFail)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "app.service", JobStart)
	requireJob(t, plan, "db.service", JobStart)
	if len(plan.Edges) != 0 {
		t.Fatalf("Requires= must not create ordering edges: %#v", plan.Edges)
	}

	graph.Units["app.service"].After = []string{"db.service"}
	plan, err = graph.Start("app.service", ModeFail)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasEdge("db.service", "app.service") {
		t.Fatalf("expected db -> app start edge, got %#v", plan.Edges)
	}
	requireOrder(t, plan.StartOrder, "db.service", "app.service")
}

func TestRequisiteNeverStartsInactiveTarget(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "app.service", Requisite: []string{"db.service"}},
		Unit{Name: "db.service"},
	)

	plan, err := graph.Start("app.service", ModeFail)
	if plan != nil {
		t.Fatalf("expected no plan on Requisite failure: %#v", plan)
	}
	if !errors.Is(err, ErrRequisiteInactive) {
		t.Fatalf("got %v, want ErrRequisiteInactive", err)
	}

	graph.Units["db.service"].Active = true
	plan, err = graph.Start("app.service", ModeFail)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "app.service", JobStart)
	requireNoJob(t, plan, "db.service")
}

func TestConflictsModeAndAnchorProtection(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "root.service", Requires: []string{"a.service"}, Wants: []string{"b.service"}},
		Unit{Name: "a.service", Conflicts: []string{"b.service"}},
		Unit{Name: "b.service"},
	)

	if _, err := graph.Start("root.service", ModeFail); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("fail mode: got %v, want transaction conflict", err)
	}

	plan, err := graph.Start("root.service", ModeReplace)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "root.service", JobStart)
	requireJob(t, plan, "a.service", JobStart)
	requireNoJob(t, plan, "b.service")

	anchorGraph := NewGraph(
		Unit{Name: "a.service", Conflicts: []string{"b.service"}},
		Unit{Name: "b.service"},
	)
	if _, err := anchorGraph.SolveAll(
		Start("a.service", ModeReplace),
		Start("b.service", ModeReplace),
	); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("two anchors must still conflict, got %v", err)
	}

	coreGraph := NewGraph(
		Unit{Name: "root.service", Requires: []string{"a.service", "b.service"}},
		Unit{Name: "a.service", Conflicts: []string{"b.service"}},
		Unit{Name: "b.service"},
	)
	if _, err := coreGraph.Start("root.service", ModeReplace); !errors.Is(err, ErrTransactionConflict) {
		t.Fatalf("two core requirements must not be trimmed, got %v", err)
	}
}

func TestConflictStopsActiveUnit(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "a.service", Conflicts: []string{"b.service"}},
		Unit{Name: "b.service", Active: true},
	)

	plan, err := graph.Start("a.service", ModeFail)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "a.service", JobStart)
	requireJob(t, plan, "b.service", JobStop)
}

func TestIsolateHonorsDependencyClosureAndIgnoreOnIsolate(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "target.target", Requires: []string{"dep.service"}},
		Unit{Name: "dep.service", Wants: []string{"helper.service"}},
		Unit{Name: "helper.service"},
		Unit{Name: "extra.service", Active: true},
		Unit{Name: "ignored.service", Active: true, IgnoreOnIsolate: true},
	)

	plan, err := graph.Isolate("target.target", ModeReplace)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "target.target", JobStart)
	requireJob(t, plan, "dep.service", JobStart)
	requireJob(t, plan, "helper.service", JobStart)
	requireJob(t, plan, "extra.service", JobStop)
	requireNoJob(t, plan, "ignored.service")
	if !reflect.DeepEqual(plan.StopOrder, []string{"extra.service"}) {
		t.Fatalf("unexpected isolate stop order: %v", plan.StopOrder)
	}
}

func TestStopOrderingIsReversed(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "a.service", Active: true, After: []string{"b.service"}},
		Unit{Name: "b.service", Active: true},
	)

	startPlan, err := graph.SolveAll(
		Start("a.service", ModeFail),
		Start("b.service", ModeFail),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !startPlan.HasEdge("b.service", "a.service") {
		t.Fatalf("start edge should be b -> a: %#v", startPlan.Edges)
	}
	requireOrder(t, startPlan.StartOrder, "b.service", "a.service")

	stopPlan, err := graph.SolveAll(
		Stop("a.service", ModeFail),
		Stop("b.service", ModeFail),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !stopPlan.HasEdge("a.service", "b.service") {
		t.Fatalf("stop edge should be a -> b: %#v", stopPlan.Edges)
	}
	requireOrder(t, stopPlan.StopOrder, "a.service", "b.service")
}

func TestDeterministicMultiCycleBreaking(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "a.service", After: []string{"b.service"}},
		Unit{Name: "b.service", After: []string{"c.service"}},
		Unit{Name: "c.service", After: []string{"a.service"}},
		Unit{Name: "d.service", After: []string{"e.service"}},
		Unit{Name: "e.service", After: []string{"d.service"}},
	)

	requests := []Request{
		Start("a.service", ModeFail),
		Start("b.service", ModeFail),
		Start("c.service", ModeFail),
		Start("d.service", ModeFail),
		Start("e.service", ModeFail),
	}
	first, err := graph.SolveAll(requests...)
	if err != nil {
		t.Fatal(err)
	}
	second, err := graph.SolveAll(requests...)
	if err != nil {
		t.Fatal(err)
	}

	if len(first.Jobs) != 5 || len(first.Order) != 5 {
		t.Fatalf("cycle breaking must retain every job: jobs=%v order=%v", first.Jobs, first.Order)
	}
	if len(first.Cycles) != 2 {
		t.Fatalf("expected two broken cycles, got %#v", first.Cycles)
	}
	if len(first.CutEdges) != 2 {
		t.Fatalf("expected two cut edges, got %#v", first.CutEdges)
	}
	if len(first.Edges) != 3 {
		t.Fatalf("expected three retained edges, got %#v", first.Edges)
	}
	if !reflect.DeepEqual(first.Cycles, second.Cycles) || !reflect.DeepEqual(first.CutEdges, second.CutEdges) {
		t.Fatalf("cycle report is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func TestStopCycleIsBrokenOnReverseGraph(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "a.service", Active: true, After: []string{"b.service"}},
		Unit{Name: "b.service", Active: true, After: []string{"c.service"}},
		Unit{Name: "c.service", Active: true, After: []string{"a.service"}},
	)
	plan, err := graph.SolveAll(
		Stop("a.service", ModeFail),
		Stop("b.service", ModeFail),
		Stop("c.service", ModeFail),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.StopOrder) != 3 || len(plan.Cycles) != 1 || len(plan.CutEdges) != 1 {
		t.Fatalf("unexpected reverse stop cycle report: %#v", plan)
	}
}

func TestJobMergeRules(t *testing.T) {
	graph := NewGraph(Unit{Name: "a.service", Active: true})

	plan, err := graph.SolveAll(
		Start("a.service", ModeReplace),
		Start("a.service", ModeReplace),
	)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "a.service", JobStart)
	if len(plan.Jobs) != 1 {
		t.Fatalf("start + start must merge: %#v", plan.Jobs)
	}

	plan, err = graph.SolveAll(
		Restart("a.service", ModeReplace),
		Stop("a.service", ModeReplace),
	)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "a.service", JobStop)
	if len(plan.Jobs) != 1 {
		t.Fatalf("restart + stop must merge to stop: %#v", plan.Jobs)
	}

	plan, err = graph.Start("a.service", ModeReplaceIrreversibly)
	if err != nil {
		t.Fatal(err)
	}
	job, _ := plan.Job("a.service")
	if !job.Irreversible {
		t.Fatalf("replace-irreversibly must mark retained jobs irreversible: %#v", job)
	}
}

func TestMissingHardRequirementFailsTransaction(t *testing.T) {
	graph := NewGraph(Unit{Name: "a.service", Requires: []string{"missing.service"}})
	if _, err := graph.Start("a.service", ModeFail); !errors.Is(err, ErrMissingUnit) {
		t.Fatalf("got %v, want ErrMissingUnit", err)
	}
}

func TestFailedHardRequirementCancelsDependent(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "root.service", Wants: []string{"a.service"}},
		Unit{Name: "a.service", Requires: []string{"b.service"}},
		Unit{Name: "b.service", Active: true},
	)

	plan, err := graph.SolveAll(
		Start("root.service", ModeReplace),
		Stop("b.service", ModeReplace),
	)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "root.service", JobStart)
	requireJob(t, plan, "b.service", JobStop)
	requireNoJob(t, plan, "a.service")
}

func TestStopPropagationThroughRequiresAndPartOf(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "base.service", Active: true},
		Unit{Name: "requires.service", Active: true, Requires: []string{"base.service"}},
		Unit{Name: "part.service", Active: true, PartOf: []string{"base.service"}},
		Unit{Name: "wants.service", Active: true, Wants: []string{"base.service"}},
	)

	plan, err := graph.Stop("base.service", ModeFail)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "base.service", JobStop)
	requireJob(t, plan, "requires.service", JobStop)
	requireJob(t, plan, "part.service", JobStop)
	requireNoJob(t, plan, "wants.service")
}

func TestPartOfRestartPropagation(t *testing.T) {
	graph := NewGraph(
		Unit{Name: "base.service", Active: true},
		Unit{Name: "part.service", Active: true, PartOf: []string{"base.service"}},
	)

	plan, err := graph.Restart("base.service", ModeFail)
	if err != nil {
		t.Fatal(err)
	}
	requireJob(t, plan, "base.service", JobRestart)
	requireJob(t, plan, "part.service", JobRestart)
	if len(plan.Steps) != 4 {
		t.Fatalf("two restarts should produce four execution steps: %#v", plan.Steps)
	}
}
