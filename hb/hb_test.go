package hb

import (
	"reflect"
	"strings"
	"testing"
)

func mustAdd(t *testing.T, g *Graph, e Event) {
	t.Helper()
	if _, err := g.Add(e); err != nil {
		t.Fatalf("Add(%s): %v", e.ID, err)
	}
}

func ids(evs []Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}

// Two nodes exchanging messages, imported in scrambled order (recv before
// its send). The partial order and Lamport clocks must not depend on the
// import order nor on the (deliberately misleading) wall-clock values.
func TestOutOfOrderImport(t *testing.T) {
	events := []Event{
		{ID: "b2", Node: "B", Seq: 2, Kind: Send, Wall: 1},
		{ID: "a2", Node: "A", Seq: 2, Kind: Recv, SendID: "b2", Wall: 2},
		{ID: "b1", Node: "B", Seq: 1, Kind: Recv, SendID: "a1", Wall: 5},
		{ID: "a1", Node: "A", Seq: 1, Kind: Send, Wall: 9},
		{ID: "a3", Node: "A", Seq: 3, Kind: Local, Wall: 3},
	}
	g := New()
	for _, e := range events {
		mustAdd(t, g, e)
	}
	if errs := g.Validate(); len(errs) != 0 {
		t.Fatalf("Validate: %v", errs)
	}

	order, err := g.TopoSort()
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	want := []string{"a1", "b1", "b2", "a2", "a3"}
	if got := ids(order); !reflect.DeepEqual(got, want) {
		t.Fatalf("topo order = %v, want %v", got, want)
	}

	clock, err := g.LogicalClocks()
	if err != nil {
		t.Fatalf("LogicalClocks: %v", err)
	}
	wantClock := map[string]int{"a1": 1, "b1": 2, "b2": 3, "a2": 4, "a3": 5}
	if !reflect.DeepEqual(clock, wantClock) {
		t.Fatalf("clocks = %v, want %v", clock, wantClock)
	}
}

// Wall-clock timestamps must never influence the causal order: here the
// wall clock of the recv is *earlier* than its send, yet send -> recv
// must hold and clocks must follow causality.
func TestWallClockIgnored(t *testing.T) {
	g := New()
	mustAdd(t, g, Event{ID: "s", Node: "A", Seq: 1, Kind: Send, Wall: 1000})
	mustAdd(t, g, Event{ID: "r", Node: "B", Seq: 1, Kind: Recv, SendID: "s", Wall: 1})
	order, err := g.TopoSort()
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}
	if got := ids(order); !reflect.DeepEqual(got, []string{"s", "r"}) {
		t.Fatalf("order = %v, want [s r]", got)
	}
	clock, _ := g.LogicalClocks()
	if clock["s"] != 1 || clock["r"] != 2 {
		t.Fatalf("clocks = %v, want s=1 r=2", clock)
	}
}

// Identical re-import merges; contradictory re-import is a conflict.
func TestDuplicateMergeAndConflict(t *testing.T) {
	g := New()
	e := Event{ID: "e1", Node: "A", Seq: 1, Kind: Local, Wall: 7, Data: "x"}
	merged, err := g.Add(e)
	if err != nil || merged {
		t.Fatalf("first Add: merged=%v err=%v", merged, err)
	}
	merged, err = g.Add(e)
	if err != nil || !merged {
		t.Fatalf("duplicate Add: merged=%v err=%v", merged, err)
	}
	if g.Len() != 1 {
		t.Fatalf("Len = %d, want 1", g.Len())
	}

	_, err = g.Add(Event{ID: "e1", Node: "A", Seq: 1, Kind: Send})
	if _, ok := err.(*ConflictError); !ok {
		t.Fatalf("same-ID conflict: got %v, want *ConflictError", err)
	}

	_, err = g.Add(Event{ID: "e2", Node: "A", Seq: 1, Kind: Local})
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("slot conflict: got %v, want *ConflictError", err)
	}
	if !strings.Contains(ce.Reason, "e1") {
		t.Fatalf("slot conflict should name the holder, got %q", ce.Reason)
	}
}

