package ormap

import (
	"fmt"
	"testing"
)

// threeReplicaScenario drives replicas a, b, c through a fixed schedule
// of assigns and observe-removes and records the emitted ops:
//
//	a1: a assigns "a1"          b1: b assigns "b1"
//	a and b sync (both directions)
//	RA: a removes (observed a1, b1)
//	c1: c assigns "c1"          (concurrent, unobserved by a)
//	b2: b assigns "b2"          (b has not seen RA)
//	c sees RA, then RC: c removes (observed c1 + tombs a1, b1)
//
// Direct interpretation of the op set: removed dots = {a1, b1, c1},
// so the only survivor is b2.
func threeReplicaScenario() []Op {
	a, b, c := NewMap("a"), NewMap("b"), NewMap("c")
	var ops []Op
	emit := func(op Op) Op { ops = append(ops, op); return op }

	a1 := emit(a.Assign("k", "a1"))
	b1 := emit(b.Assign("k", "b1"))
	if err := b.Apply(a1); err != nil {
		panic(err)
	}
	if err := a.Apply(b1); err != nil {
		panic(err)
	}
	ra := emit(a.Remove("k"))
	emit(c.Assign("k", "c1"))
	emit(b.Assign("k", "b2"))
	if err := c.Apply(ra); err != nil {
		panic(err)
	}
	emit(c.Remove("k"))
	return ops
}

func permutations(n int) [][]int {
	var out [][]int
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	var rec func(l int)
	rec = func(l int) {
		if l == n {
			out = append(out, append([]int(nil), perm...))
			return
		}
		for i := l; i < n; i++ {
			perm[l], perm[i] = perm[i], perm[l]
			rec(l + 1)
			perm[l], perm[i] = perm[i], perm[l]
		}
	}
	rec(0)
	return out
}

// applySeq delivers ops (by index, possibly repeated) to a fresh replica.
func applySeq(t *testing.T, ops []Op, seq []int) *Map {
	t.Helper()
	m := NewMap("test")
	for _, i := range seq {
		if err := m.Apply(ops[i]); err != nil {
			t.Fatalf("apply op %d: %v", i, err)
		}
	}
	return m
}

// Exhaustively replay the three-replica scenario in every op order with
// every subset of ops duplicated, and check the result against the
// direct interpretation of the op set.
func TestExhaustiveOrderAndDuplicates(t *testing.T) {
	ops := threeReplicaScenario()
	wantLive, err := Reference(ops)
	if err != nil {
		t.Fatal(err)
	}
	if got := values(wantLive["k"]); len(got) != 1 || got[0] != "b2" {
		t.Fatalf("reference live = %v, want [b2]", got)
	}

	perms := permutations(len(ops))
	runs := 0
	for _, perm := range perms {
		for mask := 0; mask < 1<<uint(len(ops)); mask++ {
			var seq []int
			for _, i := range perm {
				seq = append(seq, i)
				if mask&(1<<uint(i)) != 0 {
					seq = append(seq, i) // duplicate delivery
				}
			}
			m := applySeq(t, ops, seq)
			live := m.Live("k")
			if fmt.Sprint(values(live)) != fmt.Sprint(values(wantLive["k"])) {
				t.Fatalf("seq %v: live = %v, want %v", seq, values(live), values(wantLive["k"]))
			}
			disp, ok := m.Display("k")
			if !ok || disp.Value != "b2" {
				t.Fatalf("seq %v: display = %+v, want b2", seq, disp)
			}
			if m.Conflict("k") {
				t.Fatalf("seq %v: unexpected conflict", seq)
			}
			runs++
		}
	}
	t.Logf("checked %d order/duplicate combinations", runs)
}

// Split the op stream between two replicas in every possible way and
// check that the state merge converges on the reference result.
func TestExhaustiveStateMergeConvergence(t *testing.T) {
	ops := threeReplicaScenario()
	want, err := Reference(ops)
	if err != nil {
		t.Fatal(err)
	}
	for _, perm := range permutations(len(ops)) {
		for cut := 0; cut <= len(ops); cut++ {
			x := applySeq(t, ops, perm[:cut])
			y := applySeq(t, ops, perm[cut:])
			if err := x.Merge(y); err != nil {
				t.Fatalf("perm %v cut %d: %v", perm, cut, err)
			}
			if got := values(x.Live("k")); fmt.Sprint(got) != fmt.Sprint(values(want["k"])) {
				t.Fatalf("perm %v cut %d: live = %v, want %v", perm, cut, got, values(want["k"]))
			}
			// Merge the other way too: commutativity against the same cut.
			y2 := applySeq(t, ops, perm[cut:])
			x2 := applySeq(t, ops, perm[:cut])
			if err := y2.Merge(x2); err != nil {
				t.Fatalf("perm %v cut %d: %v", perm, cut, err)
			}
			if !x.Export().SameData(y2.Export()) {
				t.Fatalf("perm %v cut %d: merge not commutative", perm, cut)
			}
		}
	}
}

// After convergence, compact (tombstones are already stored as ranges),
// checkpoint, restore, and replay every old message: nothing may
// resurrect, and the visible content stays exactly the reference live set.
func TestCompactionThenReplayOldMessages(t *testing.T) {
	ops := threeReplicaScenario()
	m := NewMap("test")
	for _, op := range ops {
		if err := m.Apply(op); err != nil {
			t.Fatal(err)
		}
	}
	cp := m.Export()
	tombs := cp.Keys["k"].Tombstones
	wantTombs := map[string][][2]uint64{"a": {{1, 1}}, "b": {{1, 1}}, "c": {{1, 1}}}
	if fmt.Sprint(tombs) != fmt.Sprint(wantTombs) {
		t.Fatalf("compressed tombstones = %v, want %v", tombs, wantTombs)
	}

	r, err := Restore(cp)
	if err != nil {
		t.Fatal(err)
	}
	before := r.Export()
	for i := 0; i < 3; i++ { // replay everything, repeatedly
		for _, op := range ops {
			if err := r.Apply(op); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !r.Export().SameData(before) {
		t.Fatal("replaying old messages after compaction changed the state")
	}
	if got := values(r.Live("k")); len(got) != 1 || got[0] != "b2" {
		t.Fatalf("live after replay = %v, want [b2]", got)
	}
}

// The content visible before a remove differs from the content visible
// after it, while concurrent unobserved assignments stay visible.
func TestVisibleContentBeforeAndAfterRemove(t *testing.T) {
	a, b := NewMap("a"), NewMap("b")
	a1 := a.Assign("k", "a1")
	b1 := b.Assign("k", "b1")
	mustApply(t, a, b1) // a sees b1
	// b has NOT seen a1: b1 is concurrent with the coming remove.

	rm := a.Remove("k") // observed dots: a1, b1
	if got := values(a.Live("k")); len(got) != 0 {
		t.Fatalf("remover still sees %v", got)
	}

	mustApply(t, b, a1) // b now sees both assigns
	if got := values(b.Live("k")); fmt.Sprint(got) != fmt.Sprint([]string{"a1", "b1"}) {
		t.Fatalf("before remove arrives, b sees %v", got)
	}
	mustApply(t, b, rm)
	if got := values(b.Live("k")); len(got) != 0 {
		t.Fatalf("after remove arrives, b sees %v", got)
	}

	// A concurrent assignment b never observed would have survived:
	c := NewMap("c")
	c1 := c.Assign("k", "c1")
	mustApply(t, b, c1)
	if got := values(b.Live("k")); fmt.Sprint(got) != fmt.Sprint([]string{"c1"}) {
		t.Fatalf("unobserved concurrent assign should survive, b sees %v", got)
	}
}
