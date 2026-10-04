package planner

import (
	"fmt"
	"strings"
	"testing"
)

func phaseOf(t *testing.T, p *Plan, id string) int {
	t.Helper()
	for _, ph := range p.Phases {
		for _, s := range ph.Steps {
			if s.Step.ID == id {
				return ph.Index
			}
		}
	}
	t.Fatalf("step %q not found in plan", id)
	return 0
}

func TestLifecycleOrderInjected(t *testing.T) {
	// No explicit requires: the planner must still order
	// add_column -> backfill -> add_constraint.
	p, err := Schedule([]Step{
		{ID: "add_constraint", Kind: KindAddConstraint, Table: "users", Column: "email", Rollback: "drop constraint"},
		{ID: "backfill", Kind: KindBackfill, Table: "users", Column: "email", Rollback: "null out"},
		{ID: "add_column", Kind: KindAddColumn, Table: "users", Column: "email", Rollback: "still nullable"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := phaseOf(t, p, "add_column"), phaseOf(t, p, "backfill"), phaseOf(t, p, "add_constraint")
	if !(a < b && b < c) {
		t.Fatalf("misordered lifecycle: add=%d backfill=%d constraint=%d", a, b, c)
	}
}

func TestLifecycleMisorderDetected(t *testing.T) {
	// Explicit requires inverting the chain must be rejected.
	_, err := Schedule([]Step{
		{ID: "add_column", Kind: KindAddColumn, Table: "users", Column: "email", Requires: []string{"backfill"}},
		{ID: "backfill", Kind: KindBackfill, Table: "users", Column: "email"},
	})
	if err == nil || !strings.Contains(err.Error(), "misordered") {
		t.Fatalf("expected misorder error, got %v", err)
	}

	_, err = Schedule([]Step{
		{ID: "backfill", Kind: KindBackfill, Table: "users", Column: "email", Requires: []string{"constraint"}},
		{ID: "constraint", Kind: KindAddConstraint, Table: "users", Column: "email"},
		{ID: "add_column", Kind: KindAddColumn, Table: "users", Column: "email"},
	})
	if err == nil || !strings.Contains(err.Error(), "misordered") {
		t.Fatalf("expected misorder error, got %v", err)
	}
}

func TestBackfillWithoutAddColumnWarns(t *testing.T) {
	p, err := Schedule([]Step{
		{ID: "backfill", Kind: KindBackfill, Table: "users", Column: "email"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) == 0 {
		t.Fatal("expected a warning about the missing add_column step")
	}
}

func TestMaxSteps(t *testing.T) {
	steps := make([]Step, MaxSteps+1)
	for i := range steps {
		steps[i] = Step{ID: fmt.Sprintf("s%02d", i), Kind: KindCustom, Table: fmt.Sprintf("t%d", i)}
	}
	if _, err := Schedule(steps); err == nil {
		t.Fatal("expected error for more than 20 steps")
	}
	if _, err := Schedule(steps[:MaxSteps]); err != nil {
		t.Fatalf("20 steps must be accepted: %v", err)
	}
}

func TestCycleAndUnknownRequires(t *testing.T) {
	_, err := Schedule([]Step{
		{ID: "a", Kind: KindCustom, Requires: []string{"b"}},
		{ID: "b", Kind: KindCustom, Requires: []string{"a"}},
	})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
	_, err = Schedule([]Step{
		{ID: "a", Kind: KindCustom, Requires: []string{"ghost"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown-requires error, got %v", err)
	}
}

func TestConflictMerging(t *testing.T) {
	// t1 and t2 writers are independent: same phase.
	// two writers on t1 conflict: separate phases.
	p, err := Schedule([]Step{
		{ID: "w1", Kind: KindCustom, Writes: []Object{{Table: "t1"}}},
		{ID: "w2", Kind: KindCustom, Writes: []Object{{Table: "t2"}}},
		{ID: "w3", Kind: KindCustom, Writes: []Object{{Table: "t1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if phaseOf(t, p, "w1") != phaseOf(t, p, "w2") {
		t.Fatal("non-conflicting steps should share a phase")
	}
	if phaseOf(t, p, "w1") == phaseOf(t, p, "w3") {
		t.Fatal("conflicting writers must not share a phase")
	}
}

func TestReadWriteConflictSeparated(t *testing.T) {
	p, err := Schedule([]Step{
		{ID: "writer", Kind: KindCustom, Writes: []Object{{Table: "t2"}}},
		{ID: "reader", Kind: KindCustom, Reads: []Object{{Table: "t2"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if phaseOf(t, p, "writer") == phaseOf(t, p, "reader") {
		t.Fatal("read/write conflict on t2 must be separated")
	}
}

func TestWriteStopWindowsMinimized(t *testing.T) {
	// w1 can run any time after s1; w2 is pinned late by a chain.
	// Optimal: both write-stop steps share one window.
	p, err := Schedule([]Step{
		{ID: "s1", Kind: KindCustom, Table: "a"},
		{ID: "s2", Kind: KindCustom, Table: "b", Requires: []string{"s1"}},
		{ID: "s3", Kind: KindCustom, Table: "c", Requires: []string{"s2"}},
		{ID: "w1", Kind: KindCustom, Table: "d", NeedsWriteStop: true, Requires: []string{"s1"}},
		{ID: "w2", Kind: KindCustom, Table: "e", NeedsWriteStop: true, Requires: []string{"s3"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.WriteStopWindows != 1 {
		t.Fatalf("expected 1 write-stop window, got %d", p.WriteStopWindows)
	}
	if phaseOf(t, p, "w1") != phaseOf(t, p, "w2") {
		t.Fatal("write-stop steps should be clustered into one phase")
	}
}

func TestWriteStopWindowsForcedApart(t *testing.T) {
	// w2 depends on w1: two windows are unavoidable.
	p, err := Schedule([]Step{
		{ID: "w1", Kind: KindCustom, Table: "a", NeedsWriteStop: true},
		{ID: "w2", Kind: KindCustom, Table: "b", NeedsWriteStop: true, Requires: []string{"w1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.WriteStopWindows != 2 {
		t.Fatalf("expected 2 write-stop windows, got %d", p.WriteStopWindows)
	}
}

func TestIrreversibleBoundary(t *testing.T) {
	p, err := Schedule([]Step{
		{ID: "add_column", Kind: KindAddColumn, Table: "users", Column: "email", Rollback: "nullable, safe"},
		{ID: "backfill", Kind: KindBackfill, Table: "users", Column: "email", Rollback: "null out"},
		{ID: "drop_old", Kind: KindDropColumn, Table: "users", Column: "old_email", Requires: []string{"backfill"}},
		{ID: "final", Kind: KindCustom, Table: "audit", Requires: []string{"drop_old"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Boundary == nil {
		t.Fatal("expected an irreversibility boundary")
	}
	if p.Boundary.StepID != "drop_old" {
		t.Fatalf("boundary should be drop_old, got %q", p.Boundary.StepID)
	}
	if p.Boundary.Phase != phaseOf(t, p, "drop_old") {
		t.Fatal("boundary phase mismatch")
	}
	for _, ph := range p.Phases {
		for _, s := range ph.Steps {
			if s.Step.ID == "drop_old" && s.Reversible {
				t.Fatal("drop_column without rollback must be irreversible")
			}
			if s.Step.ID == "add_column" && !s.Reversible {
				t.Fatal("add_column with rollback must be reversible")
			}
		}
	}
}

func TestNoRollbackIsIrreversible(t *testing.T) {
	p, err := Schedule([]Step{
		{ID: "custom", Kind: KindCustom, Table: "t"}, // no Rollback field
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Boundary == nil || p.Boundary.StepID != "custom" {
		t.Fatal("step without rollback must mark the boundary")
	}
}

func TestDeterministic(t *testing.T) {
	steps := []Step{
		{ID: "c", Kind: KindCustom, Table: "t3"},
		{ID: "a", Kind: KindCustom, Table: "t1"},
		{ID: "b", Kind: KindCustom, Table: "t2"},
	}
	p1, err := Schedule(steps)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Schedule(steps)
	if err != nil {
		t.Fatal(err)
	}
	for i := range p1.Phases {
		for j := range p1.Phases[i].Steps {
			if p1.Phases[i].Steps[j].Step.ID != p2.Phases[i].Steps[j].Step.ID {
				t.Fatal("scheduling must be deterministic")
			}
		}
	}
}
