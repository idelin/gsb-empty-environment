package livecheck

import (
	"strings"
	"testing"
)

func req(id string) Action      { return Action{Kind: Request, ID: id} }
func resp(id string) Action     { return Action{Kind: Response, ID: id} }
func other(label string) Action { return Action{Kind: Other, ID: label} }

func mustHold(t *testing.T, g *Graph) {
	t.Helper()
	ce, err := Check(g)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if ce != nil {
		t.Fatalf("expected property to hold, got counterexample:\n%s", ce)
	}
}

func mustViolate(t *testing.T, g *Graph) *Counterexample {
	t.Helper()
	ce, err := Check(g)
	if err != nil {
		t.Fatalf("Check error: %v", err)
	}
	if ce == nil {
		t.Fatalf("expected a violation, property held")
	}
	if err := Verify(g, ce); err != nil {
		t.Fatalf("returned counterexample fails independent replay: %v", err)
	}
	return ce
}

func TestRequestAnsweredInLoopHolds(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s0", resp("a"))
	mustHold(t, g)
}

func TestFiniteRunFullyAnsweredHolds(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s2", resp("a"))
	mustHold(t, g)
}

func TestSeveralIDsAllAnsweredHolds(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s2", req("b"))
	g.AddEdge("s2", "s3", resp("a"))
	g.AddEdge("s3", "s0", resp("b"))
	mustHold(t, g)
}

func TestIdleLoopWithoutRequestHolds(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s0", other("tick"))
	mustHold(t, g)
}

func TestInfiniteRunOwingResponseForever(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s1", other("tick"))
	ce := mustViolate(t, g)
	if ce.Terminal {
		t.Fatalf("expected lasso, got terminal violation")
	}
	if ce.PendingID != "a" {
		t.Fatalf("expected owed id %q, got %q", "a", ce.PendingID)
	}
	if len(ce.Prefix) != 1 || len(ce.Cycle) != 1 {
		t.Fatalf("expected 1-step prefix and 1-step cycle, got:\n%s", ce)
	}
}

func TestTerminalStateOwingResponseFails(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	ce := mustViolate(t, g)
	if !ce.Terminal {
		t.Fatalf("expected terminal violation, got lasso:\n%s", ce)
	}
	if ce.PendingID != "a" {
		t.Fatalf("expected owed id %q, got %q", "a", ce.PendingID)
	}
	if len(ce.Cycle) != 0 {
		t.Fatalf("terminal violation must not carry a cycle")
	}
}

func TestMultipleRequestsTrackedByID(t *testing.T) {
	// a is answered, b is not; the loop keeps running forever.
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s2", req("b"))
	g.AddEdge("s2", "s3", resp("a"))
	g.AddEdge("s3", "s3", other("tick"))
	ce := mustViolate(t, g)
	if ce.PendingID != "b" {
		t.Fatalf("expected owed id %q, got %q", "b", ce.PendingID)
	}
}

func TestCycleMayAnswerOtherIDs(t *testing.T) {
	// The loop answers a fresh "x" request on every iteration but never
	// answers the "b" request raised in the prefix.
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("b"))
	g.AddEdge("s1", "s2", req("x"))
	g.AddEdge("s2", "s1", resp("x"))
	ce := mustViolate(t, g)
	if ce.PendingID != "b" {
		t.Fatalf("expected owed id %q, got %q", "b", ce.PendingID)
	}
	if err := Verify(g, ce); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestReRequestingInsideCycleStillOwes(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s0", req("a"))
	ce := mustViolate(t, g)
	if ce.PendingID != "a" {
		t.Fatalf("expected owed id %q, got %q", "a", ce.PendingID)
	}
}

func TestResponseChoiceAvoidsViolation(t *testing.T) {
	// The graph may ignore the request forever, but it may also answer
	// it; since the ignoring loop exists, the property must fail.
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s1", other("tick"))
	g.AddEdge("s1", "s2", resp("a"))
	g.AddEdge("s2", "s2", other("tick"))
	ce := mustViolate(t, g)
	if ce.Terminal {
		t.Fatalf("expected lasso, got terminal violation")
	}
}

func TestVerifyRejectsTamperedCounterexample(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s1", other("tick"))
	ce := mustViolate(t, g)

	cases := map[string]func(*Counterexample){
		"wrong owed id": func(c *Counterexample) { c.PendingID = "zzz" },
		"broken prefix edge": func(c *Counterexample) {
			c.Prefix[0].Action = other("tick")
		},
		"broken cycle target": func(c *Counterexample) {
			c.Cycle[0].To = "nowhere"
		},
		"empty cycle": func(c *Counterexample) { c.Cycle = nil },
		"detached prefix": func(c *Counterexample) {
			c.Prefix = append([]Step{{From: "s1", To: "s1", Action: other("tick")}}, c.Prefix...)
		},
	}
	for name, mutate := range cases {
		cp := *ce
		cp.Prefix = append([]Step(nil), ce.Prefix...)
		cp.Cycle = append([]Step(nil), ce.Cycle...)
		mutate(&cp)
		if err := Verify(g, &cp); err == nil {
			t.Errorf("tampered counterexample (%s) passed replay", name)
		}
	}
}

func TestVerifyRejectsCycleAnsweringOwedID(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s1", other("tick"))
	g.AddEdge("s1", "s1", resp("a"))
	// Hand-crafted bogus lasso: the cycle answers the id it claims is
	// owed forever.
	bogus := &Counterexample{
		Prefix:    []Step{{From: "s0", To: "s1", Action: req("a")}},
		Cycle:     []Step{{From: "s1", To: "s1", Action: resp("a")}},
		PendingID: "a",
	}
	if err := Verify(g, bogus); err == nil {
		t.Fatalf("replay accepted a cycle that answers the owed id")
	}
}

func TestVerifyRejectsFalseTerminalClaim(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s1", other("tick"))
	bogus := &Counterexample{
		Prefix:    []Step{{From: "s0", To: "s1", Action: req("a")}},
		PendingID: "a",
		Terminal:  true,
	}
	if err := Verify(g, bogus); err == nil {
		t.Fatalf("replay accepted terminal claim for non-terminal state")
	}
}

func TestCounterexampleString(t *testing.T) {
	g := NewGraph("s0")
	g.AddEdge("s0", "s1", req("a"))
	g.AddEdge("s1", "s1", other("tick"))
	ce := mustViolate(t, g)
	out := ce.String()
	for _, want := range []string{"prefix:", "cycle", "request(a)", `"a"`} {
		if !strings.Contains(out, want) {
			t.Errorf("counterexample text missing %q:\n%s", want, out)
		}
	}
}
