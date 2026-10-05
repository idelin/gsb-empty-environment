package ormap

import (
	"fmt"
	"reflect"
	"testing"
)

// permutations calls fn with every ordering of items.
func permutations(items []int, fn func([]int)) {
	var rec func(prefix, rest []int)
	rec = func(prefix, rest []int) {
		if len(rest) == 0 {
			fn(prefix)
			return
		}
		for i := range rest {
			next := append([]int{}, rest[:i]...)
			next = append(next, rest[i+1:]...)
			rec(append(append([]int{}, prefix...), rest[i]), next)
		}
	}
	rec(nil, items)
}

func assertLive(t *testing.T, label string, got map[Dot]string, want map[Dot]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: live set mismatch\n got: %v\nwant: %v", label, got, want)
	}
}

// Three replicas, assignments and observed-removes, delivered in every
// possible order and then re-delivered in full (duplicate delivery).
// Final state must match the direct interpretation of the op set.
func TestExhaustiveDeliveryMatchesReference(t *testing.T) {
	a1 := Dot{Replica: "A", Counter: 1}
	b1 := Dot{Replica: "B", Counter: 1}
	c1 := Dot{Replica: "C", Counter: 1}

	script := []refOp{
		refSet("A", 1, "k", "a1"),
		refSet("B", 1, "k", "b1"),
		refSet("C", 1, "k", "c1"),
		refSet("A", 2, "k", "a2"),
		refRemove("k", a1, b1), // A removed after observing a1 and b1
		refRemove("k", c1, a1), // C removed after observing c1 and a1
		refSet("C", 2, "k", "c2"),
	}
	want := refLive(script, "k")
	wantDisplay, wantConflict := refDisplay(script, "k")
	ops := toOps(script)

	indices := make([]int, len(ops))
	for i := range indices {
		indices[i] = i
	}
	permutations(indices, func(order []int) {
		// Op-based delivery: apply each op, then re-apply the whole
		// sequence to simulate duplicate delivery.
		r := NewReplica("R")
		for _, i := range order {
			if err := r.Apply(ops[i]); err != nil {
				t.Fatalf("apply %v: %v", order, err)
			}
		}
		for _, i := range order {
			if err := r.Apply(ops[i]); err != nil {
				t.Fatalf("re-apply %v: %v", order, err)
			}
		}
		assertLive(t, fmt.Sprintf("order %v", order), r.Live("k"), want)
		if got, conflict := r.Display("k"); got != wantDisplay || conflict != wantConflict {
			t.Fatalf("order %v: display = %q,%v; want %q,%v", order, got, conflict, wantDisplay, wantConflict)
		}

		// State-based: split the delivery between two replicas, then
		// merge states in both directions; both must equal the reference.
		half := len(order) / 2
		left, right := NewReplica("L"), NewReplica("R2")
		for _, i := range order[:half] {
			_ = left.Apply(ops[i])
		}
		for _, i := range order[half:] {
			_ = right.Apply(ops[i])
		}
		mergeAB, mergeBA := left.State(), right.State()
		if err := mergeAB.Merge(right.State()); err != nil {
			t.Fatalf("merge A<-B %v: %v", order, err)
		}
		if err := mergeBA.Merge(left.State()); err != nil {
			t.Fatalf("merge B<-A %v: %v", order, err)
		}
		assertLive(t, fmt.Sprintf("state merge A<-B %v", order), mergeAB.Live("k"), want)
		assertLive(t, fmt.Sprintf("state merge B<-A %v", order), mergeBA.Live("k"), want)
		if !mergeAB.Equal(mergeBA) {
			t.Fatalf("order %v: merge not commutative\nA<-B: %v\nB<-A: %v", order, mergeAB, mergeBA)
		}
	})
}

