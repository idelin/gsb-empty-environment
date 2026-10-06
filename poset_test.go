package poset

import (
	"errors"
	"strings"
	"testing"
)

func buildChain(t *testing.T) *Case {
	t.Helper()
	c := NewCase("chain")
	for _, e := range []Event{{"a", "x"}, {"b", "y"}, {"c", "z"}} {
		if err := c.AddEvent(e.ID, e.Label); err != nil {
			t.Fatalf("AddEvent: %v", err)
		}
	}
	if err := c.AddPrecedence("a", "b"); err != nil {
		t.Fatalf("AddPrecedence: %v", err)
	}
	return c
}

func TestAddPrecedenceRejectsCycleAtomically(t *testing.T) {
	c := buildChain(t)
	if err := c.AddPrecedence("b", "c"); err != nil {
		t.Fatalf("AddPrecedence: %v", err)
	}
	snap, err := c.Match([]string{"x", "y", "z"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !snap.Valid || !snap.Matched() {
		t.Fatalf("expected one valid match, got %+v", snap)
	}
	versionBefore := c.Version()
	historyBefore := len(c.History())

	err = c.AddPrecedence("c", "a")
	var cyc *CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("expected CycleError, got %v", err)
	}
	if got := strings.Join([]string{string(cyc.Path[0]), string(cyc.Path[len(cyc.Path)-1])}, ","); got != "c,c" {
		t.Fatalf("cycle path should start and end at c, got %v", cyc.Path)
	}

	// Atomic rejection: nothing changed, old evidence still valid.
	if c.Version() != versionBefore {
		t.Fatalf("version changed on rejected addition: %d -> %d", versionBefore, c.Version())
	}
	if len(c.History()) != historyBefore {
		t.Fatalf("history changed on rejected addition")
	}
	cur, ok := c.Current()
	if !ok || !cur.Valid {
		t.Fatalf("existing snapshot lost validity after rejected addition")
	}
	if len(c.Edges()) != 2 {
		t.Fatalf("edge set changed on rejected addition: %v", c.Edges())
	}
}

func TestSelfLoopRejected(t *testing.T) {
	c := buildChain(t)
	var cyc *CycleError
	if err := c.AddPrecedence("a", "a"); !errors.As(err, &cyc) {
		t.Fatalf("expected CycleError for self loop, got %v", err)
	}
}

func TestRetractEventInvalidatesAndRetainsEvidence(t *testing.T) {
	c := buildChain(t)
	if err := c.AddPrecedence("b", "c"); err != nil {
		t.Fatalf("AddPrecedence: %v", err)
	}
	snap, err := c.Match([]string{"y", "z"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !snap.Matched() {
		t.Fatalf("expected match before retraction")
	}

	if err := c.RetractEvent("c"); err != nil {
		t.Fatalf("RetractEvent: %v", err)
	}

	// Old evidence retained but invalidated with a reason.
	hist := c.History()
	if len(hist) != 1 {
		t.Fatalf("expected 1 retained snapshot, got %d", len(hist))
	}
	old := hist[0]
	if old.Valid {
		t.Fatalf("old snapshot still valid after retraction")
	}
	if !strings.Contains(old.InvalidatedBy, "retracted") {
		t.Fatalf("invalidation reason missing: %q", old.InvalidatedBy)
	}
	if !old.Matched() || old.Version != snap.Version {
		t.Fatalf("old-version evidence lost: %+v", old)
	}
	if _, ok := c.Current(); ok {
		t.Fatalf("no valid snapshot should remain")
	}

	// Recomputed matches reflect the retracted event.
	next, err := c.Match([]string{"y", "z"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if next.Matched() {
		t.Fatalf("pattern must not match after retracting z-event")
	}
	if next.Version != c.Version() {
		t.Fatalf("snapshot version %d, case version %d", next.Version, c.Version())
	}
}

func TestRemovePrecedenceInvalidates(t *testing.T) {
	c := buildChain(t)
	snap, err := c.Match([]string{"y", "x"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if snap.Matched() {
		t.Fatalf("y,x must not match while a -> b holds")
	}
	if err := c.RemovePrecedence("a", "b"); err != nil {
		t.Fatalf("RemovePrecedence: %v", err)
	}
	if c.History()[0].Valid {
		t.Fatalf("snapshot should be invalidated by precedence removal")
	}
	next, err := c.Match([]string{"y", "x"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !next.Matched() {
		t.Fatalf("y,x must match once a and b are concurrent")
	}
	if err := c.RemovePrecedence("a", "b"); !errors.Is(err, ErrEdgeMissing) {
		t.Fatalf("expected ErrEdgeMissing, got %v", err)
	}
}

func TestDuplicateAndMissingEvents(t *testing.T) {
	c := NewCase("dup")
	if err := c.AddEvent("a", "x"); err != nil {
		t.Fatalf("AddEvent: %v", err)
	}
	if err := c.AddEvent("a", "y"); !errors.Is(err, ErrEventExists) {
		t.Fatalf("expected ErrEventExists, got %v", err)
	}
	if err := c.RetractEvent("ghost"); !errors.Is(err, ErrEventMissing) {
		t.Fatalf("expected ErrEventMissing, got %v", err)
	}
	if err := c.AddPrecedence("a", "ghost"); !errors.Is(err, ErrEventMissing) {
		t.Fatalf("expected ErrEventMissing, got %v", err)
	}
}
