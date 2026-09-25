package orset

import (
	"reflect"
	"testing"
)

var cluster = []string{"A", "B", "C"}

// sync merges src's snapshot into dst, as one gossip step.
func sync(dst, src *ORSet) {
	dst.Merge(src.Snapshot())
}

// --- Merge satisfies the semilattice laws --------------------------------

func TestMergeCommutativeAssociativeIdempotent(t *testing.T) {
	build := func(ops func(*ORSet)) *ORSet {
		s := New("X", cluster)
		ops(s)
		return s
	}
	a := build(func(s *ORSet) { s.Add("apple"); s.Add("pear"); s.Remove("pear") })
	b := build(func(s *ORSet) { s.Add("fig"); s.Add("apple") })
	c := build(func(s *ORSet) { s.Add("kiwi"); s.Remove("fig") }) // removes nothing observed

	sa, sb, sc := a.Snapshot(), b.Snapshot(), c.Snapshot()

	merge := func(snaps ...Snapshot) Snapshot {
		r := New("R", cluster)
		for _, sn := range snaps {
			r.Merge(sn)
		}
		return r.Snapshot()
	}

	// Idempotent: s ∪ s = s
	if !merge(sa, sa).CoreEqual(merge(sa)) {
		t.Fatal("merge not idempotent")
	}
	// Commutative: a ∪ b = b ∪ a
	if !merge(sa, sb).CoreEqual(merge(sb, sa)) {
		t.Fatal("merge not commutative")
	}
	// Associative: (a ∪ b) ∪ c = a ∪ (b ∪ c)
	left := New("L", cluster)
	left.Merge(sa)
	left.Merge(sb)
	left.Merge(sc)
	right := New("Q", cluster)
	right.Merge(merge(sb, sc))
	right.Merge(sa)
	if !left.Snapshot().CoreEqual(right.Snapshot()) {
		t.Fatal("merge not associative")
	}
	// Incremental merges reach the same state as a bulk merge.
	if !merge(sa, sb, sc).CoreEqual(left.Snapshot()) {
		t.Fatal("incremental and bulk merge differ")
	}
}

// --- Remove only affects observed tags ------------------------------------

func TestConcurrentAddSurvivesRemove(t *testing.T) {
	a, b := New("A", cluster), New("B", cluster)

	a.Add("x")
	sync(b, a) // B observes A's add

	b.Add("x") // concurrent-ish add: B's own tag
	a.Remove("x")

	// A observed only its own tag: exactly one tombstone, x gone at A.
	if a.TombCount() != 1 {
		t.Fatalf("expected 1 tombstone at A, got %d", a.TombCount())
	}
	if a.Contains("x") {
		t.Fatal("x should be removed at A")
	}

	// B never observed the remove; its own tag is still live.
	if !b.Contains("x") {
		t.Fatal("x should still be live at B")
	}

	// After merging both ways, B's unobserved add keeps x alive everywhere.
	sync(a, b)
	sync(b, a)
	if !a.Contains("x") || !b.Contains("x") {
		t.Fatal("concurrent (unobserved) add must survive the remove")
	}
	if live := a.LiveDots("x"); len(live) != 1 || live[0].Replica != "B" {
		t.Fatalf("only B's tag should be live, got %v", live)
	}
}

func TestRemoveOnlyObservedTags(t *testing.T) {
	a, b := New("A", cluster), New("B", cluster)

	a.Add("y")    // tag A:1
	b.Add("y")    // tag B:1 — concurrent, A has NOT seen it
	a.Remove("y") // A has observed only A:1

	if n := a.TombCount(); n != 1 {
		t.Fatalf("remove must tombstone exactly the observed tags; got %d tombstones", n)
	}
	// Merging B's concurrent add resurrects y with B's tag only.
	sync(a, b)
	if !a.Contains("y") {
		t.Fatal("unobserved concurrent add must survive")
	}
	live := a.LiveDots("y")
	if len(live) != 1 || live[0] != (Dot{Replica: "B", Seq: 1}) {
		t.Fatalf("expected only B:1 live, got %v", live)
	}
}

