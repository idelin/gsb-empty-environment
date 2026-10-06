package posetmatch

import (
	"testing"
)

func newInvCase(t *testing.T) *Case {
	t.Helper()
	c := NewCase("inv")
	for _, ev := range []Event{
		{ID: "1", Label: "a", Time: 1}, {ID: "2", Label: "b", Time: 2},
		{ID: "3", Label: "c", Time: 3}, {ID: "4", Label: "d", Time: 4},
	} {
		if err := c.AddEvent(ev.ID, ev.Label, ev.Time); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.AddPrecedence("1", "4"); err != nil {
		t.Fatal(err)
	}
	return c
}

// A cycle-forming relation must be rejected atomically: no edge, no version
// bump, no evidence disturbed.
func TestCycleAtomicReject(t *testing.T) {
	c := NewCase("cyc")
	for _, ev := range []Event{{ID: "a", Label: "α"}, {ID: "b", Label: "β"}, {ID: "c", Label: "γ"}} {
		if err := c.AddEvent(ev.ID, ev.Label, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.AddPrecedence("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPrecedence("b", "c"); err != nil {
		t.Fatal(err)
	}
	ev := c.Match([]Label{"α", "β", "γ"})
	if !ev.Found {
		t.Fatal("chain labels should match")
	}
	v := c.Version()

	err := c.AddPrecedence("c", "a")
	ce, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("expected *CycleError, got %v", err)
	}
	if len(ce.Path) == 0 || ce.Path[0] != "a" || ce.Path[len(ce.Path)-1] != "c" {
		t.Errorf("cycle path should run a..c, got %v", ce.Path)
	}
	if c.Version() != v {
		t.Error("rejected relation must not advance the version")
	}
	if c.HasEdge("c", "a") {
		t.Error("rejected relation must not be applied")
	}
	if !ev.Valid {
		t.Error("rejected relation must not invalidate evidence")
	}
	if got := c.Match([]Label{"α", "β", "γ"}); got != ev {
		t.Error("cached evidence should be reused after a rejected mutation")
	}
}

// A batch of relations is all-or-nothing, including cycles formed only
// inside the batch.
func TestBatchAtomic(t *testing.T) {
	c := NewCase("batch")
	for _, id := range []EventID{"x", "y", "z"} {
		if err := c.AddEvent(id, "L", 0); err != nil {
			t.Fatal(err)
		}
	}
	v := c.Version()
	err := c.AddPrecedences([][2]EventID{{"x", "y"}, {"y", "z"}, {"z", "x"}})
	if _, ok := err.(*CycleError); !ok {
		t.Fatalf("expected *CycleError, got %v", err)
	}
	if c.Version() != v {
		t.Error("failed batch must not advance the version")
	}
	for _, e := range [][2]EventID{{"x", "y"}, {"y", "z"}, {"z", "x"}} {
		if c.HasEdge(e[0], e[1]) {
			t.Errorf("failed batch must apply nothing, found %s -> %s", e[0], e[1])
		}
	}

	if err := c.AddPrecedences([][2]EventID{{"x", "y"}, {"y", "z"}}); err != nil {
		t.Fatal(err)
	}
	if c.Version() != v+1 {
		t.Error("successful batch should bump the version exactly once")
	}
	if !c.HasEdge("x", "y") || !c.HasEdge("y", "z") {
		t.Error("successful batch should apply every relation")
	}

	// Re-adding an existing relation is an idempotent no-op.
	if err := c.AddPrecedence("x", "y"); err != nil {
		t.Fatal(err)
	}
	if c.Version() != v+1 {
		t.Error("duplicate relation must not advance the version")
	}
}

// Mutations must invalidate exactly the evidence whose conclusion no longer
// holds, while refuted records stay in the log as old-version evidence.
func TestIncrementalInvalidation(t *testing.T) {
	c := newInvCase(t)

	pos := c.Match([]Label{"a", "b", "c"})
	if !pos.Found {
		t.Fatal("a,b,c should match with witness 1,2,3,4")
	}
	neg := c.Match([]Label{"d", "a"})
	if neg.Found {
		t.Fatal("d before a contradicts 1<4")
	}
	if neg.Report == nil || !neg.Report.Complete || neg.Report.ExtensionsTotal != 12 {
		t.Errorf("bad closed search range: %+v", neg.Report)
	}
	if got := c.Match([]Label{"a", "b", "c"}); got != pos {
		t.Fatal("valid cached evidence should be returned as-is")
	}
	if len(c.Evidence()) != 2 {
		t.Fatalf("cache hit must not append evidence, log = %d", len(c.Evidence()))
	}

	// New relation 3<2 cuts the witness 1,2,3,4: positive evidence dies,
	// negative evidence survives because relations only shrink the space.
	if err := c.AddPrecedence("3", "2"); err != nil {
		t.Fatal(err)
	}
	if pos.Valid {
		t.Error("witness violating the new relation must be invalidated")
	}
	if pos.InvalidatedAt != c.Version() {
		t.Errorf("InvalidatedAt = %d, want %d", pos.InvalidatedAt, c.Version())
	}
	if !neg.Valid {
		t.Error("no-match evidence must survive relation addition")
	}

	// Re-querying recomputes under the new constraints.
	pos2 := c.Match([]Label{"a", "b", "c"})
	if pos2.Found {
		t.Fatal("a,b,c is impossible once 3<2 holds")
	}

	// Retraction relaxes constraints: every no-match conclusion is dropped.
	if err := c.RetractEvent("4"); err != nil {
		t.Fatal(err)
	}
	if neg.Valid || pos2.Valid {
		t.Error("retraction must invalidate negative evidence")
	}

	// A fresh positive match on the remaining events.
	pos3 := c.Match([]Label{"c", "b", "a"})
	if !pos3.Found {
		t.Fatal("c,b,a should match on events 1,2,3 with 3<2")
	}

	// Isolated addition keeps positive evidence alive...
	if err := c.AddEvent("5", "x", 5); err != nil {
		t.Fatal(err)
	}
	if !pos3.Valid {
		t.Error("adding an isolated event must not invalidate positive evidence")
	}
	// ...and retracting a non-segment event keeps it alive too.
	if err := c.RetractEvent("5"); err != nil {
		t.Fatal(err)
	}
	if !pos3.Valid {
		t.Error("retracting a non-segment event must not invalidate the match")
	}

	// Retracting a segment event kills the match.
	if err := c.RetractEvent("2"); err != nil {
		t.Fatal(err)
	}
	if pos3.Valid {
		t.Error("retracting a segment event must invalidate the match")
	}

	// The log retains every record, refuted or not.
	if len(c.Evidence()) != 4 {
		t.Fatalf("evidence log should retain all 4 records, got %d", len(c.Evidence()))
	}
	if len(c.ValidEvidence()) != 0 {
		t.Error("no evidence should remain valid at the final version")
	}
	for _, ev := range c.Evidence() {
		if !ev.Valid && ev.InvalidatedAt == 0 {
			t.Errorf("refuted evidence for %v lacks InvalidatedAt", ev.Pattern)
		}
	}
}

// Adding an event may enable previously impossible matches, so negative
// evidence must be dropped conservatively.
func TestAddEventInvalidatesNoMatch(t *testing.T) {
	c := NewCase("grow")
	if err := c.AddEvent("1", "a", 1); err != nil {
		t.Fatal(err)
	}
	neg := c.Match([]Label{"b"})
	if neg.Found || !neg.Valid {
		t.Fatal("b should not match a singleton a-case")
	}
	if err := c.AddEvent("2", "b", 2); err != nil {
		t.Fatal(err)
	}
	if neg.Valid {
		t.Error("new event must invalidate negative evidence")
	}
	pos := c.Match([]Label{"b"})
	if !pos.Found {
		t.Fatal("b should match after the event was added")
	}
	if len(c.Evidence()) != 2 {
		t.Fatalf("log should hold the refuted and the fresh record, got %d", len(c.Evidence()))
	}
}

// Errors on unknown endpoints must leave the case untouched.
func TestMutationErrors(t *testing.T) {
	c := newInvCase(t)
	v := c.Version()
	if err := c.AddPrecedence("1", "ghost"); err == nil {
		t.Error("unknown endpoint must be rejected")
	}
	if err := c.AddPrecedence("1", "1"); err == nil {
		t.Error("self precedence must be rejected")
	}
	if err := c.RetractEvent("ghost"); err == nil {
		t.Error("retracting a missing event must be rejected")
	}
	if err := c.AddEvent("1", "z", 9); err == nil {
		t.Error("duplicate event must be rejected")
	}
	if c.Version() != v {
		t.Error("failed mutations must not advance the version")
	}
}

// The analyzer fans a query out to independent cases.
func TestAnalyzer(t *testing.T) {
	a := NewAnalyzer()
	c1 := NewCase("c1")
	c2 := NewCase("c2")
	for _, c := range []*Case{c1, c2} {
		if err := c.AddEvent("p", "a", 1); err != nil {
			t.Fatal(err)
		}
		if err := c.AddEvent("q", "b", 2); err != nil {
			t.Fatal(err)
		}
		if err := a.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := c1.AddPrecedence("p", "q"); err != nil {
		t.Fatal(err)
	}
	if err := c2.AddPrecedence("q", "p"); err != nil {
		t.Fatal(err)
	}
	if err := a.Add(NewCase("c1")); err == nil {
		t.Error("duplicate case name must be rejected")
	}

	res := a.AnalyzeAll([]Label{"a", "b"})
	if !res["c1"].Found {
		t.Error("c1 should match a,b")
	}
	if res["c2"].Found {
		t.Error("c2 should not match a,b")
	}
	if res["c2"].Report == nil || !res["c2"].Report.Complete {
		t.Error("c2 should report a closed search range")
	}

	// Mutating one case leaves the other's evidence and version untouched.
	v1 := c1.Version()
	if err := c2.RetractEvent("q"); err != nil {
		t.Fatal(err)
	}
	if c1.Version() != v1 || !res["c1"].Valid {
		t.Error("cases must be independent")
	}
	if res["c2"].Valid {
		t.Error("c2's no-match evidence should be invalidated by retraction")
	}
}
