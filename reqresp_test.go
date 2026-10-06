package reqresp

import (
	"reflect"
	"strings"
	"testing"
)

func req(id string) Action  { return Action{Kind: Request, ID: id} }
func resp(id string) Action { return Action{Kind: Response, ID: id} }
func tau() Action           { return Action{Kind: Internal} }

func mustGraph(t *testing.T, initial string, edges []Edge) *Graph {
	t.Helper()
	g, err := NewGraph(initial, edges)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func mustCheck(t *testing.T, g *Graph) *Result {
	t.Helper()
	res, err := Check(g)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	return res
}

func TestOKAlwaysResponds(t *testing.T) {
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s0", Action: resp("a")},
	})
	res := mustCheck(t, g)
	if !res.OK() {
		t.Fatalf("expected OK, got %v", res.Violation)
	}
}

func TestOKInterleavedPending(t *testing.T) {
	// Mid-cycle the pending set is non-empty, but no single id stays
	// owed for a whole loop: every request is answered each round.
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s2", Action: req("b")},
		{From: "s2", To: "s3", Action: resp("a")},
		{From: "s3", To: "s0", Action: resp("b")},
	})
	res := mustCheck(t, g)
	if !res.OK() {
		t.Fatalf("expected OK, got %v", res.Violation)
	}
}

func TestOKCycleRespondsEachRound(t *testing.T) {
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s2", Action: tau()},
		{From: "s2", To: "s3", Action: resp("a")},
		{From: "s3", To: "s1", Action: req("a")},
	})
	res := mustCheck(t, g)
	if !res.OK() {
		t.Fatalf("expected OK, got %v", res.Violation)
	}
}

func TestOKTerminalNoPending(t *testing.T) {
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s2", Action: resp("a")}, // s2 terminal, nothing owed
	})
	res := mustCheck(t, g)
	if !res.OK() {
		t.Fatalf("expected OK, got %v", res.Violation)
	}
}

func TestOKSingleTerminalState(t *testing.T) {
	g := mustGraph(t, "s0", nil)
	res := mustCheck(t, g)
	if !res.OK() {
		t.Fatalf("expected OK, got %v", res.Violation)
	}
	if res.ProductStates != 1 {
		t.Fatalf("expected 1 product state, got %d", res.ProductStates)
	}
}

func TestLassoViolation(t *testing.T) {
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s1", Action: tau()}, // may spin forever, never responding
		{From: "s1", To: "s0", Action: resp("a")},
	})
	res := mustCheck(t, g)
	if res.OK() {
		t.Fatal("expected violation")
	}
	v := res.Violation
	if !v.Lasso() {
		t.Fatalf("expected lasso, got %v", v)
	}
	if !reflect.DeepEqual(v.Owed, []string{"a"}) {
		t.Fatalf("expected owed [a], got %v", v.Owed)
	}
	if err := Verify(g, v); err != nil {
		t.Fatalf("Verify rejected genuine counterexample: %v\n%s", err, v)
	}
}

func TestTerminalViolation(t *testing.T) {
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")}, // s1 terminal, a owed
	})
	res := mustCheck(t, g)
	if res.OK() {
		t.Fatal("expected violation")
	}
	v := res.Violation
	if v.Lasso() {
		t.Fatalf("expected finite violation, got %v", v)
	}
	if !reflect.DeepEqual(v.Owed, []string{"a"}) {
		t.Fatalf("expected owed [a], got %v", v.Owed)
	}
	if err := Verify(g, v); err != nil {
		t.Fatalf("Verify rejected genuine counterexample: %v\n%s", err, v)
	}
}

func TestMultipleIDsTrackedSeparately(t *testing.T) {
	// a is answered, b never is; the loop must owe exactly b.
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s2", Action: req("b")},
		{From: "s2", To: "s3", Action: resp("a")},
		{From: "s3", To: "s3", Action: tau()},
		{From: "s3", To: "s4", Action: resp("b")},
		{From: "s4", To: "s0", Action: tau()},
	})
	res := mustCheck(t, g)
	if res.OK() {
		t.Fatal("expected violation")
	}
	v := res.Violation
	if !reflect.DeepEqual(v.Owed, []string{"b"}) {
		t.Fatalf("expected owed [b], got %v", v.Owed)
	}
	if err := Verify(g, v); err != nil {
		t.Fatalf("Verify rejected genuine counterexample: %v\n%s", err, v)
	}
}

