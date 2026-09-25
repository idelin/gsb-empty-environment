package orset

import (
	"testing"
)

var roster = []string{"A", "B", "C"}

func newSet(id string) *Set { return New(id, roster...) }

// newPair builds a two-replica roster so GC confirmation only involves A,B.
func newPair(id string) *Set { return New(id, "A", "B") }

// --- Merge laws: commutative, associative, idempotent ---------------------

// diverged returns three replicas that synced once and then diverged with
// adds and removes, plus their snapshots.
func diverged() (a, b, c *Set) {
	a, b, c = newSet("A"), newSet("B"), newSet("C")
	a.Add("apple")
	a.Add("pear")
	b.Merge(a.Snapshot())
	c.Merge(a.Snapshot())
	// Diverge.
	a.Add("fig")
	a.Remove("pear")
	b.Add("grape")
	b.Remove("apple")
	c.Add("kiwi")
	c.Remove("pear")
	return a, b, c
}

func TestMergeCommutative(t *testing.T) {
	a, b, c := diverged()
	if !Equal(Join(a.Snapshot(), b.Snapshot()), Join(b.Snapshot(), a.Snapshot())) {
		t.Fatal("A|B != B|A")
	}
	if !Equal(Join(a.Snapshot(), c.Snapshot()), Join(c.Snapshot(), a.Snapshot())) {
		t.Fatal("A|C != C|A")
	}
}

func TestMergeAssociative(t *testing.T) {
	a, b, c := diverged()
	left := Join(Join(a.Snapshot(), b.Snapshot()), c.Snapshot())
	right := Join(a.Snapshot(), Join(b.Snapshot(), c.Snapshot()))
	if !Equal(left, right) {
		t.Fatal("(A|B)|C != A|(B|C)")
	}
}

func TestMergeIdempotent(t *testing.T) {
	a, b, _ := diverged()
	m := Join(a.Snapshot(), b.Snapshot())
	if !Equal(m, Join(m, a.Snapshot())) {
		t.Fatal("re-merging A changed the state")
	}
	if !Equal(m, Join(m, b.Snapshot())) {
		t.Fatal("re-merging B changed the state")
	}
	if !Equal(m, Join(m, m)) {
		t.Fatal("self-join changed the state")
	}
	// Idempotency through the replica Merge path as well.
	r := newSet("A")
	r.Merge(m)
	once := r.Snapshot()
	r.Merge(m)
	r.Merge(r.Snapshot())
	if !Equal(once, r.Snapshot()) {
		t.Fatal("re-merging changed the state")
	}
}

// --- Observed-remove semantics --------------------------------------------

func TestConcurrentAddSurvivesRemove(t *testing.T) {
	a, b := newSet("A"), newSet("B")
	a.Add("x")               // tag A#1
	b.Merge(a.Snapshot())    // B observes A#1
	removed := b.Remove("x") // removes exactly the observed tag A#1
	if len(removed) != 1 || removed[0] != (Tag{Replica: "A", Seq: 1}) {
		t.Fatalf("remove tombstoned %v, want [A#1]", removed)
	}
	a.Add("x")            // concurrent add A#2, unobserved by B
	a.Merge(b.Snapshot()) // remove arrives at A
	if !a.Contains("x") {
		t.Fatal("concurrent (unobserved) add must survive the remove")
	}
	b.Merge(a.Snapshot())
	if !b.Contains("x") {
		t.Fatal("concurrent add lost after merge at B")
	}
	// The observed tag A#1 stays tombstoned everywhere.
	if len(a.st.Adds["x"]) != 1 || !a.st.Adds["x"][Tag{Replica: "A", Seq: 2}] {
		t.Fatalf("live tags = %v, want only A#2", a.st.Adds["x"])
	}
}

func TestDuplicateRemoveIsNoOp(t *testing.T) {
	a := newSet("A")
	a.Add("x")
	a.Add("y")
	a.Remove("x")
	before := a.Snapshot()
	if got := a.Remove("x"); got != nil {
		t.Fatalf("duplicate remove returned %v, want nil", got)
	}
	a.Remove("x")
	a.Remove("never-added")
	if !Equal(before, a.Snapshot()) {
		t.Fatal("duplicate remove changed the state")
	}
}

func TestUniqueTags(t *testing.T) {
	a, b := newSet("A"), newSet("B")
	ta := a.Add("x")
	tb := b.Add("x") // concurrent, same element
	if ta == tb {
		t.Fatalf("tags collide: %v", ta)
	}
	a.Merge(b.Snapshot())
	if len(a.st.Adds["x"]) != 2 {
		t.Fatalf("live tags = %v, want both concurrent adds", a.st.Adds["x"])
	}
}

// --- Tombstone GC ----------------------------------------------------------

// Counterexample: dropping a tombstone before the remove is causally
// confirmed lets a stale replica resurrect the removed element.
func TestPrematureTombstoneGCResurrects(t *testing.T) {
	a, b := newPair("A"), newPair("B")
	a.Add("x")               // A#1
	b.Merge(a.Snapshot())    // B observes the add
	a.Remove("x")            // tombstone A#1, removed by A#2
	a.UnsafeDropTombstones() // PREMATURE: B has not seen the remove yet
	a.Merge(b.Snapshot())    // B still holds add A#1, no tombstone blocks it
	if !a.Contains("x") {
		t.Fatal("expected resurrection hazard to be reproducible")
	}
	if !b.Contains("x") {
		t.Fatal("B never saw the remove, must still contain x")
	}
}

// The same scenario with causal confirmation: GC refuses to collect until
// every replica has applied the remove, and then the element never returns.
func TestCausalGCPreventsResurrection(t *testing.T) {
	a, b := newPair("A"), newPair("B")
	a.Add("x")
	b.Merge(a.Snapshot())
	a.Remove("x")

	// B has not applied the remove: GC must collect nothing.
	if n := a.GC(); n != 0 {
		t.Fatalf("GC collected %d tombstones without confirmation", n)
	}

	// Propagate the remove to B and B's version vector back to A.
	b.Merge(a.Snapshot())
	a.Merge(b.Snapshot())
	if n := a.GC(); n != 1 {
		t.Fatalf("GC collected %d tombstones, want 1", n)
	}
	b.GC()

	// No matter how often the replicas sync now, x stays removed.
	for i := 0; i < 3; i++ {
		a.Merge(b.Snapshot())
		b.Merge(a.Snapshot())
	}
	if a.Contains("x") || b.Contains("x") {
		t.Fatal("element resurrected after causally-confirmed GC")
	}
	if a.Tombstones() != 0 || b.Tombstones() != 0 {
		t.Fatal("tombstones should all be collected")
	}
}

// GC must wait for EVERY replica in the roster, not just the ones heard from.
func TestGCWaitsForWholeRoster(t *testing.T) {
	a, b, c := newSet("A"), newSet("B"), newSet("C")
	a.Add("x")
	b.Merge(a.Snapshot())
	c.Merge(a.Snapshot())
	a.Remove("x")
	// Only B confirms; C is still silent.
	b.Merge(a.Snapshot())
	a.Merge(b.Snapshot())
	if n := a.GC(); n != 0 {
		t.Fatalf("GC collected %d tombstones with C unconfirmed", n)
	}
	// C applies the remove and reports back.
	c.Merge(a.Snapshot())
	a.Merge(c.Snapshot())
	if n := a.GC(); n != 1 {
		t.Fatalf("GC collected %d tombstones after full confirmation, want 1", n)
	}
	if a.Contains("x") {
		t.Fatal("x must stay removed")
	}
}
