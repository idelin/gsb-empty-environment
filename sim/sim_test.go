package sim

import (
	"reflect"
	"testing"
)

// Liveness: despite message loss, duplication, reordering (random delays)
// and a heal-able partition, periodic gossip drives all replicas to
// identical state (strong eventual consistency).
func TestEventualConvergenceUnderFaults(t *testing.T) {
	s := New(42, "A", "B", "C")
	s.Loss = 0.30 // 30% message loss
	s.Dup = 0.20  // 20% duplication

	// Phase 1: healthy cluster, some ops.
	s.Add(1, "A", "apple")
	s.Add(2, "B", "banana")
	s.GossipAll(3, 4)

	// Phase 2: partition C away; concurrent ops on both sides.
	s.Partition(10, []string{"C"}, []string{"A", "B"})
	s.Add(11, "C", "cherry")
	s.Remove(12, "A", "apple") // A cannot see C's concurrent add
	s.Add(13, "C", "apple")    // concurrent re-add of apple by C
	s.GossipEvery(14, 3, 3, 3) // gossip attempts during partition (C's msgs dropped)

	// Phase 3: heal; gossip until quiescent.
	s.Heal(30)
	s.GossipEvery(31, 3, 20, 3)
	s.Run()

	if !s.Converged() {
		t.Fatalf("replicas did not converge; log:\n%v", s.Log)
	}
	// cherry survives (never removed); apple survives via C's unobserved
	// concurrent add; banana survives.
	want := []string{"apple", "banana", "cherry"}
	for _, id := range s.IDs() {
		if got := s.Elements(id); !reflect.DeepEqual(got, want) {
			t.Fatalf("replica %s has %v, want %v", id, got, want)
		}
	}
	if s.Delivered == 0 || s.Dropped == 0 {
		t.Fatalf("fault injection not exercised: delivered=%d dropped=%d", s.Delivered, s.Dropped)
	}
	t.Logf("delivered=%d dropped=%d (loss+partition)", s.Delivered, s.Dropped)
}

// Safety invariant: once an element is removed by a replica that observed
// ALL its add-tags (no concurrent adds), it never reappears at any
// replica, under arbitrary message reordering/loss/duplication.
func TestNoResurrectionInvariant(t *testing.T) {
	s := New(7, "A", "B", "C")
	s.Loss = 0.2
	s.Dup = 0.2

	s.Add(1, "A", "x")
	s.GossipEvery(2, 2, 4, 2) // everyone observes x's only tag
	s.Remove(10, "B", "x")    // B removes x after full observation
	s.GCAll(11)               // too early: must collect nothing
	s.GossipEvery(12, 2, 15, 3)
	s.GCAll(60)
	s.GossipEvery(61, 2, 5, 2)
	s.Run()

	for _, id := range s.IDs() {
		if s.Replicas[id].Contains("x") {
			t.Fatalf("SAFETY VIOLATION: x resurrected at %s", id)
		}
	}
	if !s.Converged() {
		t.Fatal("replicas did not converge")
	}
	// Liveness of GC: once the remove is causally stable everywhere,
	// tombstones (and dead add-dots) are eventually collected.
	for _, id := range s.IDs() {
		if n := s.Replicas[id].TombCount(); n != 0 {
			t.Fatalf("replica %s still holds %d tombstones after causal stability", id, n)
		}
		if n := s.Replicas[id].AddDotCount(); n != 0 {
			t.Fatalf("replica %s still holds %d dead add-dots", id, n)
		}
	}
}

// Early GC is a no-op: while a partition prevents causal confirmation,
// GC collects nothing and the tombstone keeps protecting the remove.
func TestGCNoOpDuringPartition(t *testing.T) {
	s := New(1, "A", "B", "C")

	s.Add(1, "A", "x")
	s.GossipAll(2, 1) // B, C observe x
	s.Partition(5, []string{"C"}, []string{"A", "B"})
	s.Remove(6, "A", "x")
	s.GossipAll(7, 1) // B learns the remove; C is isolated
	s.GCAll(10)       // C unreachable -> remove not stable -> collect nothing
	s.Run()

	for _, id := range []string{"A", "B"} {
		if n := s.Replicas[id].TombCount(); n != 1 {
			t.Fatalf("%s: tombstone collected during partition (tombs=%d)", id, n)
		}
		if s.Replicas[id].Contains("x") {
			t.Fatalf("%s: x should be removed", id)
		}
	}
	// C never saw the remove: x is still live there.
	if !s.Replicas["C"].Contains("x") {
		t.Fatal("C should still see x (it missed the remove)")
	}

	// Heal and gossip: C learns the remove; once confirmation flows back,
	// GC collects everywhere and x never returns.
	s.Heal(20)
	s.GossipEvery(21, 2, 8, 2)
	s.GCAll(40)
	s.GossipEvery(41, 2, 3, 1)
	s.GCAll(50)
	s.Run()

	if !s.Converged() {
		t.Fatal("replicas did not converge after heal")
	}
	for _, id := range s.IDs() {
		if s.Replicas[id].Contains("x") {
			t.Fatalf("x resurrected at %s after heal", id)
		}
		if n := s.Replicas[id].TombCount(); n != 0 {
			t.Fatalf("%s: %d tombstones left after stable GC", id, n)
		}
	}
}

// Duplicate delivery of the same message changes nothing (idempotence at
// the transport level), and duplicate removes are no-ops end to end.
func TestDuplicateDeliveryAndRemove(t *testing.T) {
	s := New(3, "A", "B")
	s.Dup = 1.0 // every message duplicated

	s.Add(1, "A", "x")
	s.GossipAll(2, 1)
	s.Remove(5, "B", "x")
	s.Remove(6, "B", "x") // duplicate remove
	s.GossipAll(7, 1)
	s.Run()

	if !s.Converged() {
		t.Fatal("duplicated messages broke convergence")
	}
	for _, id := range s.IDs() {
		if s.Replicas[id].Contains("x") {
			t.Fatalf("x live at %s after remove", id)
		}
		if n := s.Replicas[id].TombCount(); n != 1 {
			t.Fatalf("%s: duplicate remove created extra tombstones (tombs=%d)", id, n)
		}
	}
}
