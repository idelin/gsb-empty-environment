package compensation

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// recorder collects compensation invocations safely across goroutines.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, id)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func okRun(Context) error { return nil }

func compensate(rec *recorder, id string) func(Context) error {
	return func(Context) error {
		rec.add(id)
		return nil
	}
}

// Graph under test:
//
//	a (root)
//	├── b          c (parallel branch of b)
//	│   └── d      └── e
//	f (independent root, runs in the same level as a)
//
// d fails, so a, b, c, e, f must all be compensated — ancestors and
// parallel branches alike — in reverse dependency order.
func TestFailureCompensatesAncestorsAndParallelBranches(t *testing.T) {
	rec := &recorder{}
	steps := []Step{
		{ID: "a", Run: okRun, Compensate: compensate(rec, "a")},
		{ID: "f", Run: okRun, Compensate: compensate(rec, "f")},
		{ID: "b", DependsOn: []string{"a"}, Run: okRun, Compensate: compensate(rec, "b")},
		{ID: "c", DependsOn: []string{"a"}, Run: okRun, Compensate: compensate(rec, "c")},
		{ID: "e", DependsOn: []string{"c"}, Run: okRun, Compensate: compensate(rec, "e")},
		{ID: "d", DependsOn: []string{"b"}, Run: func(Context) error { return errors.New("boom") }},
	}
	ex := &Executor{}
	res, err := ex.Execute(nil, steps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.FailedStep != "d" {
		t.Fatalf("FailedStep = %q, want d", res.FailedStep)
	}
	if res.Plan == nil || res.Report == nil {
		t.Fatal("expected a compensation plan and report")
	}
	if !res.Report.Success() {
		t.Fatalf("report should be a full success, got %+v", res.Report)
	}
	want := []string{"f", "e", "c", "b", "a"}
	if got := rec.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("compensation order = %v, want %v", got, want)
	}
	// Plan entries must be in the same reverse dependency order.
	var order []string
	for _, e := range res.Plan.Entries {
		order = append(order, e.StepID)
		if e.Status != StatusDone {
			t.Fatalf("entry %s status = %s, want done", e.StepID, e.Status)
		}
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("plan order = %v, want %v", order, want)
	}
}

