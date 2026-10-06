package poset

import (
	"testing"
)

func mustAdd(t *testing.T, c *Case, id EventID, label string) {
	t.Helper()
	if err := c.AddEvent(id, label); err != nil {
		t.Fatalf("AddEvent %s: %v", id, err)
	}
}

func mustEdge(t *testing.T, c *Case, before, after EventID) {
	t.Helper()
	if err := c.AddPrecedence(before, after); err != nil {
		t.Fatalf("AddPrecedence %s -> %s: %v", before, after, err)
	}
}

// A time/ID-sorted scan would report "no match": the witness only exists
// because concurrent events may be interleaved against the sort order.
func TestMatchBeyondSortedOrder(t *testing.T) {
	c := NewCase("unsorted")
	mustAdd(t, c, "e1", "a")
	mustAdd(t, c, "e2", "b")
	mustAdd(t, c, "e3", "c")

	snap, err := c.Match([]string{"c", "a"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !snap.Matched() {
		t.Fatalf("pattern c,a must match some interleaving of concurrent events")
	}
	for _, m := range snap.Matches {
		if got := []string{labelOf(t, c, m.Witness[m.WindowStart]), labelOf(t, c, m.Witness[m.WindowStart+1])}; got[0] != "c" || got[1] != "a" {
			t.Fatalf("witness window labels = %v", got)
		}
	}
}

func labelOf(t *testing.T, c *Case, id EventID) string {
	t.Helper()
	for _, e := range c.Events() {
		if e.ID == id {
			return e.Label
		}
	}
	t.Fatalf("unknown event %s", id)
	return ""
}

// Duplicate labels: the pattern only matches when the two x-labeled events
// flank y, and the witness must respect the precedence constraints.
func TestMatchWithDuplicateLabels(t *testing.T) {
	c := NewCase("dup-labels")
	mustAdd(t, c, "p", "x")
	mustAdd(t, c, "q", "x")
	mustAdd(t, c, "r", "y")
	mustEdge(t, c, "p", "r") // p before r
	mustEdge(t, c, "r", "q") // r before q

	snap, err := c.Match([]string{"x", "y", "x"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(snap.Matches) != 1 {
		t.Fatalf("expected exactly 1 match, got %d", len(snap.Matches))
	}
	m := snap.Matches[0]
	want := []EventID{"p", "r", "q"}
	for i, id := range want {
		if m.Witness[i] != id {
			t.Fatalf("witness = %v, want %v", m.Witness, want)
		}
	}
	if m.WindowStart != 0 {
		t.Fatalf("window start = %d, want 0", m.WindowStart)
	}
}

// Two parallel chains a<b and c<d have exactly 6 linear extensions.
// d can never immediately precede c, so the search closes with no match
// and the full range of 6 extensions is retained as evidence.
func TestNoMatchClosedSearchRange(t *testing.T) {
	c := NewCase("parallel")
	mustAdd(t, c, "a", "a")
	mustAdd(t, c, "b", "b")
	mustAdd(t, c, "c", "c")
	mustAdd(t, c, "d", "d")
	mustEdge(t, c, "a", "b")
	mustEdge(t, c, "c", "d")

	snap, err := c.Match([]string{"d", "c"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if snap.Matched() {
		t.Fatalf("d,c can never be adjacent: c always precedes d")
	}
	if !snap.Search.Closed || snap.Search.Truncated {
		t.Fatalf("search range must be closed, got %+v", snap.Search)
	}
	if snap.Search.Explored != 6 {
		t.Fatalf("explored = %d, want 6 linear extensions", snap.Search.Explored)
	}
	if len(snap.Search.Extensions) != 6 {
		t.Fatalf("retained %d extensions, want the full closed range of 6", len(snap.Search.Extensions))
	}
	for _, ext := range snap.Search.Extensions {
		assertTopological(t, c, ext)
	}
}

func TestMatchFindsInterleavingOfConcurrentChains(t *testing.T) {
	c := NewCase("parallel")
	mustAdd(t, c, "a", "a")
	mustAdd(t, c, "b", "b")
	mustAdd(t, c, "c", "c")
	mustAdd(t, c, "d", "d")
	mustEdge(t, c, "a", "b")
	mustEdge(t, c, "c", "d")

	snap, err := c.Match([]string{"d", "a"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !snap.Matched() {
		t.Fatalf("d,a must match via interleaving c d a b")
	}
	for _, m := range snap.Matches {
		assertTopological(t, c, m.Witness)
		if labelOf(t, c, m.Witness[m.WindowStart]) != "d" || labelOf(t, c, m.Witness[m.WindowStart+1]) != "a" {
			t.Fatalf("bad witness window in %v at %d", m.Witness, m.WindowStart)
		}
	}
}

func TestPatternLongerThanCase(t *testing.T) {
	c := buildChain(t)
	snap, err := c.Match([]string{"x", "y", "z", "w"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if snap.Matched() {
		t.Fatalf("pattern longer than the case must not match")
	}
	if !snap.Search.Closed {
		t.Fatalf("search range must still close")
	}
}

func TestEmptyPatternRejected(t *testing.T) {
	c := buildChain(t)
	if _, err := c.Match(nil); err != ErrEmptyPattern {
		t.Fatalf("expected ErrEmptyPattern, got %v", err)
	}
}

func TestEveryWitnessIsTopologicalOrder(t *testing.T) {
	c := NewCase("mixed")
	mustAdd(t, c, "a", "x")
	mustAdd(t, c, "b", "y")
	mustAdd(t, c, "d", "x")
	mustAdd(t, c, "e", "z")
	mustEdge(t, c, "a", "b")
	mustEdge(t, c, "d", "e")

	snap, err := c.Match([]string{"x"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !snap.Matched() {
		t.Fatalf("expected matches")
	}
	for _, m := range snap.Matches {
		assertTopological(t, c, m.Witness)
	}
}

func assertTopological(t *testing.T, c *Case, ext []EventID) {
	t.Helper()
	pos := make(map[EventID]int, len(ext))
	for i, id := range ext {
		pos[id] = i
	}
	if len(pos) != len(c.Events()) {
		t.Fatalf("extension %v does not cover all events", ext)
	}
	for _, e := range c.Edges() {
		if pos[e.Before] >= pos[e.After] {
			t.Fatalf("extension %v violates %s -> %s", ext, e.Before, e.After)
		}
	}
}
