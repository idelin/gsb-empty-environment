package compensation

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

// recorder collects compensation calls safely.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, id)
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func okRun(context.Context) error { return nil }

// buildGraph creates: a -> b -> c, plus parallel branch a -> d.
// c fails on run.
func buildGraph(t *testing.T, rec *recorder, compensate map[string]CompensateFunc, compensatable map[string]bool) *Executor {
	t.Helper()
	g := NewGraph()
	mk := func(id string, deps ...string) Step {
		return Step{
			ID:            id,
			DependsOn:     deps,
			Compensatable: compensatable[id],
			Run:           okRun,
			Compensate: func(ctx context.Context) error {
				rec.add(id)
				if h := compensate[id]; h != nil {
					return h(ctx)
				}
				return nil
			},
		}
	}
	fail := errors.New("step c failed")
	steps := []Step{
		mk("a"),
		mk("b", "a"),
		mk("d", "a"),
		{
			ID:            "c",
			DependsOn:     []string{"b"},
			Compensatable: true,
			Run:           func(context.Context) error { return fail },
		},
	}
	for _, s := range steps {
		if err := g.Add(s); err != nil {
			t.Fatalf("add %s: %v", s.ID, err)
		}
	}
	return NewExecutor(g, WithSleeper(func(context.Context, time.Duration) error { return nil }))
}

func allCompensatable(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestForwardSuccessNoPlan(t *testing.T) {
	g := NewGraph()
	_ = g.Add(Step{ID: "a", Compensatable: true, Run: okRun})
	_ = g.Add(Step{ID: "b", DependsOn: []string{"a"}, Compensatable: true, Run: okRun})

	res := NewExecutor(g).Execute(context.Background())
	if res.RunErr != nil {
		t.Fatalf("unexpected error: %v", res.RunErr)
	}
	if res.Plan != nil {
		t.Fatalf("expected no plan on success, got %+v", res.Plan)
	}
	if !reflect.DeepEqual(res.Completed, []string{"a", "b"}) {
		t.Fatalf("completed = %v", res.Completed)
	}
}

func TestFailureCompensatesAncestorsAndParallelBranchesInReverseOrder(t *testing.T) {
	rec := &recorder{}
	ex := buildGraph(t, rec, nil, allCompensatable("a", "b", "d"))

	res := ex.Execute(context.Background())
	if res.RunErr == nil || res.FailedStep != "c" {
		t.Fatalf("expected failure at c, got step=%q err=%v", res.FailedStep, res.RunErr)
	}
	if res.Plan == nil {
		t.Fatal("expected a compensation plan")
	}
	if !reflect.DeepEqual(res.Completed, []string{"a", "b", "d"}) {
		t.Fatalf("completed = %v", res.Completed)
	}

	if err := res.Plan.Run(context.Background()); err != nil {
		t.Fatalf("plan run: %v", err)
	}
	// Reverse dependency order: dependents before dependencies.
	want := []string{"d", "b", "a"}
	if got := rec.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("compensation order = %v, want %v", got, want)
	}
	if st := res.Plan.Status(); st != PlanRolledBack {
		t.Fatalf("status = %s, want %s", st, PlanRolledBack)
	}
}

