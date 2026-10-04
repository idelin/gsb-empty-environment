package main

import (
	"reflect"
	"testing"
)

func ev(id, object, op string, fields map[string]any, causes ...string) Event {
	return Event{ID: id, Object: object, Op: op, Fields: fields, Causes: causes}
}

func mustReconcile(t *testing.T, events []Event) *Report {
	t.Helper()
	rep, err := Reconcile(events)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return rep
}

func TestDeterminedChain(t *testing.T) {
	rep := mustReconcile(t, []Event{
		ev("e1", "order-1", "create", map[string]any{"status": "new"}),
		ev("e2", "order-1", "set", map[string]any{"status": "paid"}, "e1"),
	})
	if rep.Status != "determined" {
		t.Fatalf("status = %s", rep.Status)
	}
	want := map[string]ObjectState{"order-1": {Exists: true, Fields: map[string]any{"status": "paid"}}}
	if !reflect.DeepEqual(rep.Conclusion, want) {
		t.Fatalf("conclusion = %+v", rep.Conclusion)
	}
	if rep.LegalOrders != 1 {
		t.Fatalf("legal orders = %d", rep.LegalOrders)
	}
}

func TestAmbiguousUnorderedWrites(t *testing.T) {
	rep := mustReconcile(t, []Event{
		ev("e1", "order-1", "create", map[string]any{"status": "new"}),
		ev("e2", "order-1", "set", map[string]any{"status": "paid"}, "e1"),
		ev("e3", "order-1", "set", map[string]any{"status": "refunded"}, "e1"),
	})
	if rep.Status != "ambiguous" {
		t.Fatalf("status = %s", rep.Status)
	}
	if len(rep.FinalStates) != 2 {
		t.Fatalf("final states = %d, want 2", len(rep.FinalStates))
	}
	if rep.LegalOrders != 2 {
		t.Fatalf("legal orders = %d, want 2", rep.LegalOrders)
	}
}

func TestCommutingSetsDetermined(t *testing.T) {
	// Two independent fields: both orders legal, one final state.
	rep := mustReconcile(t, []Event{
		ev("e1", "order-1", "create", nil),
		ev("e2", "order-1", "set", map[string]any{"a": "1"}, "e1"),
		ev("e3", "order-1", "set", map[string]any{"b": "2"}, "e1"),
	})
	if rep.Status != "determined" {
		t.Fatalf("status = %s", rep.Status)
	}
	if rep.LegalOrders != 2 {
		t.Fatalf("legal orders = %d, want 2", rep.LegalOrders)
	}
	want := map[string]any{"a": "1", "b": "2"}
	if !reflect.DeepEqual(rep.Conclusion["order-1"].Fields, want) {
		t.Fatalf("fields = %+v", rep.Conclusion["order-1"].Fields)
	}
}