// A message link that contradicts local order must surface as a cycle
// with the full evidence path and per-edge reasons.
func TestContradictoryLinkCycleEvidence(t *testing.T) {
	g := New()
	// A: recv of m2 happens locally before send of m1.
	mustAdd(t, g, Event{ID: "a1", Node: "A", Seq: 1, Kind: Recv, SendID: "b2"})
	mustAdd(t, g, Event{ID: "a2", Node: "A", Seq: 2, Kind: Send})
	// B: recv of m1 (a2) happens locally before send of m2 (b2).
	mustAdd(t, g, Event{ID: "b1", Node: "B", Seq: 1, Kind: Recv, SendID: "a2"})
	mustAdd(t, g, Event{ID: "b2", Node: "B", Seq: 2, Kind: Send})

	_, err := g.TopoSort()
	cyc, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("TopoSort err = %v, want *CycleError", err)
	}
	if len(cyc.Path) < 2 || cyc.Path[0] != cyc.Path[len(cyc.Path)-1] {
		t.Fatalf("cycle path %v is not closed", cyc.Path)
	}
	if len(cyc.Why) != len(cyc.Path)-1 {
		t.Fatalf("got %d reasons for %d path nodes", len(cyc.Why), len(cyc.Path))
	}
	msg := cyc.Error()
	for _, want := range []string{"local order", "message"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("cycle evidence missing %q:\n%s", want, msg)
		}
	}

	if _, err := g.LogicalClocks(); err == nil {
		t.Fatal("LogicalClocks on cyclic graph should fail")
	}
	errs := g.Validate()
	foundCycle := false
	for _, e := range errs {
		if _, ok := e.(*CycleError); ok {
			foundCycle = true
		}
	}
	if !foundCycle {
		t.Fatalf("Validate should report the cycle, got %v", errs)
	}
}

// Insertion order must not change the topological order or the clocks.
func TestDeterministicRegardlessOfImportOrder(t *testing.T) {
	base := []Event{
		{ID: "a1", Node: "A", Seq: 1, Kind: Send},
		{ID: "a2", Node: "A", Seq: 2, Kind: Local},
		{ID: "b1", Node: "B", Seq: 1, Kind: Recv, SendID: "a1"},
		{ID: "c1", Node: "C", Seq: 1, Kind: Recv, SendID: "a1"}, // broadcast: relax single-consumer below
		{ID: "b2", Node: "B", Seq: 2, Kind: Send},
		{ID: "c2", Node: "C", Seq: 2, Kind: Recv, SendID: "b2"},
	}
	build := func(evs []Event) ([]string, map[string]int) {
		g := New()
		for _, e := range evs {
			mustAdd(t, g, e)
		}
		order, err := g.TopoSort()
		if err != nil {
			t.Fatalf("TopoSort: %v", err)
		}
		clock, err := g.LogicalClocks()
		if err != nil {
			t.Fatalf("LogicalClocks: %v", err)
		}
		return ids(order), clock
	}

	fwdOrder, fwdClock := build(base)
	rev := make([]Event, len(base))
	for i, e := range base {
		rev[len(base)-1-i] = e
	}
	revOrder, revClock := build(rev)

	if !reflect.DeepEqual(fwdOrder, revOrder) {
		t.Fatalf("order differs by import order: %v vs %v", fwdOrder, revOrder)
	}
	if !reflect.DeepEqual(fwdClock, revClock) {
		t.Fatalf("clocks differ by import order: %v vs %v", fwdClock, revClock)
	}
}

func TestValidateInvariants(t *testing.T) {
	g := New()
	mustAdd(t, g, Event{ID: "a1", Node: "A", Seq: 1, Kind: Send})
	mustAdd(t, g, Event{ID: "a3", Node: "A", Seq: 3, Kind: Local}) // gap: seq 2 missing
	mustAdd(t, g, Event{ID: "b1", Node: "B", Seq: 1, Kind: Recv, SendID: "a1"})
	mustAdd(t, g, Event{ID: "c1", Node: "C", Seq: 1, Kind: Recv, SendID: "a1"}) // double consumer
	mustAdd(t, g, Event{ID: "d1", Node: "D", Seq: 1, Kind: Recv, SendID: "zz"}) // dangling
	mustAdd(t, g, Event{ID: "e1", Node: "E", Seq: 1, Kind: Recv, SendID: "b1"}) // target not a send
	mustAdd(t, g, Event{ID: "f1", Node: "F", Seq: 1, Kind: Recv})               // missing SendID

	errs := g.Validate()
	rules := make(map[string]int)
	for _, e := range errs {
		if ie, ok := e.(*InvariantError); ok {
			rules[ie.Rule]++
		}
	}
	for _, rule := range []string{"kind-fields", "link-target", "single-consumer", "local-contiguity"} {
		if rules[rule] == 0 {
			t.Fatalf("missing invariant violation %q in %v", rule, errs)
		}
	}
}

// Lamport clock on a diamond: concurrent branches get independent clocks.
func TestLamportOnDiamond(t *testing.T) {
	g := New()
	mustAdd(t, g, Event{ID: "a1", Node: "A", Seq: 1, Kind: Send})
	mustAdd(t, g, Event{ID: "b1", Node: "B", Seq: 1, Kind: Recv, SendID: "a1"})
	mustAdd(t, g, Event{ID: "c1", Node: "C", Seq: 1, Kind: Local})
	mustAdd(t, g, Event{ID: "c2", Node: "C", Seq: 2, Kind: Local})
	clock, err := g.LogicalClocks()
	if err != nil {
		t.Fatalf("LogicalClocks: %v", err)
	}
	want := map[string]int{"a1": 1, "b1": 2, "c1": 1, "c2": 2}
	if !reflect.DeepEqual(clock, want) {
		t.Fatalf("clocks = %v, want %v", clock, want)
	}
}