func TestCompensationRetriesUntilSuccess(t *testing.T) {
	rec := &recorder{}
	attempts := 0
	handlers := map[string]CompensateFunc{
		"b": func(context.Context) error {
			attempts++
			if attempts < 3 {
				return errBoom
			}
			return nil
		},
	}
	ex := buildGraph(t, rec, handlers, allCompensatable("a", "b", "d"))
	ex.retry = RetryPolicy{MaxAttempts: 3, Backoff: time.Millisecond}

	res := ex.Execute(context.Background())
	if err := res.Plan.Run(context.Background()); err != nil {
		t.Fatalf("plan run: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if st := res.Plan.Status(); st != PlanRolledBack {
		t.Fatalf("status = %s, want %s", st, PlanRolledBack)
	}
	for _, e := range res.Plan.Entries() {
		if e.StepID == "b" && e.Attempts != 3 {
			t.Fatalf("entry b attempts = %d, want 3", e.Attempts)
		}
	}
}

func TestExhaustedRetriesStayFailedAndResume(t *testing.T) {
	rec := &recorder{}
	fail := true
	handlers := map[string]CompensateFunc{
		"b": func(context.Context) error {
			if fail {
				return errBoom
			}
			return nil
		},
	}
	ex := buildGraph(t, rec, handlers, allCompensatable("a", "b", "d"))
	ex.retry = RetryPolicy{MaxAttempts: 2}

	res := ex.Execute(context.Background())
	plan := res.Plan

	err := plan.Run(context.Background())
	if err == nil {
		t.Fatal("expected ErrIncomplete")
	}
	if st := plan.Status(); st != PlanFailed {
		t.Fatalf("status = %s, want %s", st, PlanFailed)
	}
	// b failed, but a and d must still have been compensated.
	for _, e := range plan.Entries() {
		switch e.StepID {
		case "b":
			if e.Status != EntryFailed || e.Attempts != 2 {
				t.Fatalf("entry b = %+v", e)
			}
		case "a", "d":
			if e.Status != EntrySucceeded {
				t.Fatalf("entry %s = %+v", e.StepID, e)
			}
		}
	}

	// Fix the external system and resume: only b is retried.
	fail = false
	if err := plan.Run(context.Background()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if st := plan.Status(); st != PlanRolledBack {
		t.Fatalf("status = %s, want %s", st, PlanRolledBack)
	}
}

func TestNonCompensatableStepBecomesManualItem(t *testing.T) {
	rec := &recorder{}
	compensatable := allCompensatable("a", "d") // b is NOT compensatable
	ex := buildGraph(t, rec, nil, compensatable)

	res := ex.Execute(context.Background())
	plan := res.Plan
	if err := plan.Run(context.Background()); err != nil {
		t.Fatalf("plan run: %v", err)
	}

	if st := plan.Status(); st != PlanManualRequired {
		t.Fatalf("status = %s, want %s (must not report rolled back)", st, PlanManualRequired)
	}
	manual := plan.ManualItems()
	if len(manual) != 1 || manual[0].StepID != "b" {
		t.Fatalf("manual items = %+v", manual)
	}
	// Compensatable steps still ran.
	if got := rec.all(); !reflect.DeepEqual(got, []string{"d", "a"}) {
		t.Fatalf("compensated = %v, want [d a]", got)
	}
}

func TestSnapshotRestoreResumesPlan(t *testing.T) {
	rec := &recorder{}
	fail := true
	handlers := map[string]CompensateFunc{
		"b": func(context.Context) error {
			if fail {
				return errBoom
			}
			return nil
		},
	}
	ex := buildGraph(t, rec, handlers, allCompensatable("a", "b", "d"))
	ex.retry = RetryPolicy{MaxAttempts: 1}

	res := ex.Execute(context.Background())
	plan := res.Plan
	if err := plan.Run(context.Background()); err == nil {
		t.Fatal("expected failure")
	}

	data, err := plan.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Simulate a restart: new registry, fixed handler.
	restoredRec := &recorder{}
	registry := HandlerRegistry{
		"b": func(context.Context) error { restoredRec.add("b"); return nil },
	}
	restored, err := RestorePlan(data, registry, RetryPolicy{MaxAttempts: 1}, nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := restored.Run(context.Background()); err != nil {
		t.Fatalf("restored run: %v", err)
	}
	if st := restored.Status(); st != PlanRolledBack {
		t.Fatalf("status = %s, want %s", st, PlanRolledBack)
	}
	// Only the failed entry was re-executed after restore.
	if got := restoredRec.all(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("re-executed = %v, want [b]", got)
	}
	// Attempt counter survived the snapshot.
	for _, e := range restored.Entries() {
		if e.StepID == "b" && e.Attempts != 2 {
			t.Fatalf("entry b attempts = %d, want 2", e.Attempts)
		}
	}
}

func TestRestoredPlanWithoutHandlerBecomesManual(t *testing.T) {
	rec := &recorder{}
	ex := buildGraph(t, rec, nil, allCompensatable("a", "b", "d"))
	res := ex.Execute(context.Background())

	data, err := res.Plan.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// Restore without a handler for b.
	restored, err := RestorePlan(data, HandlerRegistry{}, RetryPolicy{}, nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := restored.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if st := restored.Status(); st != PlanManualRequired {
		t.Fatalf("status = %s, want %s", st, PlanManualRequired)
	}
	if len(restored.ManualItems()) != 3 {
		t.Fatalf("manual items = %+v", restored.ManualItems())
	}
}

func TestMarkManualForFailedEntry(t *testing.T) {
	rec := &recorder{}
	handlers := map[string]CompensateFunc{
		"b": func(context.Context) error { return errBoom },
	}
	ex := buildGraph(t, rec, handlers, allCompensatable("a", "b", "d"))
	res := ex.Execute(context.Background())
	plan := res.Plan
	_ = plan.Run(context.Background())

	if err := plan.MarkManual("b", "external system unreachable, ops ticket OPS-1"); err != nil {
		t.Fatalf("mark manual: %v", err)
	}
	if st := plan.Status(); st != PlanManualRequired {
		t.Fatalf("status = %s, want %s", st, PlanManualRequired)
	}
	if err := plan.MarkManual("a", "nope"); err == nil {
		t.Fatal("expected error marking a succeeded entry manual")
	}
}

func TestContextCancellationKeepsPlanResumable(t *testing.T) {
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	handlers := map[string]CompensateFunc{
		"b": func(context.Context) error { cancel(); return errBoom },
	}
	ex := buildGraph(t, rec, handlers, allCompensatable("a", "b", "d"))
	ex.retry = RetryPolicy{MaxAttempts: 5, Backoff: time.Hour} // sleeper honours ctx

	res := ex.Execute(context.Background())
	plan := res.Plan
	if err := plan.Run(ctx); err == nil {
		t.Fatal("expected cancellation error")
	}
	// a was never reached; plan is still resumable.
	pending := 0
	for _, e := range plan.Entries() {
		if e.Status == EntryPending {
			pending++
		}
	}
	if pending == 0 {
		t.Fatal("expected pending entries after cancellation")
	}
}

func TestGraphValidation(t *testing.T) {
	// Duplicate ID.
	g := NewGraph()
	_ = g.Add(Step{ID: "a"})
	if err := g.Add(Step{ID: "a"}); err == nil {
		t.Fatal("expected duplicate ID error")
	}
	// Missing dependency.
	g = NewGraph()
	_ = g.Add(Step{ID: "a", DependsOn: []string{"ghost"}})
	if res := NewExecutor(g).Execute(context.Background()); res.RunErr == nil {
		t.Fatal("expected missing dependency error")
	}
	// Cycle.
	g = NewGraph()
	_ = g.Add(Step{ID: "a", DependsOn: []string{"b"}})
	_ = g.Add(Step{ID: "b", DependsOn: []string{"a"}})
	if res := NewExecutor(g).Execute(context.Background()); res.RunErr == nil {
		t.Fatal("expected cycle error")
	}
}
