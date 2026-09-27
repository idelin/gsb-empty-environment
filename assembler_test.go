package traceasm

import (
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func span(traceID, spanID, parentID string) Span {
	return Span{TraceID: traceID, SpanID: spanID, ParentSpanID: parentID, Name: spanID}
}

func eventTypes(evs []Event) []EventType {
	types := make([]EventType, len(evs))
	for i, e := range evs {
		types[i] = e.Type
	}
	return types
}

func hasEvent(evs []Event, typ EventType) bool {
	for _, e := range evs {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func findNode(t *testing.T, nodes []*TreeNode, spanID string) *TreeNode {
	t.Helper()
	for _, n := range nodes {
		if n.Span.SpanID == spanID {
			return n
		}
		if found := findNode(t, n.Children, spanID); found != nil {
			return found
		}
	}
	return nil
}

func TestChildBeforeParentThenLateRoot(t *testing.T) {
	a := New(5 * time.Second)

	evs := a.Ingest(span("t1", "s3", "s2"), at(1))
	if !hasEvent(evs, EventOrphanPending) {
		t.Fatalf("grandchild before parent: want OrphanPending, got %v", eventTypes(evs))
	}
	evs = a.Ingest(span("t1", "s2", "s1"), at(2))
	if !hasEvent(evs, EventOrphanPending) || !hasEvent(evs, EventOrphanResolved) {
		t.Fatalf("middle span: want OrphanPending+OrphanResolved, got %v", eventTypes(evs))
	}
	evs = a.Ingest(span("t1", "s1", ""), at(3))
	if !hasEvent(evs, EventRootRegistered) || !hasEvent(evs, EventOrphanResolved) {
		t.Fatalf("late root: want RootRegistered+OrphanResolved, got %v", eventTypes(evs))
	}

	f := a.Tree("t1")
	if len(f.Roots) != 1 || f.Roots[0].Span.SpanID != "s1" {
		t.Fatalf("want single root s1, got %+v", f.Roots)
	}
	if len(f.Unattached) != 0 {
		t.Fatalf("all orphans must be reattached, got unattached %+v", f.Unattached)
	}
	if findNode(t, f.Roots, "s3") == nil {
		t.Fatal("grandchild s3 must hang under the late root's subtree")
	}
}

func TestDuplicateSameParentMerges(t *testing.T) {
	a := New(5 * time.Second)
	a.Ingest(span("t1", "s1", ""), at(0))
	a.Ingest(span("t1", "s2", "s1"), at(1))

	dup := span("t1", "s2", "s1")
	dup.Name = "renamed"
	evs := a.Ingest(dup, at(2))
	if !hasEvent(evs, EventDuplicateUpdated) {
		t.Fatalf("want DuplicateUpdated, got %v", eventTypes(evs))
	}
	f := a.Tree("t1")
	if got := findNode(t, f.Roots, "s2"); got == nil || got.Span.Name != "renamed" {
		t.Fatalf("duplicate must merge mutable fields, got %+v", got)
	}
}

func TestDuplicateDifferentParentConflicts(t *testing.T) {
	a := New(5 * time.Second)
	a.Ingest(span("t1", "s1", ""), at(0))
	a.Ingest(span("t1", "s2", "s1"), at(1))

	evs := a.Ingest(span("t1", "s2", "s9"), at(2))
	if !hasEvent(evs, EventConflict) {
		t.Fatalf("want ConflictDetected, got %v", eventTypes(evs))
	}
	f := a.Tree("t1")
	s1 := findNode(t, f.Roots, "s1")
	if len(s1.Children) != 1 || s1.Children[0].Span.SpanID != "s2" {
		t.Fatalf("first parent must win, s1 children: %+v", s1.Children)
	}
}

func TestMissingParentExpiresAndIsNotReRooted(t *testing.T) {
	a := New(5 * time.Second)
	a.Ingest(span("t1", "s1", ""), at(0))
	a.Ingest(span("t1", "s2", "missing"), at(1))

	evs := a.Expire(at(7))
	if !hasEvent(evs, EventOrphanExpired) {
		t.Fatalf("want OrphanExpired, got %v", eventTypes(evs))
	}
	f := a.Tree("t1")
	if len(f.Roots) != 1 || len(f.Roots[0].Children) != 0 {
		t.Fatalf("expired orphan must not be attached to root, roots: %+v", f.Roots)
	}
	if len(f.Unattached) != 1 || f.Unattached[0].State != "expired" {
		t.Fatalf("want one expired unattached span, got %+v", f.Unattached)
	}
}

func TestOrphanSurvivesUntilDeadline(t *testing.T) {
	a := New(5 * time.Second)
	a.Ingest(span("t1", "s2", "s1"), at(1))
	if evs := a.Expire(at(5)); len(evs) != 0 {
		t.Fatalf("orphan must survive before deadline, got %v", eventTypes(evs))
	}
	evs := a.Ingest(span("t1", "s1", ""), at(6))
	if !hasEvent(evs, EventOrphanResolved) {
		t.Fatalf("late parent must still resolve orphan, got %v", eventTypes(evs))
	}
}

func TestStructuralCycleDetected(t *testing.T) {
	a := New(5 * time.Second)
	a.Ingest(span("t1", "a", "b"), at(0)) // orphan waiting for b
	evs := a.Ingest(span("t1", "b", "a"), at(1))
	if !hasEvent(evs, EventCycle) {
		t.Fatalf("mutual parent cycle: want CycleDetected, got %v", eventTypes(evs))
	}

	evs = a.Ingest(span("t1", "self", "self"), at(2))
	if !hasEvent(evs, EventCycle) {
		t.Fatalf("self loop: want CycleDetected, got %v", eventTypes(evs))
	}

	f := a.Tree("t1")
	if len(f.Roots) != 0 {
		t.Fatalf("cyclic spans must not become roots, got %+v", f.Roots)
	}
	var cyclic int
	for _, u := range f.Unattached {
		if u.State == "cyclic" {
			cyclic++
		}
	}
	if cyclic != 2 {
		t.Fatalf("want 2 cyclic spans, got %+v", f.Unattached)
	}
}

func TestCrossTraceReferenceIsNotAStructuralCycle(t *testing.T) {
	a := New(5 * time.Second)
	a.Ingest(span("tA", "s1", ""), at(0))

	evs := a.Ingest(span("tB", "s9", "s1"), at(1))
	if !hasEvent(evs, EventCrossTraceRef) {
		t.Fatalf("want CrossTraceReference, got %v", eventTypes(evs))
	}
	if hasEvent(evs, EventCycle) {
		t.Fatalf("cross-trace reference must not be reported as a cycle: %v", eventTypes(evs))
	}
	if !hasEvent(evs, EventOrphanPending) {
		t.Fatalf("cross-trace child stays pending, got %v", eventTypes(evs))
	}

	evs = a.Expire(at(7))
	if !hasEvent(evs, EventOrphanExpired) {
		t.Fatalf("cross-trace orphan must expire, got %v", eventTypes(evs))
	}
	fA, fB := a.Tree("tA"), a.Tree("tB")
	if len(fA.Roots) != 1 || len(fA.Roots[0].Children) != 0 {
		t.Fatalf("trace tA must not adopt tB's span, roots: %+v", fA.Roots)
	}
	if len(fB.Roots) != 0 || len(fB.Unattached) != 1 {
		t.Fatalf("tB span must stay unattached, got roots=%+v unattached=%+v", fB.Roots, fB.Unattached)
	}
}

func TestRecursiveOrphanChain(t *testing.T) {
	a := New(5 * time.Second)
	a.Ingest(span("t1", "s4", "s3"), at(1))
	a.Ingest(span("t1", "s3", "s2"), at(2))
	a.Ingest(span("t1", "s2", "s1"), at(3))
	a.Ingest(span("t1", "s1", ""), at(4))

	f := a.Tree("t1")
	if len(f.Unattached) != 0 {
		t.Fatalf("whole chain must resolve, unattached: %+v", f.Unattached)
	}
	if findNode(t, f.Roots, "s4") == nil {
		t.Fatal("deepest span s4 must be reachable from root")
	}
}
