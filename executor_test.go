package dagexec

import (
	"strings"
	"sync"
	"testing"
)

var termOutcomes = []Outcome{Succeeded, Failed, Cancelled}

// The small DAG used by the enumeration:
//
//	A, B          roots
//	C             all-success(A, B)
//	D             any-success(A, B)
//	E             all-terminal(C, D)
//	F             all-success(D)
//
// It covers every condition type plus skip propagation through a chain
// (D -> F) and a join over a skipped task (C, D -> E).
func build(t *testing.T) *Executor {
	t.Helper()
	e := NewExecutor()
	specs := []struct {
		name    string
		cond    CondType
		parents []string
	}{
		{"A", AllSuccess, nil},
		{"B", AllSuccess, nil},
		{"C", AllSuccess, []string{"A", "B"}},
		{"D", AnySuccess, []string{"A", "B"}},
		{"E", AllTerminal, []string{"C", "D"}},
		{"F", AllSuccess, []string{"D"}},
	}
	for _, s := range specs {
		if err := e.Add(s.name, s.cond, s.parents...); err != nil {
			t.Fatalf("Add(%s): %v", s.name, err)
		}
	}
	return e
}

var allTasks = []string{"A", "B", "C", "D", "E", "F"}

// TestEnumerateTerminalOrders enumerates every combination of terminal
// outcomes for the roots (3x3), every completion order of the roots (2),
// and every terminal outcome for each non-root that happens to start
// (3^4), then checks the invariants on all 1458 resulting runs.
func TestEnumerateTerminalOrders(t *testing.T) {
	scenarios := 0
	coverage := make(map[string]bool)
	for _, oa := range termOutcomes {
		for _, ob := range termOutcomes {
			for _, swap := range []bool{false, true} {
				for _, oc := range termOutcomes {
					for _, od := range termOutcomes {
						for _, oe := range termOutcomes {
							for _, of := range termOutcomes {
								scenarios++
								script := map[string]Outcome{
									"A": oa, "B": ob, "C": oc,
									"D": od, "E": oe, "F": of,
								}
								e := build(t)
								queue := []string{"A", "B"}
								if swap {
									queue = []string{"B", "A"}
								}
								for len(queue) > 0 {
									name := queue[0]
									queue = queue[1:]
									started, err := e.Complete(name, script[name])
									if err != nil {
										t.Fatalf("script %v: Complete(%s): %v", script, name, err)
									}
									queue = append(queue, started...)
								}
								checkScenario(t, e, script, coverage)
							}
						}
					}
				}
			}
		}
	}
	for _, want := range []string{
		"C:start", "C:skip", "D:start", "D:skip", "F:start", "F:skip",
		"skipCause:failed", "skipCause:cancelled",
	} {
		if !coverage[want] {
			t.Errorf("enumeration never exercised %q", want)
		}
	}
	t.Logf("checked %d scenarios", scenarios)
}

