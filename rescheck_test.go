package rescheck

import (
	"strings"
	"testing"
)

// lockResource models a lock with explicit public failure transitions:
// double-acquire fails in place, release-while-free fails into broken.
func lockResource() Resource {
	return Resource{
		Name:    "lock",
		States:  []string{"free", "held", "broken"},
		Initial: "free",
		Final:   []string{"free"},
		Transitions: []Transition{
			{From: "free", Op: "acquire", To: "held"},
			{From: "held", Op: "acquire", To: "held", Result: ResultFail},
			{From: "held", Op: "release", To: "free"},
			{From: "free", Op: "release", To: "broken", Result: ResultFail},
		},
	}
}

func script(name string, resource string, ops ...string) Script {
	sc := Script{Name: name}
	for _, op := range ops {
		sc.Actions = append(sc.Actions, Action{Resource: resource, Op: op})
	}
	return sc
}

func TestViolatedShortestTrace(t *testing.T) {
	spec := &Spec{
		Resources: []Resource{lockResource()},
		Scripts: []Script{
			script("t1", "lock", "acquire", "release"),
			script("t2", "lock", "acquire", "release"),
		},
	}
	rep, err := spec.Check()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusViolated {
		t.Fatalf("status = %s, want violated", rep.Status)
	}
	// Both scripts must run to completion, so no counterexample can be
	// shorter than the total number of actions.
	if len(rep.Trace) != 4 {
		t.Fatalf("trace length = %d, want 4 (shortest)", len(rep.Trace))
	}
	if len(rep.Leaked) == 0 {
		t.Fatal("expected leaked resources")
	}
	// The failure transition must be publicly visible in the trace.
	hasFail := false
	for _, st := range rep.Trace {
		if st.Result == ResultFail {
			hasFail = true
		}
	}
	if !hasFail {
		t.Fatal("trace should contain a public failure step")
	}
	if err := spec.Verify(rep.Trace); err != nil {
		t.Fatalf("reported trace failed independent replay: %v", err)
	}
}

func TestSafeSingleScript(t *testing.T) {
	spec := &Spec{
		Resources: []Resource{lockResource()},
		Scripts:   []Script{script("t1", "lock", "acquire", "release")},
	}
	rep, err := spec.Check()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusSafe {
		t.Fatalf("status = %s, want safe", rep.Status)
	}
}

func TestViolatedForgottenRelease(t *testing.T) {
	spec := &Spec{
		Resources: []Resource{lockResource()},
		Scripts:   []Script{script("t1", "lock", "acquire")},
	}
	rep, err := spec.Check()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusViolated {
		t.Fatalf("status = %s, want violated", rep.Status)
	}
	if len(rep.Trace) != 1 {
		t.Fatalf("trace length = %d, want 1", len(rep.Trace))
	}
	if rep.Trace[0].Result != ResultOK {
		t.Fatalf("step result = %s, want ok", rep.Trace[0].Result)
	}
	if rep.Leaked[0] != "lock@held" {
		t.Fatalf("leaked = %v, want [lock@held]", rep.Leaked)
	}
}

func TestBudgetExhaustedUnknown(t *testing.T) {
	spec := &Spec{
		Resources: []Resource{lockResource()},
		Scripts: []Script{
			script("t1", "lock", "acquire", "release"),
			script("t2", "lock", "acquire", "release"),
		},
		Budget: 2,
	}
	rep, err := spec.Check()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusUnknown {
		t.Fatalf("status = %s, want unknown", rep.Status)
	}
	if rep.Explored != 2 {
		t.Fatalf("explored = %d, want 2", rep.Explored)
	}
	if !strings.Contains(rep.Reason, "budget") {
		t.Fatalf("reason should mention budget: %q", rep.Reason)
	}
}

