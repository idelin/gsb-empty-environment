package eventsched

import (
	"reflect"
	"testing"
)

func TestPopOrdersByTimeThenID(t *testing.T) {
	b := New()
	for _, ev := range []Event{
		{ID: "c", Time: 5},
		{ID: "a", Time: 5},
		{ID: "b", Time: 1},
		{ID: "d", Time: 1},
	} {
		b = b.Upsert(ev)
	}
	var got []string
	for {
		ev, next, ok := b.Pop()
		if !ok {
			break
		}
		got = append(got, ev.ID)
		b = next
	}
	want := []string{"b", "d", "a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pop order = %v, want %v", got, want)
	}
}

func TestUpsertInvalidatesOldEntry(t *testing.T) {
	b := New().
		Upsert(Event{ID: "a", Time: 5, Payload: "old"}).
		Upsert(Event{ID: "b", Time: 1}).
		Upsert(Event{ID: "a", Time: 10, Payload: "new"})

	if b.Len() != 2 {
		t.Fatalf("Len = %d, want 2", b.Len())
	}
	ev, b, ok := b.Pop()
	if !ok || ev.ID != "b" {
		t.Fatalf("first pop = %v, %v; want b", ev, ok)
	}
	ev, b, ok = b.Pop()
	if !ok || ev.ID != "a" || ev.Time != 10 || ev.Payload != "new" {
		t.Fatalf("second pop = %+v, %v; want a@10/new", ev, ok)
	}
	if _, _, ok := b.Pop(); ok {
		t.Fatal("third pop should fail: stale entry for a@5 resurfaced")
	}
}

func TestPeekDoesNotConsume(t *testing.T) {
	b := New().Upsert(Event{ID: "x", Time: 3})
	for i := 0; i < 3; i++ {
		ev, ok := b.Peek()
		if !ok || ev.ID != "x" {
			t.Fatalf("Peek #%d = %v, %v; want x, true", i, ev, ok)
		}
	}
	if b.Len() != 1 {
		t.Fatalf("Len = %d after peeks, want 1", b.Len())
	}
}

func TestCancelOnlyAffectsCurrentBranch(t *testing.T) {
	base := New().
		Upsert(Event{ID: "a", Time: 1}).
		Upsert(Event{ID: "b", Time: 2})

	cancelled := base.Cancel("a")
	kept := base.Fork()

	if cancelled.Has("a") {
		t.Fatal("cancelled branch still has a")
	}
	if !kept.Has("a") || !base.Has("a") {
		t.Fatal("cancel leaked into sibling/parent branch")
	}
	if ev, ok := cancelled.Peek(); !ok || ev.ID != "b" {
		t.Fatalf("cancelled branch peek = %v, %v; want b", ev, ok)
	}
	if ev, ok := kept.Peek(); !ok || ev.ID != "a" {
		t.Fatalf("kept branch peek = %v, %v; want a", ev, ok)
	}
}

func TestForkIndependence(t *testing.T) {
	base := New().Upsert(Event{ID: "a", Time: 1})
	f1 := base.Fork().Upsert(Event{ID: "b", Time: 2})
	f2 := base.Fork().Cancel("a")

	if base.Len() != 1 || !base.Has("a") {
		t.Fatalf("base mutated by fork operations: %+v", base.List())
	}
	if !f1.Has("a") || !f1.Has("b") {
		t.Fatalf("f1 = %+v, want a and b", f1.List())
	}
	if f2.Has("a") {
		t.Fatalf("f2 = %+v, want empty", f2.List())
	}
}

func TestMergePicksLargerRevision(t *testing.T) {
	base := New().Upsert(Event{ID: "a", Time: 1, Payload: "v1"})
	updated := base.Fork().Upsert(Event{ID: "a", Time: 9, Payload: "v2"})

	m, err := Merge(base, updated)
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	ev, ok := m.Peek()
	if !ok || ev.Time != 9 || ev.Payload != "v2" {
		t.Fatalf("merged peek = %+v, %v; want a@9/v2", ev, ok)
	}
	if m.Len() != 1 {
		t.Fatalf("merged Len = %d, want 1", m.Len())
	}
	// Symmetric argument order.
	m2, err := Merge(updated, base)
	if err != nil {
		t.Fatalf("Merge (swapped) error: %v", err)
	}
	if ev, _ := m2.Peek(); ev.Payload != "v2" {
		t.Fatalf("swapped merge peek = %+v, want v2", ev)
	}
}

func TestMergeConflictSameRevision(t *testing.T) {
	base := New().Upsert(Event{ID: "a", Time: 1, Payload: "v1"})
	f1 := base.Fork().Upsert(Event{ID: "a", Time: 5, Payload: "x"})
	f2 := base.Fork().Upsert(Event{ID: "a", Time: 5, Payload: "y"})

	_, err := Merge(f1, f2)
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("Merge err = %v, want *ConflictError", err)
	}
	if ce.ID != "a" {
		t.Fatalf("conflict ID = %q, want a", ce.ID)
	}
}

func TestMergeSameRevisionSameContent(t *testing.T) {
	base := New().Upsert(Event{ID: "a", Time: 1, Payload: "v"})
	f1 := base.Fork().Upsert(Event{ID: "b", Time: 2})
	f2 := base.Fork().Upsert(Event{ID: "c", Time: 3})

	m, err := Merge(f1, f2)
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	got := m.List()
	want := []Event{
		{ID: "a", Time: 1, Payload: "v"},
		{ID: "b", Time: 2},
		{ID: "c", Time: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged List = %+v, want %+v", got, want)
	}
}

func TestMergeDisjointAndCancelResurrection(t *testing.T) {
	base := New().Upsert(Event{ID: "a", Time: 1})
	cancelled := base.Fork().Cancel("a")
	other := New().Upsert(Event{ID: "z", Time: 7})

	// Cancel only deletes locally: merging with a branch that still has the
	// ID brings the event back.
	m, err := Merge(cancelled, base)
	if err != nil {
		t.Fatalf("Merge error: %v", err)
	}
	if !m.Has("a") {
		t.Fatal("merge of cancelled branch with base lost a")
	}

	d, err := Merge(cancelled, other)
	if err != nil {
		t.Fatalf("Merge disjoint error: %v", err)
	}
	if d.Len() != 1 || !d.Has("z") {
		t.Fatalf("disjoint merge = %+v, want only z", d.List())
	}
}

func TestZeroValueUsable(t *testing.T) {
	var b Branch
	if _, ok := b.Peek(); ok {
		t.Fatal("zero branch should be empty")
	}
	b = b.Upsert(Event{ID: "a", Time: 1})
	if ev, ok := b.Peek(); !ok || ev.ID != "a" {
		t.Fatalf("zero-value branch peek = %v, %v", ev, ok)
	}
}
