package txn

import (
	"errors"
	"reflect"
	"testing"
)

func graphOf(units ...*Unit) *Graph {
	g := &Graph{Units: map[string]*Unit{}}
	for _, u := range units {
		g.Units[u.Name] = u
	}
	return g
}

func edgeSet(plan *Plan) map[[2]string]OrderEdge {
	out := map[[2]string]OrderEdge{}
	for _, e := range plan.Edges {
		out[[2]string{e.From, e.To}] = e
	}
	return out
}

func assertJob(t *testing.T, plan *Plan, unit string, want JobType) {
	t.Helper()
	got, ok := plan.JobType(unit)
	if !ok {
		t.Fatalf("expected job for %s, plan has none", unit)
	}
	if got != want {
		t.Fatalf("job for %s = %s, want %s", unit, got, want)
	}
}

func assertNoJob(t *testing.T, plan *Plan, unit string) {
	t.Helper()
	if _, ok := plan.JobType(unit); ok {
		t.Fatalf("did not expect a job for %s", unit)
	}
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("err = %v, want errors.Is %v", err, target)
	}
}

// TestRequirementOrderingOrthogonal verifies that Requires pulls a start job
// without imposing any ordering; only explicit Before/After order the jobs.
func TestRequirementOrderingOrthogonal(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Requires: []string{"b.service"}},
		&Unit{Name: "b.service"},
	)
	plan, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "a.service", JobStart)
	assertJob(t, plan, "b.service", JobStart)
	if len(plan.Edges) != 0 {
		t.Fatalf("Requires must not create ordering edges, got %+v", plan.Edges)
	}
	if !reflect.DeepEqual(plan.Layers, [][]string{{"a.service", "b.service"}}) {
		t.Fatalf("unordered jobs should share one layer, got %+v", plan.Layers)
	}

	g.Units["b.service"].After = []string{"a.service"}
	plan, err = Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	edge, ok := edgeSet(plan)[[2]string{"a.service", "b.service"}]
	if !ok {
		t.Fatalf("expected ordering edge a -> b, got %+v", plan.Edges)
	}
	if edge.Reversed {
		t.Fatalf("startup edge must not be reversed")
	}
	if !reflect.DeepEqual(plan.Layers, [][]string{{"a.service"}, {"b.service"}}) {
		t.Fatalf("expected a before b layers, got %+v", plan.Layers)
	}
}

// TestRequisiteNeverPullsStart covers the Requisite gate: an inactive target
// fails the transaction, an active target yields a verify-active gate only.
func TestRequisiteNeverPullsStart(t *testing.T) {
	g := graphOf(
		&Unit{Name: "a.service", Requisite: []string{"b.service"}},
		&Unit{Name: "b.service"},
	)

	_, err := Solve(g, Snapshot{}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	assertErrorIs(t, err, ErrRequisiteNotActive)

	plan, err := Solve(g, Snapshot{"b.service": true}, Request{Unit: "a.service", Job: JobStart, Mode: ModeFail})
	if err != nil {
		t.Fatal(err)
	}
	assertJob(t, plan, "a.service", JobStart)
	assertJob(t, plan, "b.service", JobVerifyActive)
	if len(plan.Jobs) != 2 {
		t.Fatalf("Requisite must not pull a start job, got %+v", plan.Jobs)
	}
}

// TestConflictsFailMode rejects a start/stop collision outright.
func TestConflictsFailMode(t *testing.T) {
	g := graphOf(
		&Unit{Name: "anchor.service",
			Requires:  []string{"a.service", "b.service"},
			Conflicts: []string{"b.service"}},
		&Unit{Name: "a.service"},
		&Unit{Name: "b.service"},
	)
	_, err := Solve(g, Snapshot{"b.service": true}, Request{Unit: "anchor.service", Job: JobStart, Mode: ModeFail})
	assertErrorIs(t, err, ErrConflict)
}