func TestImplicitFailureSelfLoop(t *testing.T) {
	// No explicit failure edges: a double acquire fails publicly in place.
	res := Resource{
		Name:    "sem",
		States:  []string{"down", "up"},
		Initial: "down",
		Final:   []string{"down"},
		Transitions: []Transition{
			{From: "down", Op: "up", To: "up"},
			{From: "up", Op: "down", To: "down"},
		},
	}
	safe := &Spec{
		Resources: []Resource{res},
		Scripts:   []Script{script("t1", "sem", "up", "up", "down")},
	}
	rep, err := safe.Check()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusSafe {
		t.Fatalf("status = %s, want safe", rep.Status)
	}

	leaky := &Spec{
		Resources: []Resource{res},
		Scripts:   []Script{script("t1", "sem", "up", "up")},
	}
	rep, err = leaky.Check()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusViolated {
		t.Fatalf("status = %s, want violated", rep.Status)
	}
	if rep.Trace[1].Result != ResultFail || rep.Trace[1].To != "up" {
		t.Fatalf("implicit failure should be a public self-loop, got %+v", rep.Trace[1])
	}
}

func TestMultipleResources(t *testing.T) {
	db := lockResource()
	db.Name = "db"
	spec := &Spec{
		Resources: []Resource{lockResource(), db},
		Scripts: []Script{
			{Name: "t1", Actions: []Action{
				{Resource: "lock", Op: "acquire"},
				{Resource: "db", Op: "acquire"},
				{Resource: "lock", Op: "release"},
			}},
		},
	}
	rep, err := spec.Check()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusViolated {
		t.Fatalf("status = %s, want violated", rep.Status)
	}
	if len(rep.Leaked) != 1 || rep.Leaked[0] != "db@held" {
		t.Fatalf("leaked = %v, want [db@held]", rep.Leaked)
	}
}

func TestVerifyRejectsTamperedTrace(t *testing.T) {
	spec := &Spec{
		Resources: []Resource{lockResource()},
		Scripts:   []Script{script("t1", "lock", "acquire")},
	}
	rep, err := spec.Check()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusViolated {
		t.Fatalf("status = %s, want violated", rep.Status)
	}

	// Wrong destination state.
	bad := append([]Step{}, rep.Trace...)
	bad[0].To = "free"
	if err := spec.Verify(bad); err == nil {
		t.Fatal("Verify accepted trace with wrong To state")
	}

	// Wrong result label.
	bad = append([]Step{}, rep.Trace...)
	bad[0].Result = ResultFail
	if err := spec.Verify(bad); err == nil {
		t.Fatal("Verify accepted trace with wrong result")
	}

	// Action out of script order.
	bad = append([]Step{}, rep.Trace...)
	bad[0].Op = "release"
	if err := spec.Verify(bad); err == nil {
		t.Fatal("Verify accepted trace violating script order")
	}

	// Truncated trace: script not finished.
	if err := spec.Verify(rep.Trace[:0]); err == nil {
		t.Fatal("Verify accepted incomplete trace")
	}
}

func TestVerifyRejectsNonCounterexample(t *testing.T) {
	spec := &Spec{
		Resources: []Resource{lockResource()},
		Scripts:   []Script{script("t1", "lock", "acquire", "release")},
	}
	trace := []Step{
		{Script: "t1", Resource: "lock", Op: "acquire", Result: ResultOK, From: "free", To: "held"},
		{Script: "t1", Resource: "lock", Op: "release", Result: ResultOK, From: "held", To: "free"},
	}
	if err := spec.Verify(trace); err == nil {
		t.Fatal("Verify accepted a trace with everything released")
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name string
		spec *Spec
	}{
		{"no resources", &Spec{Scripts: []Script{script("t", "x", "go")}}},
		{"duplicate resource", &Spec{
			Resources: []Resource{lockResource(), lockResource()},
		}},
		{"unknown script resource", &Spec{
			Resources: []Resource{lockResource()},
			Scripts:   []Script{script("t", "nope", "acquire")},
		}},
		{"nondeterministic", &Spec{
			Resources: []Resource{{
				Name: "r", States: []string{"a", "b", "c"}, Initial: "a", Final: []string{"a"},
				Transitions: []Transition{
					{From: "a", Op: "x", To: "b"},
					{From: "a", Op: "x", To: "c"},
				},
			}},
		}},
		{"initial not a state", &Spec{
			Resources: []Resource{{
				Name: "r", States: []string{"a"}, Initial: "zzz", Final: []string{"a"},
			}},
		}},
	}
	for _, tc := range cases {
		if err := tc.spec.Validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}

func TestEmptyScriptsSafe(t *testing.T) {
	spec := &Spec{Resources: []Resource{lockResource()}}
	rep, err := spec.Check()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusSafe {
		t.Fatalf("status = %s, want safe", rep.Status)
	}
}
