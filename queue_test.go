package evsched

import (
	"testing"
)

func popAll(q *Queue) []Event {
	var out []Event
	for {
		ev, ok := q.Pop()
		if !ok {
			return out
		}
		out = append(out, ev)
	}
}

func TestOrderingByTimeThenID(t *testing.T) {
	s := NewScheduler()
	q := s.NewQueue()
	q.Upsert("b", 5, "")
	q.Upsert("a", 5, "")
	q.Upsert("c", 3, "")
	q.Upsert("d", 9, "")

	got := popAll(q)
	want := []Event{{ID: "c", Time: 3}, {ID: "a", Time: 5}, {ID: "b", Time: 5}, {ID: "d", Time: 9}}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestUpsertInvalidatesOldEntry(t *testing.T) {
	s := NewScheduler()
	q := s.NewQueue()
	q.Upsert("x", 10, "old")
	q.Upsert("y", 5, "keep")
	q.Upsert("x", 20, "new") // old (x,10) must never surface

	if q.Len() != 2 {
		t.Fatalf("Len = %d, want 2", q.Len())
	}
	got := popAll(q)
	want := []Event{{ID: "y", Time: 5, Data: "keep"}, {ID: "x", Time: 20, Data: "new"}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCancelAffectsOnlyCurrentBranch(t *testing.T) {
	s := NewScheduler()
	base := s.NewQueue()
	base.Upsert("a", 1, "A")
	base.Upsert("b", 2, "B")

	left := base.Fork()
	right := base.Fork()

	if !left.Cancel("a") {
		t.Fatal("Cancel(a) on left returned false")
	}
	if left.Contains("a") {
		t.Fatal("left still contains a after Cancel")
	}
	if !right.Contains("a") || !base.Contains("a") {
		t.Fatal("Cancel leaked into sibling/parent branch")
	}
	if ev, ok := right.Peek(); !ok || ev.ID != "a" {
		t.Fatalf("right.Peek = %+v,%v want a,true", ev, ok)
	}
	if ev, ok := left.Peek(); !ok || ev.ID != "b" {
		t.Fatalf("left.Peek = %+v,%v want b,true", ev, ok)
	}
	if left.Cancel("missing") {
		t.Fatal("Cancel of absent id returned true")
	}
}

func TestForkIsolationOfUpsert(t *testing.T) {
	s := NewScheduler()
	base := s.NewQueue()
	base.Upsert("a", 1, "base")

	child := base.Fork()
	child.Upsert("a", 7, "child")

	if ev, _ := base.Peek(); ev.Time != 1 || ev.Data != "base" {
		t.Fatalf("base changed after child upsert: %+v", ev)
	}
	if ev, _ := child.Peek(); ev.Time != 7 || ev.Data != "child" {
		t.Fatalf("child.Peek = %+v, want time 7 data child", ev)
	}
}

func TestMergePicksLargerRevision(t *testing.T) {
	s := NewScheduler()
	base := s.NewQueue()
	base.Upsert("x", 1, "v1")
	base.Upsert("only-left", 3, "L")

	left := base.Fork()
	right := base.Fork()

	left.Upsert("x", 2, "v2")  // x rev 2
	left.Upsert("x", 4, "v3")  // x rev 3
	right.Upsert("x", 8, "v2") // x rev 2, loses to left's rev 3
	right.Upsert("only-right", 6, "R")

	merged, err := Merge(left, right)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got := popAll(merged)
	want := []Event{
		{ID: "x", Time: 4, Data: "v3"},
		{ID: "only-left", Time: 3, Data: "L"},
		{ID: "only-right", Time: 6, Data: "R"},
	}
	// order by (time,id): only-left(3), x(4), only-right(6)
	want = []Event{want[1], want[0], want[2]}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
	// Inputs must be untouched.
	if left.Len() != 2 || right.Len() != 3 {
		t.Fatalf("inputs modified: left=%d right=%d", left.Len(), right.Len())
	}
}

func TestMergeSameRevisionSameContentOK(t *testing.T) {
	s := NewScheduler()
	base := s.NewQueue()
	base.Upsert("x", 1, "v1")

	a := base.Fork()
	b := base.Fork()
	a.Upsert("y", 2, "ya")
	b.Upsert("z", 3, "zb")

	merged, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if merged.Len() != 3 {
		t.Fatalf("merged.Len = %d, want 3", merged.Len())
	}
}

func TestMergeConflictSameRevisionDifferentContent(t *testing.T) {
	s := NewScheduler()
	base := s.NewQueue()
	base.Upsert("x", 1, "v1")

	a := base.Fork()
	b := base.Fork()
	a.Upsert("x", 5, "a-content") // rev 2
	b.Upsert("x", 9, "b-content") // rev 2, different content

	_, err := Merge(a, b)
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("err = %v (%T), want *ConflictError", err, err)
	}
	if ce.ID != "x" || ce.Rev != 2 {
		t.Fatalf("ConflictError = %+v, want id x rev 2", ce)
	}
}

func TestMergeDifferentSchedulers(t *testing.T) {
	a := NewScheduler().NewQueue()
	b := NewScheduler().NewQueue()
	if _, err := Merge(a, b); err != ErrDifferentSchedulers {
		t.Fatalf("err = %v, want ErrDifferentSchedulers", err)
	}
}

func TestCancelThenMergeResurrectsFromOtherBranch(t *testing.T) {
	s := NewScheduler()
	base := s.NewQueue()
	base.Upsert("x", 1, "v1")

	a := base.Fork()
	b := base.Fork()
	a.Cancel("x") // cancel only in a

	merged, err := Merge(a, b)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !merged.Contains("x") {
		t.Fatal("x should survive via branch b")
	}
}
