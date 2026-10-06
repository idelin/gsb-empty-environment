package rules

import (
	"reflect"
	"testing"
)

// applyAll feeds ops and flattens the emitted deltas.
func applyAll(e *Engine, ops ...Op) []Delta {
	var out []Delta
	for _, op := range ops {
		out = append(out, e.Apply(op)...)
	}
	return out
}

func TestPriorityThenSeqOrdering(t *testing.T) {
	e := NewEngine()
	deltas := applyAll(e,
		Add(Event{ObjectID: "o1", EventID: "e1", Priority: 1, Seq: 10, Payload: "low-prio-newer"}),
		Add(Event{ObjectID: "o1", EventID: "e2", Priority: 5, Seq: 1, Payload: "high-prio-older"}),
		Add(Event{ObjectID: "o1", EventID: "e3", Priority: 5, Seq: 2, Payload: "high-prio-newer"}),
	)
	want := []Delta{
		{ObjectID: "o1", NewPayload: "low-prio-newer", HasNew: true},
		{ObjectID: "o1", OldPayload: "low-prio-newer", NewPayload: "high-prio-older", HadOld: true, HasNew: true},
		{ObjectID: "o1", OldPayload: "high-prio-older", NewPayload: "high-prio-newer", HadOld: true, HasNew: true},
	}
	if !reflect.DeepEqual(deltas, want) {
		t.Fatalf("deltas = %+v, want %+v", deltas, want)
	}
	w, ok := e.Effective("o1")
	if !ok || w.EventID != "e3" {
		t.Fatalf("winner = %+v, %v; want e3", w, ok)
	}
}

func TestRetractBeforeArrival(t *testing.T) {
	e := NewEngine()
	deltas := applyAll(e,
		Retract("e9"), // pending: event not seen yet
		Add(Event{ObjectID: "o1", EventID: "e9", Priority: 9, Seq: 1, Payload: "dead-on-arrival"}),
		Add(Event{ObjectID: "o1", EventID: "e1", Priority: 1, Seq: 1, Payload: "live"}),
	)
	want := []Delta{
		{ObjectID: "o1", NewPayload: "live", HasNew: true},
	}
	if !reflect.DeepEqual(deltas, want) {
		t.Fatalf("deltas = %+v, want %+v", deltas, want)
	}
}

func TestRetractWinnerRevealsNextCandidate(t *testing.T) {
	e := NewEngine()
	deltas := applyAll(e,
		Add(Event{ObjectID: "o1", EventID: "e1", Priority: 3, Seq: 1, Payload: "best"}),
		Add(Event{ObjectID: "o1", EventID: "e2", Priority: 2, Seq: 1, Payload: "runner-up"}),
		Add(Event{ObjectID: "o1", EventID: "e3", Priority: 1, Seq: 1, Payload: "last"}),
		Retract("e1"), // winner leaves -> runner-up surfaces
		Retract("e2"), // runner-up leaves -> last surfaces
		Retract("e3"), // nothing left -> effective value disappears
	)
	want := []Delta{
		{ObjectID: "o1", NewPayload: "best", HasNew: true},
		{ObjectID: "o1", OldPayload: "best", NewPayload: "runner-up", HadOld: true, HasNew: true},
		{ObjectID: "o1", OldPayload: "runner-up", NewPayload: "last", HadOld: true, HasNew: true},
		{ObjectID: "o1", OldPayload: "last", HadOld: true},
	}
	if !reflect.DeepEqual(deltas, want) {
		t.Fatalf("deltas = %+v, want %+v", deltas, want)
	}
	if _, ok := e.Effective("o1"); ok {
		t.Fatal("expected no effective value after all retractions")
	}
}

func TestRetractNonWinnerEmitsNoDelta(t *testing.T) {
	e := NewEngine()
	deltas := applyAll(e,
		Add(Event{ObjectID: "o1", EventID: "e1", Priority: 3, Seq: 1, Payload: "best"}),
		Add(Event{ObjectID: "o1", EventID: "e2", Priority: 2, Seq: 1, Payload: "runner-up"}),
		Retract("e2"),
	)
	want := []Delta{
		{ObjectID: "o1", NewPayload: "best", HasNew: true},
	}
	if !reflect.DeepEqual(deltas, want) {
		t.Fatalf("deltas = %+v, want %+v", deltas, want)
	}
}

func TestRetractTargetsOnlyGivenEventID(t *testing.T) {
	e := NewEngine()
	applyAll(e,
		Add(Event{ObjectID: "o1", EventID: "e1", Priority: 1, Seq: 1, Payload: "a"}),
		Add(Event{ObjectID: "o1", EventID: "e2", Priority: 1, Seq: 2, Payload: "b"}),
		Retract("e2"),
	)
	w, ok := e.Effective("o1")
	if !ok || w.EventID != "e1" {
		t.Fatalf("winner = %+v, %v; want e1 to survive retract of e2", w, ok)
	}
}

func TestDuplicateAndConflictingPayload(t *testing.T) {
	e := NewEngine()
	ev := Event{ObjectID: "o1", EventID: "e1", Priority: 1, Seq: 1, Payload: "original"}
	deltas := applyAll(e,
		Add(ev),
		Add(ev), // exact duplicate: no-op
		Add(Event{ObjectID: "o1", EventID: "e1", Priority: 9, Seq: 9, Payload: "conflict"}), // conflict: first wins
	)
	want := []Delta{
		{ObjectID: "o1", NewPayload: "original", HasNew: true},
	}
	if !reflect.DeepEqual(deltas, want) {
		t.Fatalf("deltas = %+v, want %+v", deltas, want)
	}
	if e.Duplicates != 1 {
		t.Fatalf("Duplicates = %d, want 1", e.Duplicates)
	}
	if e.Conflicts != 1 {
		t.Fatalf("Conflicts = %d, want 1", e.Conflicts)
	}
	w, _ := e.Effective("o1")
	if w.Payload != "original" {
		t.Fatalf("payload = %q, want original (first wins)", w.Payload)
	}
}

func TestRetractThenReaddSameIDStaysDead(t *testing.T) {
	e := NewEngine()
	ev := Event{ObjectID: "o1", EventID: "e1", Priority: 1, Seq: 1, Payload: "once"}
	deltas := applyAll(e,
		Add(ev),
		Retract("e1"),
		Add(ev), // re-delivery after retraction stays retracted
	)
	want := []Delta{
		{ObjectID: "o1", NewPayload: "once", HasNew: true},
		{ObjectID: "o1", OldPayload: "once", HadOld: true},
	}
	if !reflect.DeepEqual(deltas, want) {
		t.Fatalf("deltas = %+v, want %+v", deltas, want)
	}
}

func TestObjectsAreIndependent(t *testing.T) {
	e := NewEngine()
	applyAll(e,
		Add(Event{ObjectID: "o1", EventID: "e1", Priority: 1, Seq: 1, Payload: "a"}),
		Add(Event{ObjectID: "o2", EventID: "e2", Priority: 1, Seq: 1, Payload: "b"}),
		Retract("e1"),
	)
	if _, ok := e.Effective("o1"); ok {
		t.Fatal("o1 should have no effective value")
	}
	if w, ok := e.Effective("o2"); !ok || w.Payload != "b" {
		t.Fatalf("o2 winner = %+v, %v; want b", w, ok)
	}
}