func TestDuplicateRemoveIsNoOp(t *testing.T) {
	a := New("A", cluster)
	a.Add("x")
	a.Add("z")
	a.Remove("x")

	addsBefore := a.Snapshot().Adds
	tombsBefore := a.Snapshot().Tombs

	_, n := a.Remove("x") // duplicate: nothing new observed
	if n != 0 {
		t.Fatalf("duplicate remove tombstoned %d tags, want 0", n)
	}
	if !reflect.DeepEqual(addsBefore, a.Snapshot().Adds) ||
		!reflect.DeepEqual(tombsBefore, a.Snapshot().Tombs) {
		t.Fatal("duplicate remove changed the data payload")
	}
	if a.Contains("x") || !a.Contains("z") {
		t.Fatal("membership wrong after duplicate remove")
	}
}

// --- Garbage collection ----------------------------------------------------

// Counterexample: dropping a tombstone BEFORE the remove is causally
// stable lets a stale replica reintroduce the element.
func TestPrematureTombstoneDropResurrects(t *testing.T) {
	a, b := New("A", cluster), New("B", cluster)

	a.Add("x")
	sync(b, a)    // B observes x
	a.Remove("x") // A removes x; B has NOT seen the remove

	// BUG: A drops its tombstone without causal confirmation.
	if n := a.DropTombstonesUnsafe(); n != 1 {
		t.Fatalf("unsafe GC should have dropped 1 tombstone, dropped %d", n)
	}

	// B (still holding x as live) gossips to A: x comes back from the dead.
	sync(a, b)
	if !a.Contains("x") {
		t.Fatal("expected resurrection anomaly: x should be live again at A")
	}
	// And the resurrection propagates back to B's future peers.
	c := New("C", cluster)
	sync(c, a)
	if !c.Contains("x") {
		t.Fatal("resurrected x should propagate to C")
	}
	t.Log("premature tombstone drop caused removed element x to resurrect")
}

// The same scenario with causally-safe GC: no resurrection, and the
// tombstone is collected only once every replica has observed the remove.
func TestCausalGCPreventsResurrection(t *testing.T) {
	pair := []string{"A", "B"}
	a, b := New("A", pair), New("B", pair)

	a.Add("x")
	sync(b, a)    // B observes x
	a.Remove("x") // A removes x

	// GC too early: B has not observed the remove, so nothing is collected.
	if n := a.GC(); n != 0 {
		t.Fatalf("GC collected %d tombstones before causal stability", n)
	}
	if a.TombCount() != 1 {
		t.Fatal("tombstone must be retained until causally stable")
	}

	// B learns of the remove, then A learns that B knows (matrix clock).
	sync(b, a)
	sync(a, b)
	if n := a.GC(); n != 1 {
		t.Fatalf("GC should collect 1 stable tombstone, got %d", n)
	}
	if a.TombCount() != 0 || a.AddDotCount() != 0 {
		t.Fatal("stable tombstone and its dead add-dot should be gone")
	}

	// No matter who gossips now, x never comes back.
	sync(a, b)
	sync(b, a)
	if a.Contains("x") || b.Contains("x") {
		t.Fatal("element resurrected despite causal GC")
	}
}

// GC under a partition: a replica isolated from the cluster must not
// collect tombstones, because it cannot confirm causal stability.
func TestGCBlockedDuringPartition(t *testing.T) {
	a, b, c := New("A", cluster), New("B", cluster), New("C", cluster)

	a.Add("x")
	sync(b, a)
	sync(c, a)
	a.Remove("x")
	// B and C observe the remove, but A never hears back from C.
	sync(b, a)
	sync(c, a)
	sync(a, b) // A learns B's vv, but nothing about C

	if n := a.GC(); n != 0 {
		t.Fatalf("GC collected %d tombstones without hearing from all replicas", n)
	}

	// Once C's knowledge reaches A (even transitively via B), GC proceeds.
	sync(b, c)
	sync(a, b)
	if n := a.GC(); n != 1 {
		t.Fatalf("GC should collect after transitive confirmation, got %d", n)
	}
}

// Concurrent removes of the same element converge and stay removed.
func TestConcurrentRemovesConverge(t *testing.T) {
	a, b := New("A", cluster), New("B", cluster)
	a.Add("x")
	sync(b, a) // both observe x

	a.Remove("x")
	b.Remove("x") // concurrent remove

	sync(a, b)
	sync(b, a)
	if a.Contains("x") || b.Contains("x") {
		t.Fatal("x must stay removed after concurrent removes")
	}
	if !a.Snapshot().CoreEqual(b.Snapshot()) {
		t.Fatal("replicas must converge after concurrent removes")
	}
	// Duplicate tombstone entries for the same add-dot resolve deterministically.
	if a.TombCount() != 1 {
		t.Fatalf("expected 1 deduplicated tombstone, got %d", a.TombCount())
	}
}