func TestIdenticalDuplicatesMerged(t *testing.T) {
	rep := mustReconcile(t, []Event{
		{ID: "e1", Source: "logA", Timestamp: "2026-01-01T00:00:00Z", Object: "o", Op: "create", Fields: map[string]any{"x": "1"}},
		{ID: "e1", Source: "logB", Timestamp: "2026-01-02T00:00:00Z", Object: "o", Op: "create", Fields: map[string]any{"x": "1"}},
	})
	if rep.Status != "determined" || rep.Duplicates != 1 || rep.EventCount != 1 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestConflictingDuplicates(t *testing.T) {
	rep := mustReconcile(t, []Event{
		{ID: "e1", Source: "logA", Object: "o", Op: "create", Fields: map[string]any{"x": "1"}},
		{ID: "e1", Source: "logB", Object: "o", Op: "create", Fields: map[string]any{"x": "2"}},
	})
	if rep.Status != "contradiction" || rep.Conflict.Kind != "conflicting_duplicates" {
		t.Fatalf("report = %+v", rep)
	}
	if !reflect.DeepEqual(rep.Conflict.Events, []string{"e1"}) {
		t.Fatalf("conflict events = %v", rep.Conflict.Events)
	}
}

func TestCausalCycleMinimalSubset(t *testing.T) {
	rep := mustReconcile(t, []Event{
		ev("a", "o", "create", nil, "c"),
		ev("b", "o", "set", map[string]any{"x": "1"}, "a"),
		ev("c", "o", "set", map[string]any{"y": "2"}, "b"),
		ev("d", "o", "set", map[string]any{"z": "3"}),
		ev("e", "p", "create", nil),
	})
	if rep.Status != "contradiction" || rep.Conflict.Kind != "causal_cycle" {
		t.Fatalf("report = %+v", rep)
	}
	got := append([]string(nil), rep.Conflict.Events...)
	for _, s := range [][]string{got} {
		_ = s
	}
	// The minimal conflicting subset is the 3-cycle; innocents excluded.
	want := map[string]bool{"a": true, "b": true, "c": true}
	if len(got) != 3 {
		t.Fatalf("cycle = %v, want 3 events", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected event %q in cycle %v", id, got)
		}
	}
}

func TestSelfLoop(t *testing.T) {
	rep := mustReconcile(t, []Event{
		ev("a", "o", "create", nil, "a"),
	})
	if rep.Status != "contradiction" || rep.Conflict.Kind != "causal_cycle" {
		t.Fatalf("report = %+v", rep)
	}
	if !reflect.DeepEqual(rep.Conflict.Events, []string{"a"}) {
		t.Fatalf("conflict events = %v", rep.Conflict.Events)
	}
}

func TestMissingCauseTolerated(t *testing.T) {
	rep := mustReconcile(t, []Event{
		ev("e1", "o", "set", map[string]any{"x": "1"}, "ghost"),
	})
	if rep.Status != "determined" {
		t.Fatalf("status = %s", rep.Status)
	}
	if !reflect.DeepEqual(rep.MissingCauses, map[string][]string{"e1": {"ghost"}}) {
		t.Fatalf("missing causes = %v", rep.MissingCauses)
	}
}

func TestCausalityBeatsTimestamp(t *testing.T) {
	// Timestamps suggest delete < set < create; causality forces the
	// opposite. The causal order must win.
	rep := mustReconcile(t, []Event{
		{ID: "create", Timestamp: "2026-01-03T00:00:00Z", Object: "o", Op: "create", Fields: map[string]any{"s": "new"}},
		{ID: "set", Timestamp: "2026-01-02T00:00:00Z", Object: "o", Op: "set", Fields: map[string]any{"s": "paid"}, Causes: []string{"create"}},
		{ID: "del", Timestamp: "2026-01-01T00:00:00Z", Object: "o", Op: "delete", Causes: []string{"set"}},
	})
	if rep.Status != "determined" {
		t.Fatalf("status = %s", rep.Status)
	}
	if len(rep.Conclusion) != 0 {
		t.Fatalf("conclusion = %+v, want empty (deleted)", rep.Conclusion)
	}
}

func TestNoLegalReconstructionDoubleCreate(t *testing.T) {
	rep := mustReconcile(t, []Event{
		ev("c1", "o", "create", map[string]any{"x": "1"}),
		ev("c2", "o", "create", map[string]any{"x": "2"}),
	})
	if rep.Status != "contradiction" || rep.Conflict.Kind != "no_legal_reconstruction" {
		t.Fatalf("report = %+v", rep)
	}
	if !reflect.DeepEqual(rep.Conflict.Events, []string{"c1", "c2"}) {
		t.Fatalf("conflict events = %v", rep.Conflict.Events)
	}
}

func TestMinimalSubsetExcludesInnocents(t *testing.T) {
	rep := mustReconcile(t, []Event{
		ev("c1", "o", "create", nil),
		ev("c2", "o", "create", nil),
		ev("ok", "p", "create", nil),
		ev("ok2", "p", "set", map[string]any{"a": "1"}, "ok"),
	})
	if rep.Status != "contradiction" {
		t.Fatalf("status = %s", rep.Status)
	}
	if !reflect.DeepEqual(rep.Conflict.Events, []string{"c1", "c2"}) {
		t.Fatalf("conflict events = %v", rep.Conflict.Events)
	}
}

func TestSetAfterDeleteMinimalSubset(t *testing.T) {
	// create is removable from the conflict: delete+set with set forced
	// after delete is already irreconcilable.
	rep := mustReconcile(t, []Event{
		ev("c", "o", "create", nil),
		ev("d", "o", "delete", nil, "c"),
		ev("s", "o", "set", map[string]any{"x": "1"}, "c", "d"),
	})
	if rep.Status != "contradiction" || rep.Conflict.Kind != "no_legal_reconstruction" {
		t.Fatalf("report = %+v", rep)
	}
	if !reflect.DeepEqual(rep.Conflict.Events, []string{"d", "s"}) {
		t.Fatalf("conflict events = %v", rep.Conflict.Events)
	}
}

func TestCreateDeleteCreateChain(t *testing.T) {
	rep := mustReconcile(t, []Event{
		ev("c1", "o", "create", map[string]any{"v": "1"}),
		ev("d", "o", "delete", nil, "c1"),
		ev("c2", "o", "create", map[string]any{"v": "2"}, "d"),
	})
	if rep.Status != "determined" {
		t.Fatalf("status = %s", rep.Status)
	}
	want := map[string]any{"v": "2"}
	if !reflect.DeepEqual(rep.Conclusion["o"].Fields, want) {
		t.Fatalf("fields = %+v", rep.Conclusion["o"].Fields)
	}
}

func TestLenientMissingHistory(t *testing.T) {
	// No create in the log: the object's earlier history may be missing.
	rep := mustReconcile(t, []Event{
		ev("s", "o", "set", map[string]any{"x": "1"}),
	})
	if rep.Status != "determined" {
		t.Fatalf("status = %s", rep.Status)
	}
	if !rep.Conclusion["o"].Exists {
		t.Fatalf("conclusion = %+v", rep.Conclusion)
	}
}

func TestMultiObjectIndependent(t *testing.T) {
	rep := mustReconcile(t, []Event{
		ev("a", "o1", "create", map[string]any{"x": "1"}),
		ev("b", "o2", "create", map[string]any{"y": "2"}),
	})
	if rep.Status != "determined" || rep.LegalOrders != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.Conclusion) != 2 {
		t.Fatalf("conclusion = %+v", rep.Conclusion)
	}
}

func TestTooManyEvents(t *testing.T) {
	var events []Event
	for i := 0; i < 21; i++ {
		events = append(events, ev(string(rune('a'+i)), "o", "set", nil))
	}
	if _, err := Reconcile(events); err == nil {
		t.Fatal("expected error for 21 events")
	}
}

func TestEmptyLog(t *testing.T) {
	rep := mustReconcile(t, nil)
	if rep.Status != "determined" || len(rep.Conclusion) != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestDeleteThenRecreateAmbiguity(t *testing.T) {
	// delete and second create are unordered: only delete-then-create is
	// legal, so the result is determined even without a causal edge.
	rep := mustReconcile(t, []Event{
		ev("c1", "o", "create", map[string]any{"v": "1"}),
		ev("d", "o", "delete", nil, "c1"),
		ev("c2", "o", "create", map[string]any{"v": "2"}, "c1"),
	})
	if rep.Status != "determined" {
		t.Fatalf("status = %s", rep.Status)
	}
	if !reflect.DeepEqual(rep.Conclusion["o"].Fields, map[string]any{"v": "2"}) {
		t.Fatalf("conclusion = %+v", rep.Conclusion)
	}
}