func TestIDReuseAfterResponse(t *testing.T) {
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s2", Action: resp("a")},
		{From: "s2", To: "s3", Action: req("a")}, // same id again, now legal
		{From: "s3", To: "s0", Action: resp("a")},
	})
	res := mustCheck(t, g)
	if !res.OK() {
		t.Fatalf("expected OK, got %v", res.Violation)
	}
}

func TestDuplicatePendingRequestIsModelError(t *testing.T) {
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s2", Action: req("a")},
	})
	if _, err := Check(g); err == nil || !strings.Contains(err.Error(), "already pending") {
		t.Fatalf("expected duplicate-request error, got %v", err)
	}
}

func TestUnmatchedResponseIsModelError(t *testing.T) {
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: resp("a")},
	})
	if _, err := Check(g); err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("expected unmatched-response error, got %v", err)
	}
}

func TestNewGraphValidation(t *testing.T) {
	if _, err := NewGraph("", nil); err == nil {
		t.Fatal("expected error for empty initial state")
	}
	if _, err := NewGraph("s0", []Edge{{From: "s0", To: "s1", Action: tau()}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := NewGraph("s0", []Edge{{From: "s0", To: "s1", Action: Action{Kind: Internal, ID: "x"}}}); err == nil {
		t.Fatal("expected error for internal action with id")
	}
	if _, err := NewGraph("s0", []Edge{{From: "s0", To: "s1", Action: Action{Kind: Request}}}); err == nil {
		t.Fatal("expected error for request without id")
	}
}

func lassoFixture(t *testing.T) (*Graph, *Violation) {
	t.Helper()
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s1", Action: tau()},
		{From: "s1", To: "s0", Action: resp("a")},
	})
	res := mustCheck(t, g)
	if res.OK() {
		t.Fatal("expected violation")
	}
	return g, res.Violation
}

func TestVerifyRejectsTamperedViolations(t *testing.T) {
	g, v := lassoFixture(t)

	cases := map[string]*Violation{
		"nil owed": {
			Prefix: v.Prefix, Cycle: v.Cycle, Owed: nil,
		},
		"wrong owed id": {
			Prefix: v.Prefix, Cycle: v.Cycle, Owed: []string{"zzz"},
		},
		"extra owed id": {
			Prefix: v.Prefix, Cycle: v.Cycle, Owed: []string{"a", "b"},
		},
		"duplicate owed id": {
			Prefix: v.Prefix, Cycle: v.Cycle, Owed: []string{"a", "a"},
		},
		"cycle truncated": {
			Prefix: v.Prefix, Cycle: v.Cycle[:len(v.Cycle)-1], Owed: v.Owed,
		},
		"cycle step not an edge": {
			Prefix: v.Prefix,
			Cycle:  []Step{{From: "s1", To: "s1", Action: req("a")}},
			Owed:   v.Owed,
		},
		"prefix step not an edge": {
			Prefix: []Step{{From: "s0", To: "s1", Action: tau()}},
			Cycle:  v.Cycle,
			Owed:   v.Owed,
		},
		"prefix disconnected": {
			Prefix: append(append([]Step(nil), v.Prefix...), Step{From: "s1", To: "s0", Action: tau()}),
			Cycle:  v.Cycle,
			Owed:   v.Owed,
		},
		"cycle dropped from lasso": {
			Prefix: v.Prefix, Cycle: nil, Owed: v.Owed,
		},
	}
	for name, bad := range cases {
		if err := Verify(g, bad); err == nil {
			t.Errorf("%s: Verify accepted tampered violation:\n%s", name, bad)
		}
	}
	if err := Verify(g, nil); err == nil {
		t.Error("Verify accepted nil violation")
	}
}

func TestVerifyRejectsFiniteClaimOnNonTerminal(t *testing.T) {
	// A finite "violation" whose last state has outgoing edges must fail.
	g := mustGraph(t, "s0", []Edge{
		{From: "s0", To: "s1", Action: req("a")},
		{From: "s1", To: "s0", Action: resp("a")},
	})
	bad := &Violation{
		Prefix: []Step{{From: "s0", To: "s1", Action: req("a")}},
		Owed:   []string{"a"},
	}
	if err := Verify(g, bad); err == nil {
		t.Fatal("Verify accepted finite violation ending in non-terminal state")
	}
}

func TestViolationString(t *testing.T) {
	_, v := lassoFixture(t)
	s := v.String()
	if !strings.Contains(s, "owed=[a]") || !strings.Contains(s, "cycle") {
		t.Fatalf("unexpected String output: %s", s)
	}
	finite := &Violation{Prefix: v.Prefix, Owed: []string{"a"}}
	if !strings.Contains(finite.String(), "terminal") {
		t.Fatalf("unexpected String output: %s", finite.String())
	}
}
