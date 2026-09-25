package sim

import (
	"reflect"
	"testing"
)

// gossipEvery schedules periodic gossip rounds on [from, to).
func gossipEvery(period, from, to uint64) func(*Sim) {
	return func(s *Sim) {
		for t := from; t <= to; t += period {
			s.At(t, func(s *Sim) { s.Gossip() })
		}
	}
}

// Liveness: despite a partition with concurrent conflicting ops, once the
// partition heals and messages flow, all replicas converge to the state
// dictated, and the concurrent (unobserved) add survives the remove.
func TestEventualConvergenceAfterPartition(t *testing.T) {
	s := New(1, "A", "B", "C")

	s.At(1, func(s *Sim) { s.Nodes["A"].Set.Add("apple") })
	s.At(2, func(s *Sim) { s.Gossip() })
	s.At(4, func(s *Sim) { s.Gossip() }) // everyone sees apple

	// Partition A away from B and C.
	s.At(6, func(s *Sim) {
		s.Partition("A", "B")
		s.Partition("A", "C")
	})
	// Concurrent, unobserved ops on both sides of the partition.
	s.At(7, func(s *Sim) { s.Nodes["A"].Set.Remove("apple") }) // A removes
	s.At(7, func(s *Sim) { s.Nodes["B"].Set.Add("apple") })    // B re-adds concurrently
	s.At(8, func(s *Sim) { s.Nodes["C"].Set.Add("cherry") })
	s.At(9, func(s *Sim) { s.Gossip() }) // only B<->C can exchange

	// Heal and gossip until quiescence.
	s.At(12, func(s *Sim) {
		s.Heal("A", "B")
		s.Heal("A", "C")
	})
	gossipEvery(2, 13, 40)(s)

	s.Run(40)

	if !s.Converged() {
		t.Fatal("replicas did not converge after heal")
	}
	want := []string{"apple", "cherry"} // concurrent add wins; cherry propagates
	for _, id := range []string{"A", "B", "C"} {
		if got := s.Elements(id); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s elements = %v, want %v", id, got, want)
		}
	}
}

// Safety+liveness under adverse networks: loss, duplication and reordering
// must not prevent convergence (merge is idempotent and commutative).
func TestConvergenceUnderLossDupReorder(t *testing.T) {
	s := New(7, "A", "B", "C")
	s.Loss = 0.3
	s.Dup = 0.3
	s.MaxDelay = 8 // large random delays => reordering

	s.At(1, func(s *Sim) {
		s.Nodes["A"].Set.Add("a1")
		s.Nodes["B"].Set.Add("b1")
		s.Nodes["C"].Set.Add("c1")
		s.Nodes["C"].Set.Remove("c1")
	})
	gossipEvery(3, 2, 90)(s)
	s.Run(90)

	if !s.Converged() {
		t.Fatal("replicas did not converge under loss/dup/reorder")
	}
	want := []string{"a1", "b1"}
	for _, id := range []string{"A", "B", "C"} {
		if got := s.Elements(id); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s elements = %v, want %v", id, got, want)
		}
	}
}

// Crash/recover: a crashed node misses messages but catches up after
// recovery; a remove issued before the crash never resurrects.
func TestCrashRecover(t *testing.T) {
	s := New(3, "A", "B")

	s.At(1, func(s *Sim) { s.Nodes["A"].Set.Add("x") })
	s.At(2, func(s *Sim) { s.Gossip() })
	s.At(5, func(s *Sim) { s.Crash("B") })
	s.At(6, func(s *Sim) {
		s.Nodes["A"].Set.Remove("x")
		s.Nodes["A"].Set.Add("y")
	})
	s.At(7, func(s *Sim) { s.Gossip() }) // B drops everything
	s.At(10, func(s *Sim) { s.Recover("B") })
	gossipEvery(2, 11, 30)(s)
	s.Run(30)

	if !s.Converged() {
		t.Fatal("replicas did not converge after recovery")
	}
	want := []string{"y"}
	for _, id := range []string{"A", "B"} {
		if got := s.Elements(id); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s elements = %v, want %v", id, got, want)
		}
	}
}

// GC liveness: once the remove has causally propagated to every replica
// and their version vectors have gossiped back, tombstones are collected
// everywhere and the element stays removed.
func TestGarbageCollectionLiveness(t *testing.T) {
	s := New(5, "A", "B", "C")

	s.At(1, func(s *Sim) { s.Nodes["A"].Set.Add("doomed") })
	s.At(2, func(s *Sim) { s.Gossip() })
	s.At(4, func(s *Sim) { s.Gossip() })
	s.At(6, func(s *Sim) { s.Nodes["B"].Set.Remove("doomed") })
	gossipEvery(2, 7, 30)(s)
	s.Run(30)
	s.Gossip()
	s.RunAll() // drain in-flight messages

	if !s.Converged() {
		t.Fatal("replicas did not converge")
	}
	for _, id := range []string{"A", "B", "C"} {
		n := s.Nodes[id].Set.Tombstones()
		if n != 1 {
			t.Fatalf("%s holds %d tombstones before GC, want 1", id, n)
		}
	}

	// GC everywhere, then keep gossiping: nothing comes back. A second
	// round is needed because messages already in flight during the first
	// round may legitimately re-deliver the tombstone.
	for _, id := range []string{"A", "B", "C"} {
		if got := s.Nodes[id].Set.GC(); got != 1 {
			t.Fatalf("%s GC collected %d tombstones, want 1", id, got)
		}
	}
	s.Gossip()
	s.RunAll()
	for _, id := range []string{"A", "B", "C"} {
		s.Nodes[id].Set.GC()
	}
	s.Gossip()
	s.RunAll()

	if !s.Converged() {
		t.Fatal("replicas diverged after GC")
	}
	for _, id := range []string{"A", "B", "C"} {
		if s.Nodes[id].Set.Contains("doomed") {
			t.Fatalf("%s: removed element resurrected after GC", id)
		}
		if n := s.Nodes[id].Set.Tombstones(); n != 0 {
			t.Fatalf("%s holds %d tombstones after GC, want 0", id, n)
		}
	}
}

// Determinism: same seed, same run, same result.
func TestDeterministicReplay(t *testing.T) {
	run := func() []string {
		s := New(42, "A", "B", "C")
		s.Loss, s.Dup, s.MaxDelay = 0.2, 0.2, 6
		s.At(1, func(s *Sim) { s.Nodes["A"].Set.Add("x") })
		s.At(1, func(s *Sim) { s.Nodes["B"].Set.Add("y") })
		gossipEvery(2, 2, 30)(s)
		s.Run(30)
		return s.Elements("C")
	}
	if !reflect.DeepEqual(run(), run()) {
		t.Fatal("same seed produced different runs")
	}
}