func TestSuccessfulRunNeedsNoCompensation(t *testing.T) {
	rec := &recorder{}
	steps := []Step{
		{ID: "a", Run: okRun, Compensate: compensate(rec, "a")},
		{ID: "b", DependsOn: []string{"a"}, Run: okRun, Compensate: compensate(rec, "b")},
	}
	res, err := (&Executor{}).Execute(nil, steps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Plan != nil || res.Report != nil {
		t.Fatal("no compensation expected on success")
	}
	if len(rec.snapshot()) != 0 {
		t.Fatal("no step should have been compensated")
	}
}

func TestUncompensatableStepBecomesManualItem(t *testing.T) {
	rec := &recorder{}
	steps := []Step{
		{ID: "a", Run: okRun, Compensate: compensate(rec, "a")},
		{ID: "b", DependsOn: []string{"a"}, Run: okRun}, // no Compensate
		{ID: "c", DependsOn: []string{"b"}, Run: func(Context) error { return errors.New("boom") }},
	}
	res, err := (&Executor{}).Execute(nil, steps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	rep := res.Report
	if rep.Success() {
		t.Fatal("rollback with a manual item must not be reported as success")
	}
	if len(rep.Manual) != 1 || rep.Manual[0].StepID != "b" {
		t.Fatalf("manual items = %+v, want only b", rep.Manual)
	}
	if !reflect.DeepEqual(rep.Compensated, []string{"a"}) {
		t.Fatalf("compensated = %v, want [a]", rep.Compensated)
	}
	if len(rep.Failed) != 0 {
		t.Fatalf("failed = %v, want empty", rep.Failed)
	}
}

func TestCompensationFailureIsRetried(t *testing.T) {
	rec := &recorder{}
	attempts := 0
	steps := []Step{
		{ID: "a", Run: okRun, Compensate: compensate(rec, "a")},
		{ID: "b", DependsOn: []string{"a"}, Run: okRun, Compensate: func(Context) error {
			attempts++
			rec.add("b")
			if attempts < 3 {
				return fmt.Errorf("transient %d", attempts)
			}
			return nil
		}},
		{ID: "c", DependsOn: []string{"b"}, Run: func(Context) error { return errors.New("boom") }},
	}
	ex := &Executor{MaxCompensateAttempts: 3}
	res, err := ex.Execute(nil, steps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// First Run call only makes one attempt per entry.
	if attempts != 1 {
		t.Fatalf("attempts after first run = %d, want 1", attempts)
	}
	if !reflect.DeepEqual(res.Report.Failed, []string{"b"}) {
		t.Fatalf("failed = %v, want [b]", res.Report.Failed)
	}
	// Resume the same plan: b is retried until it succeeds.
	var rep *RollbackReport
	for i := 0; i < 2 && res.Plan.Pending(3); i++ {
		rep = res.Plan.Run(nil, map[string]func(Context) error{
			"a": compensate(rec, "a"),
			"b": steps[1].Compensate,
		}, 3)
	}
	if rep == nil || !rep.Success() {
		t.Fatalf("report after retries = %+v, want success", rep)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	// a must be compensated exactly once across all runs.
	count := 0
	for _, c := range rec.snapshot() {
		if c == "a" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("a compensated %d times, want exactly 1", count)
	}
}

func TestExhaustedCompensationBecomesManual(t *testing.T) {
	steps := []Step{
		{ID: "a", Run: okRun, Compensate: func(Context) error { return errors.New("always fails") }},
		{ID: "b", DependsOn: []string{"a"}, Run: func(Context) error { return errors.New("boom") }},
	}
	ex := &Executor{MaxCompensateAttempts: 2}
	res, err := ex.Execute(nil, steps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Run the plan again so the failed entry uses its second attempt.
	rep := res.Plan.Run(nil, map[string]func(Context) error{"a": steps[0].Compensate}, 2)
	if rep.Success() {
		t.Fatal("exhausted compensation must not be a success")
	}
	if len(rep.Manual) != 1 || rep.Manual[0].StepID != "a" {
		t.Fatalf("manual = %+v, want only a", rep.Manual)
	}
	if len(rep.Compensated) != 0 {
		t.Fatalf("compensated = %v, want empty", rep.Compensated)
	}
}

func TestPlanResumeFromDisk(t *testing.T) {
	dir, err := ioutil.TempDir("", "compensation-test")
	if err != nil {
		t.Fatalf("TempDir: %v", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "plan.json")
	rec := &recorder{}
	steps := []Step{
		{ID: "a", Run: okRun, Compensate: compensate(rec, "a")},
		{ID: "b", DependsOn: []string{"a"}, Run: okRun, Compensate: compensate(rec, "b")},
		{ID: "c", DependsOn: []string{"b"}, Run: func(Context) error { return errors.New("boom") }},
	}
	ex := &Executor{PlanPath: path}
	res, err := ex.Execute(nil, steps)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Report.Success() {
		t.Fatalf("report = %+v, want success", res.Report)
	}

	// Simulate a crash mid-plan: persist a plan where b is done and a
	// is still pending, then resume from disk.
	crash := &Plan{Entries: []*PlanEntry{
		{StepID: "b", Status: StatusDone, Attempts: 1},
		{StepID: "a", Status: StatusPending},
	}}
	if err := crash.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rec2 := &recorder{}
	plan, rep, err := Resume(nil, path, map[string]func(Context) error{
		"a": compensate(rec2, "a"),
		"b": compensate(rec2, "b"),
	}, 1)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !rep.Success() {
		t.Fatalf("resumed report = %+v, want success", rep)
	}
	// b was already done before the crash and must not run again.
	if got := rec2.snapshot(); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("resumed compensations = %v, want [a]", got)
	}
	if plan.Pending(1) {
		t.Fatal("plan should have nothing pending after successful resume")
	}
}

func TestGraphValidation(t *testing.T) {
	cases := []struct {
		name  string
		steps []Step
	}{
		{"duplicate IDs", []Step{{ID: "a", Run: okRun}, {ID: "a", Run: okRun}}},
		{"unknown dependency", []Step{{ID: "a", Run: okRun, DependsOn: []string{"nope"}}}},
		{"self dependency", []Step{{ID: "a", Run: okRun, DependsOn: []string{"a"}}}},
		{"cycle", []Step{
			{ID: "a", Run: okRun, DependsOn: []string{"b"}},
			{ID: "b", Run: okRun, DependsOn: []string{"a"}},
		}},
		{"nil run", []Step{{ID: "a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := (&Executor{}).Execute(nil, tc.steps); err == nil {
				t.Fatal("expected a graph validation error")
			}
		})
	}
}
