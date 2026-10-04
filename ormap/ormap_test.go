package ormap

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mustApply(t *testing.T, m *Map, op Op) {
	t.Helper()
	if err := m.Apply(op); err != nil {
		t.Fatalf("apply %+v: %v", op, err)
	}
}

func mustMerge(t *testing.T, m, o *Map) {
	t.Helper()
	if err := m.Merge(o); err != nil {
		t.Fatalf("merge: %v", err)
	}
}

func values(live []Version) []string {
	var out []string
	for _, v := range live {
		out = append(out, v.Value)
	}
	return out
}

// A remove only kills the dots the remover had observed; a concurrent
// assignment it never saw survives.
func TestObservedRemoveSemantics(t *testing.T) {
	a, b := NewMap("a"), NewMap("b")
	opA := a.Assign("k", "from-a")
	opB := b.Assign("k", "from-b") // concurrent: a never sees it before removing

	mustApply(t, b, opA) // b sees both assignments
	if got := values(b.Live("k")); !reflect.DeepEqual(got, []string{"from-a", "from-b"}) {
		t.Fatalf("before remove, live = %v", got)
	}
	if !b.Conflict("k") {
		t.Fatal("before remove, expected conflict")
	}

	rm := a.Remove("k") // a observed only its own dot
	if len(rm.Dots) != 1 || rm.Dots[0] != opA.Version.Dot {
		t.Fatalf("remove carries %v, want only %v", rm.Dots, opA.Version.Dot)
	}
	mustApply(t, b, rm)

	if got := values(b.Live("k")); !reflect.DeepEqual(got, []string{"from-b"}) {
		t.Fatalf("after remove, live = %v, want [from-b]", got)
	}
	if b.Conflict("k") {
		t.Fatal("after remove, unexpected conflict")
	}
	disp, ok := b.Display("k")
	if !ok || disp.Value != "from-b" {
		t.Fatalf("after remove, display = %+v", disp)
	}
	if a.Conflict("k") {
		t.Fatal("empty key must not report conflict")
	}
	// The unobserved concurrent assignment must also survive on a after sync.
	mustApply(t, a, opB)
	if got := values(a.Live("k")); !reflect.DeepEqual(got, []string{"from-b"}) {
		t.Fatalf("after sync, a live = %v, want [from-b]", got)
	}
	_ = opB
}

// Concurrent first assignments tie on the Lamport clock; the display
// value breaks the tie by replica ID, and no concurrent value is
// deleted for display convenience.
func TestDisplayTiebreakKeepsAllValues(t *testing.T) {
	a, b, c := NewMap("a"), NewMap("b"), NewMap("c")
	opA := a.Assign("k", "va")
	opB := b.Assign("k", "vb")
	opC := c.Assign("k", "vc")
	for _, op := range []Op{opA, opB, opC} {
		if op.Version.Time != 1 {
			t.Fatalf("concurrent first assigns should tie at time 1, got %d", op.Version.Time)
		}
		mustApply(t, a, op)
	}
	if got := len(a.Live("k")); got != 3 {
		t.Fatalf("live = %d, want 3 (no deletion for display)", got)
	}
	if !a.Conflict("k") {
		t.Fatal("expected conflict flag")
	}
	disp, ok := a.Display("k")
	if !ok || disp.Value != "vc" || disp.Dot.Replica != "c" {
		t.Fatalf("tie should break toward highest replica ID, got %+v", disp)
	}

	// A causally later assignment wins the display on logical time.
	opC2 := c.Assign("k", "vc2")
	mustApply(t, a, opC2)
	disp, _ = a.Display("k")
	if disp.Value != "vc2" || disp.Time != 2 {
		t.Fatalf("later logical time should win display, got %+v", disp)
	}
	if got := len(a.Live("k")); got != 4 {
		t.Fatalf("live = %d, want 4 (losers stay live)", got)
	}
}