// Merge must be commutative, associative and idempotent on arbitrary
// states built from shared op pools.
func TestMergeAlgebra(t *testing.T) {
	pool := []*Op{
		NewSetOp("A", 1, "x", "a1"),
		NewSetOp("A", 2, "x", "a2"),
		NewSetOp("B", 1, "x", "b1"),
		NewSetOp("C", 1, "y", "c1"),
		NewRemoveOp("x", Dot{Replica: "A", Counter: 1}),
		NewRemoveOp("y", Dot{Replica: "C", Counter: 1}),
	}
	build := func(idxs ...int) *State {
		s := NewState()
		for _, i := range idxs {
			if err := s.Merge(pool[i]); err != nil {
				t.Fatalf("build %v: %v", idxs, err)
			}
		}
		return s
	}
	a := build(0, 1, 4)
	b := build(2, 4)
	c := build(3, 5, 0)

	ab, ba := a.Clone(), b.Clone()
	if err := ab.Merge(b); err != nil {
		t.Fatal(err)
	}
	if err := ba.Merge(a); err != nil {
		t.Fatal(err)
	}
	if !ab.Equal(ba) {
		t.Fatal("merge not commutative")
	}

	abc1, bc := a.Clone(), b.Clone()
	if err := bc.Merge(c); err != nil {
		t.Fatal(err)
	}
	if err := abc1.Merge(bc); err != nil {
		t.Fatal(err)
	}
	abc2, ab2 := c.Clone(), a.Clone()
	if err := ab2.Merge(b); err != nil {
		t.Fatal(err)
	}
	if err := abc2.Merge(ab2); err != nil {
		t.Fatal(err)
	}
	if !abc1.Equal(abc2) {
		t.Fatal("merge not associative")
	}

	aa := a.Clone()
	if err := aa.Merge(a); err != nil {
		t.Fatal(err)
	}
	if !aa.Equal(a) {
		t.Fatal("merge not idempotent")
	}
}

// A remove only deletes what the remover had observed; a concurrent
// assignment that was not observed survives.
func TestRemoveObservedOnly(t *testing.T) {
	alice := NewReplica("alice")
	bob := NewReplica("bob")

	opA1 := alice.Set("k", "from-alice")
	if err := bob.Apply(opA1); err != nil {
		t.Fatal(err)
	}

	before := bob.Live("k")
	if len(before) != 1 {
		t.Fatalf("before remove: live = %v", before)
	}

	// Bob removes what he sees; concurrently Alice sets again.
	remove := bob.Remove("k")
	opA2 := alice.Set("k", "alice-again")

	after := bob.Live("k")
	if len(after) != 0 {
		t.Fatalf("after remove: live = %v, want empty", after)
	}

	// The concurrent, unobserved assignment survives the remove.
	if err := bob.Apply(opA2); err != nil {
		t.Fatal(err)
	}
	if err := alice.Apply(remove); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*Replica{"alice": alice, "bob": bob} {
		live := r.Live("k")
		if len(live) != 1 || live[Dot{Replica: "alice", Counter: 2}] != "alice-again" {
			t.Fatalf("%s: live = %v, want only alice/2", name, live)
		}
	}
}

// Display resolves by logical time, then replica ID; concurrent values
// are never dropped for display purposes.
func TestDisplayLogicalClockTie(t *testing.T) {
	r := NewReplica("viewer")
	// Same logical time (counter 1) on two replicas: replica ID decides.
	if err := r.Apply(NewSetOp("ann", 1, "k", "value-ann")); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(NewSetOp("bob", 1, "k", "value-bob")); err != nil {
		t.Fatal(err)
	}
	got, conflict := r.Display("k")
	if got != "value-bob" || !conflict {
		t.Fatalf("tie: display = %q,%v; want value-bob,true", got, conflict)
	}
	if live := r.Live("k"); len(live) != 2 {
		t.Fatalf("display must not prune: live = %v", live)
	}

	// Higher logical time wins regardless of replica ID.
	if err := r.Apply(NewSetOp("ann", 2, "k", "value-ann-2")); err != nil {
		t.Fatal(err)
	}
	got, conflict = r.Display("k")
	if got != "value-ann-2" || !conflict {
		t.Fatalf("later: display = %q,%v; want value-ann-2,true", got, conflict)
	}
	if live := r.Live("k"); len(live) != 3 {
		t.Fatalf("display must not prune: live = %v", live)
	}
}

// Same dot carrying different content must be rejected.
func TestDotConflictRejected(t *testing.T) {
	r := NewReplica("r")
	if err := r.Apply(NewSetOp("A", 1, "k", "one")); err != nil {
		t.Fatal(err)
	}
	err := r.Apply(NewSetOp("A", 1, "k", "two"))
	if err == nil {
		t.Fatal("expected dot conflict error")
	}
	if _, ok := err.(*DotConflictError); !ok {
		t.Fatalf("error type = %T, want *DotConflictError", err)
	}
	// State is untouched by the rejected merge.
	assertLive(t, "after conflict", r.Live("k"), map[Dot]string{
		{Replica: "A", Counter: 1}: "one",
	})
}

