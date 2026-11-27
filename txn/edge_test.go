package txn

import (
	"reflect"
	"testing"
)

func TestParseMode(t *testing.T) {
	cases := map[string]TransactionMode{
		"fail":                 ModeFail,
		"replace":              ModeReplace,
		"replace-irreversibly": ModeReplaceIrreversibly,
	}
	for raw, want := range cases {
		got, err := ParseMode(raw)
		if err != nil || got != want {
			t.Fatalf("ParseMode(%q) = %v, %v want %v", raw, got, err, want)
		}
	}
	if _, err := ParseMode("nope"); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestUnknownHardDependencyRejected(t *testing.T) {
	g := graphOf(&Unit{Name: "a.service", Requires: []string{"missing.service"}})
	_, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	assertErrorIs(t, err, ErrUnknownUnit)

	// A missing Wants target is silently tolerated.
	g2 := graphOf(&Unit{Name: "a.service", Wants: []string{"missing.service"}})
	if _, err := Solve(g2, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail}); err != nil {
		t.Fatalf("missing Wants must be ignored, got %v", err)
	}
}

func TestBindsToStopPropagation(t *testing.T) {
	g := graphOf(
		&Unit{Name: "parent.service"},
		&Unit{Name: "bound.service", BindsTo: []string{"parent.service"}},
	)
	plan, err := Solve(g, Snapshot{}, Request{Unit: "parent.service", Job: JobStop, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "bound.service", JobStop)
}

func TestRestartPartOfPropagation(t *testing.T) {
	g := graphOf(
		&Unit{Name: "parent.service"},
		&Unit{Name: "child.service", PartOf: []string{"parent.service"}},
	)
	plan, err := Solve(g, Snapshot{}, Request{Unit: "parent.service", Job: JobRestart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "parent.service", JobRestart)
	assertJob(t, plan, "child.service", JobRestart)
}

func TestVerifyActiveParticipatesInOrdering(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Requisite: []string{"b.service"}, After: []string{"b.service"}},
		&Unit{Name: "b.service"},
	)
	plan, err := Solve(g, Snapshot{"b.service": true}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "b.service", JobVerifyActive)
	if _, ok := edgeSet(plan)[[2]string{"b.service", "a.service"}]; !ok {
		t.Fatalf("requisite gate must participate in ordering b -> a, got %+v", plan.Edges)
	}
}

func TestInvalidRequests(t *testing.T) {
	g := graphOf(&Unit{Name: "a.service"})
	if _, err := Solve(g, Snapshot{}, Request{Unit: "ghost.service", Job: JobStart, Mode: ModeFail}); err == nil {
		t.Fatal("expected error for unknown anchor")
	}
	if _, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobVerifyActive, Mode: ModeFail}); err == nil {
		t.Fatal("expected error for internal job type in request")
	}
}

func TestSelfLoopCycleCut(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Before: []string{"a.service"}, Requires: []string{"b.service"}},
		&Unit{Name: "b.service"},
	)
	plan, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cuts) != 1 {
		t.Fatalf("expected the self-loop to be cut once, got %+v", plan.Cuts)
	}
	if !reflect.DeepEqual(plan.Cuts[0].Cycle, []string{"a.service"}) {
		t.Fatalf("self-loop cycle must report one unit, got %+v", plan.Cuts[0].Cycle)
	}
	if len(plan.Layers) != 1 {
		t.Fatalf("self-loop cut must leave an acyclic graph, got %+v", plan.Layers)
	}
}