func checkScenario(t *testing.T, e *Executor, script map[string]Outcome, coverage map[string]bool) {
	t.Helper()
	for _, name := range allTasks {
		task, ok := e.Task(name)
		if !ok {
			t.Fatalf("task %s missing", name)
		}
		// No permanent waiting: once the roots are terminal every task
		// must have been decided and reached a terminal outcome.
		if task.Decision.Kind == Undecided {
			t.Errorf("script %v: %s still undecided after all events", script, name)
		}
		if !task.Outcome.Terminal() {
			t.Errorf("script %v: %s has non-terminal outcome %s", script, name, task.Outcome)
		}
		switch task.Decision.Kind {
		case Started:
			// Unique start: exactly once, never more.
			if task.Starts() != 1 {
				t.Errorf("script %v: %s started %d times, want exactly 1", script, name, task.Starts())
			}
			if task.Outcome != script[name] {
				t.Errorf("script %v: %s outcome %s, want scripted %s", script, name, task.Outcome, script[name])
			}
			coverage[name+":start"] = true
		case Skip:
			if task.Starts() != 0 {
				t.Errorf("script %v: skipped %s started %d times", script, name, task.Starts())
			}
			if task.Outcome != Skipped {
				t.Errorf("script %v: skipped %s has outcome %s", script, name, task.Outcome)
			}
			coverage[name+":skip"] = true
		}
		if len(task.Parents) > 0 {
			checkDecision(t, e, &task, script)
		}
	}
	// AllTerminal can never become impossible: E must always start.
	if task, _ := e.Task("E"); task.Decision.Kind != Started {
		t.Errorf("script %v: all-terminal task E did not start (%s)", script, task.Decision.Kind)
	}
	// The reason chain of a skipped task must bottom out in tasks that
	// actually ran and failed or were cancelled, recorded distinctly.
	for _, name := range []string{"C", "D", "E", "F"} {
		task, _ := e.Task(name)
		if task.Decision.Kind != Skip {
			continue
		}
		roots := e.RootCauses(name)
		if len(roots) == 0 {
			t.Errorf("script %v: %s skipped without any root cause", script, name)
		}
		for _, rc := range roots {
			if rc.Outcome != Failed && rc.Outcome != Cancelled {
				t.Errorf("script %v: %s root cause %s=%s, want failed or cancelled",
					script, name, rc.Parent, rc.Outcome)
			}
			if actual, _ := e.Task(rc.Parent); actual.Outcome != rc.Outcome {
				t.Errorf("script %v: %s root cause %s recorded as %s, actual %s",
					script, name, rc.Parent, rc.Outcome, actual.Outcome)
			}
			coverage["skipCause:"+rc.Outcome.String()] = true
		}
	}
}

func checkDecision(t *testing.T, e *Executor, task *Task, script map[string]Outcome) {
	t.Helper()
	finals := make([]Outcome, len(task.Parents))
	for i, p := range task.Parents {
		parent, _ := e.Task(p)
		finals[i] = parent.Outcome
	}
	// The decision kind must equal the pure semantics applied to the
	// final (immutable) parent outcomes.
	wantKind, _ := decide(task.Cond, task.Parents, finals)
	if wantKind != task.Decision.Kind {
		t.Errorf("script %v: %s decided %s, semantics on final outcomes says %s",
			script, task.Name, task.Decision.Kind, wantKind)
	}
	// Every recorded cause must be valid evidence and must match the
	// parent's actual outcome: failure and cancellation are recorded
	// distinctly, never merged.
	if len(task.Decision.Reasons) == 0 {
		t.Errorf("script %v: %s decided %s without reasons", script, task.Name, task.Decision.Kind)
	}
	for _, c := range task.Decision.Reasons {
		parent, _ := e.Task(c.Parent)
		if parent.Outcome != c.Outcome {
			t.Errorf("script %v: %s cause %s recorded as %s, actual %s",
				script, task.Name, c.Parent, c.Outcome, parent.Outcome)
		}
		if !validEvidence(task.Cond, task.Decision.Kind, c.Outcome) {
			t.Errorf("script %v: %s %s: cause %s=%s is not valid evidence for %s",
				script, task.Name, task.Decision.Kind, c.Parent, c.Outcome, task.Cond)
		}
	}
	// Immediacy: the decision must have fired on the earliest event that
	// enabled it. Reconstruct the moment just before the trigger event —
	// the trigger parent and every parent that terminated after it are
	// still pending — and check that no decision was derivable then
	// (otherwise the task either decided too early or waited too long).
	if task.Decision.Trigger == "" {
		t.Errorf("script %v: %s has parents but no trigger", script, task.Name)
		return
	}
	trigger, _ := e.Task(task.Decision.Trigger)
	before := make([]Outcome, len(task.Parents))
	found := false
	for i, p := range task.Parents {
		parent, _ := e.Task(p)
		if p == task.Decision.Trigger || parent.termSeq > trigger.termSeq {
			before[i] = Pending
		} else {
			before[i] = finals[i]
		}
		if p == task.Decision.Trigger {
			found = true
		}
	}
	if !found {
		t.Errorf("script %v: %s trigger %q is not a parent", script, task.Name, task.Decision.Trigger)
		return
	}
	if kind, _ := decide(task.Cond, task.Parents, before); kind != Undecided {
		t.Errorf("script %v: %s could already decide %s before %s turned terminal",
			script, task.Name, kind, task.Decision.Trigger)
	}
}