// Checkpoints capture live data plus compacted tombstones; after restore,
// replaying an old (already removed) op must not resurrect its value.
func TestCheckpointRestoreAndOldReplay(t *testing.T) {
	r := NewReplica("A")
	op1 := r.Set("k", "v1")
	op2 := r.Set("k", "v2")
	op3 := r.Set("k", "v3")
	_ = op2
	r.Remove("k") // observes and removes all three

	// The causal context must have compacted the removed dots into the
	// version vector: no per-dot tombstones left for A.
	if got := r.state.Context.Max["A"]; got != 3 {
		t.Fatalf("context Max[A] = %d, want 3 (compacted)", got)
	}
	if len(r.state.Context.Extra) != 0 {
		t.Fatalf("context Extra = %v, want empty after compaction", r.state.Context.Extra)
	}

	data, err := r.State().Export()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Import(data)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Equal(r.State()) {
		t.Fatal("checkpoint round-trip changed the state")
	}

	// Replay old messages against the restored state: no resurrection.
	for _, op := range []*Op{op1, op3, op1} {
		if err := restored.Merge(op); err != nil {
			t.Fatal(err)
		}
	}
	if live := restored.Live("k"); len(live) != 0 {
		t.Fatalf("old op replay resurrected values: %v", live)
	}
}

// Out-of-order dots stay as individual tombstones until the gap fills,
// then fold into the version vector without losing coverage.
func TestCausalContextCompaction(t *testing.T) {
	ctx := NewCausalContext()
	ctx.Insert(Dot{Replica: "A", Counter: 1})
	ctx.Insert(Dot{Replica: "A", Counter: 3})
	if ctx.Max["A"] != 1 || !ctx.Contains(Dot{Replica: "A", Counter: 3}) {
		t.Fatalf("after gap insert: Max=%v Extra=%v", ctx.Max, ctx.Extra)
	}
	ctx.Insert(Dot{Replica: "A", Counter: 2})
	if ctx.Max["A"] != 3 || len(ctx.Extra) != 0 {
		t.Fatalf("after gap fill: Max=%v Extra=%v, want 3 and empty", ctx.Max, ctx.Extra)
	}
	for counter := uint64(1); counter <= 3; counter++ {
		if !ctx.Contains(Dot{Replica: "A", Counter: counter}) {
			t.Fatalf("compaction lost coverage of A/%d", counter)
		}
	}
	if ctx.Contains(Dot{Replica: "A", Counter: 4}) {
		t.Fatal("context claims uncovered dot A/4")
	}
}

// End-to-end replica scenario: content visible before a remove differs
// from after, and all replicas converge.
func TestReplicaScenarioConverges(t *testing.T) {
	a, b, c := NewReplica("A"), NewReplica("B"), NewReplica("C")

	a1 := a.Set("color", "red")
	b1 := b.Set("color", "blue")
	_ = a.Apply(b1)
	_ = b.Apply(a1)

	// Both see red+blue; A removes both observed values.
	if live := a.Live("color"); len(live) != 2 {
		t.Fatalf("before remove: %v", live)
	}
	rm := a.Remove("color")
	if live := a.Live("color"); len(live) != 0 {
		t.Fatalf("after remove: %v", live)
	}

	// C only saw blue, sets green concurrently with the remove.
	_ = c.Apply(b1)
	c1 := c.Set("color", "green")

	for _, r := range []*Replica{a, b, c} {
		for _, op := range []*Op{a1, b1, c1, rm} {
			if err := r.Apply(op); err != nil {
				t.Fatal(err)
			}
		}
	}
	want := map[Dot]string{{Replica: "C", Counter: 1}: "green"}
	for _, r := range []*Replica{a, b, c} {
		assertLive(t, r.ID(), r.Live("color"), want)
		if got, conflict := r.Display("color"); got != "green" || conflict {
			t.Fatalf("%s: display = %q,%v", r.ID(), got, conflict)
		}
	}
}