// Same dot with different content is rejected, on op apply and on
// state merge, without mutating the receiver.
func TestDotConflictRejected(t *testing.T) {
	m := NewMap("a")
	op := m.Assign("k", "v1")
	evil := op
	evil.Version.Value = "v2"

	if err := m.Apply(evil); err == nil {
		t.Fatal("apply of conflicting dot should fail")
	} else if _, ok := err.(*DotConflictError); !ok {
		t.Fatalf("error type = %T, want *DotConflictError", err)
	}

	other := NewMap("b")
	mustApply(t, other, evil)
	before := m.Export()
	if err := m.Merge(other); err == nil {
		t.Fatal("merge with conflicting dot should fail")
	}
	if after := m.Export(); !before.SameData(after) {
		t.Fatal("failed merge must not mutate the receiver")
	}
}

// Duplicate delivery of the same ops is a no-op.
func TestDuplicateDeliveryIdempotent(t *testing.T) {
	src := NewMap("a")
	op1 := src.Assign("k", "v1")
	op2 := src.Assign("k", "v2")
	rm := src.Remove("k")

	once := NewMap("b")
	twice := NewMap("c")
	for _, op := range []Op{op1, op2, rm} {
		mustApply(t, once, op)
		mustApply(t, twice, op)
		mustApply(t, twice, op) // duplicate
	}
	if !once.Export().SameData(twice.Export()) {
		t.Fatal("duplicate delivery changed the state")
	}
}

// Checkpoint export/restore round-trips through JSON; tombstone
// compression keeps exactly the causal info that stops old messages
// from resurrecting removed values, and the counter never reissues a
// dead dot.
func TestCheckpointRestoreAndReplay(t *testing.T) {
	a := NewMap("a")
	op1 := a.Assign("k", "v1")
	op2 := a.Assign("k", "v2")
	rm := a.Remove("k")

	cp := a.Export()
	tombs := cp.Keys["k"].Tombstones["a"]
	if !reflect.DeepEqual(tombs, [][2]uint64{{1, 2}}) {
		t.Fatalf("tombstones = %v, want one compressed range [1 2]", tombs)
	}

	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Checkpoint
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	r, err := Restore(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Export().SameData(a.Export()) {
		t.Fatal("restored state differs from exported state")
	}

	// Replay old messages after compaction: all dropped, nothing revives.
	mustApply(t, r, op1)
	mustApply(t, r, op2)
	mustApply(t, r, rm)
	if got := r.Live("k"); len(got) != 0 {
		t.Fatalf("replayed messages resurrected %v", got)
	}
	if !r.Export().SameData(a.Export()) {
		t.Fatal("replay of old messages changed the state")
	}

	// New assignments continue past the checkpointed counter.
	op3 := r.Assign("k", "v3")
	if op3.Version.Dot.Counter != 3 {
		t.Fatalf("dot counter reused after restore: %v", op3.Version.Dot)
	}
	if got := values(r.Live("k")); !reflect.DeepEqual(got, []string{"v3"}) {
		t.Fatalf("live = %v, want [v3]", got)
	}
}

// Merge is commutative, associative and idempotent.
func TestMergeLaws(t *testing.T) {
	build := func(seed string) *Map {
		m := NewMap(seed)
		m.Assign("x", seed+"1")
		m.Assign("y", seed+"2")
		return m
	}
	s1, s2, s3 := build("a"), build("b"), build("c")
	mustMerge(t, s2, s1) // overlapping history
	rm := s1.Remove("x")
	mustApply(t, s3, rm)

	clone := func(m *Map) *Map {
		r, err := Restore(m.Export())
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	merged := func(ms ...*Map) *Map {
		acc := clone(ms[0])
		for _, m := range ms[1:] {
			mustMerge(t, acc, m)
		}
		return acc
	}

	idem := merged(s1, s1)
	if !idem.Export().SameData(s1.Export()) {
		t.Fatal("merge not idempotent")
	}
	if !merged(s1, s2).Export().SameData(merged(s2, s1).Export()) {
		t.Fatal("merge not commutative")
	}
	left := merged(merged(s1, s2), s3)
	right := merged(s1, merged(s2, s3))
	if !left.Export().SameData(right.Export()) {
		t.Fatal("merge not associative")
	}
}