func validEvidence(cond CondType, kind DecisionKind, o Outcome) bool {
	switch kind {
	case Started:
		switch cond {
		case AllSuccess:
			return o == Succeeded
		case AnySuccess:
			return o == Succeeded
		case AllTerminal:
			return o.Terminal()
		}
	case Skip:
		// A skip is only ever justified by a terminal non-success.
		return o.Terminal() && !o.Success()
	}
	return false
}

// TestReasonChainDistinguishesFailureAndCancellation walks one concrete
// scenario and checks the recorded chain end to end.
func TestReasonChainDistinguishesFailureAndCancellation(t *testing.T) {
	e := build(t)
	if _, err := e.Complete("A", Failed); err != nil {
		t.Fatal(err)
	}
	// C (all-success) must have been skipped immediately by A's failure,
	// while D (any-success) is still waiting on B.
	if task, _ := e.Task("C"); task.Decision.Kind != Skip || task.Decision.Trigger != "A" {
		t.Fatalf("C = %+v, want immediate skip triggered by A", task.Decision)
	}
	if task, _ := e.Task("D"); task.Decision.Kind != Undecided {
		t.Fatalf("D = %+v, want still undecided", task.Decision)
	}
	started, err := e.Complete("B", Cancelled)
	if err != nil {
		t.Fatal(err)
	}
	// D's condition is now impossible; D and (transitively) F are
	// skipped, E (all-terminal) starts.
	if len(started) != 1 || started[0] != "E" {
		t.Fatalf("started = %v, want [E]", started)
	}
	c, _ := e.Task("C")
	if len(c.Decision.Reasons) != 1 || c.Decision.Reasons[0] != (Cause{Parent: "A", Outcome: Failed}) {
		t.Errorf("C reasons = %v, want [{A failed}]", c.Decision.Reasons)
	}
	d, _ := e.Task("D")
	wantD := []Cause{{Parent: "A", Outcome: Failed}, {Parent: "B", Outcome: Cancelled}}
	if !equalCauses(d.Decision.Reasons, wantD) {
		t.Errorf("D reasons = %v, want %v (failure and cancellation distinct)", d.Decision.Reasons, wantD)
	}
	f, _ := e.Task("F")
	if f.Decision.Kind != Skip || f.Decision.Trigger != "D" {
		t.Errorf("F = %+v, want skip triggered by D", f.Decision)
	}
	roots := e.RootCauses("F")
	if !equalCauses(roots, wantD) {
		t.Errorf("RootCauses(F) = %v, want %v", roots, wantD)
	}
	ex := e.Explain("F")
	for _, want := range []string{"F: skipped", "D was skipped", "A failed", "B was cancelled"} {
		if !strings.Contains(ex, want) {
			t.Errorf("Explain(F) missing %q:\n%s", want, ex)
		}
	}
	if _, err := e.Complete("E", Succeeded); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentCompletionStartsChildOnce hammers the executor with
// concurrent completions and checks the child still starts exactly once.
func TestConcurrentCompletionStartsChildOnce(t *testing.T) {
	for i := 0; i < 200; i++ {
		e := NewExecutor()
		for _, s := range []struct {
			name    string
			cond    CondType
			parents []string
		}{
			{"A", AllSuccess, nil},
			{"B", AllSuccess, nil},
			{"C", AllSuccess, []string{"A", "B"}},
		} {
			if err := e.Add(s.name, s.cond, s.parents...); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		for _, n := range []string{"A", "B"} {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				if _, err := e.Complete(name, Succeeded); err != nil {
					t.Error(err)
				}
			}(n)
		}
		wg.Wait()
		if task, _ := e.Task("C"); task.Starts() != 1 {
			t.Fatalf("iteration %d: C started %d times, want 1", i, task.Starts())
		}
	}
}

func equalCauses(a, b []Cause) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
