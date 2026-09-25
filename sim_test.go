package causal

import "testing"

func simMembers(n NodeID) []NodeID { return []NodeID{"A", "B", "C", "D"} }

// Safety: concurrent messages (disjoint causal pasts) get one stable
// order. Nodes may deliver them in different arrival orders, but the
// canonical linearization computed at every node is identical and
// consistent with causality.
func TestConcurrentMessagesStableOrder(t *testing.T) {
	s := NewSim(Config{
		Members:   simMembers("A"),
		MaxBuffer: 16,
		MinDelay:  1, MaxDelay: 20,
		DupRate:   0.2,
		Seed:      42,
	})
	// A and B broadcast without having seen each other's message.
	s.Broadcast("A", "concurrent a")
	s.Broadcast("B", "concurrent b")
	s.Run()

	reference, err := CanonicalOrder(s.Node("A").Log())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range simMembers("A") {
		log := s.Node(id).Log()
		if err := VerifyLog(log); err != nil {
			t.Fatalf("node %s: %v", id, err)
		}
		order, err := CanonicalOrder(log)
		if err != nil {
			t.Fatalf("node %s: %v", id, err)
		}
		if !SameOrder(reference, order) {
			t.Fatalf("node %s: canonical order diverges: %v vs %v", id, reference, order)
		}
	}
}

// Liveness: with a fair network (no permanent loss) and retransmission
// after backpressure, every broadcast is eventually delivered by every
// node, even with reordering, duplicates and a tiny hold-back buffer.
func TestLivenessEventuallyAllDelivered(t *testing.T) {
	members := simMembers("A")
	s := NewSim(Config{
		Members:   members,
		MaxBuffer: 2, // force backpressure under reordering bursts
		MinDelay:  1, MaxDelay: 30,
		DupRate:    0.5,
		RetryAfter: 5,
		MaxTime:    100000,
		Seed:       1,
	})
	const rounds = 5
	for r := 0; r < rounds; r++ {
		for _, id := range members {
			s.Broadcast(id, "payload")
		}
	}
	s.Run()

	want := len(members) * rounds
	for _, id := range members {
		n := s.Node(id)
		if n.Delivered() != want {
			t.Fatalf("node %s delivered %d of %d (buffered %d)", id, n.Delivered(), want, n.Buffered())
		}
		if n.Buffered() != 0 {
			t.Fatalf("node %s still buffers %d messages", id, n.Buffered())
		}
		if err := VerifyLog(n.Log()); err != nil {
			t.Fatalf("node %s: %v", id, err)
		}
	}
	if s.Stats.Backpressure == 0 {
		t.Fatal("expected the tiny buffer to trigger backpressure at least once")
	}
	if s.Duplicates() == 0 {
		t.Fatal("expected duplicate copies to be exercised")
	}

	// All nodes agree on the canonical order of the same message set.
	reference, err := CanonicalOrder(s.Node(members[0]).Log())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range members[1:] {
		order, err := CanonicalOrder(s.Node(id).Log())
		if err != nil {
			t.Fatal(err)
		}
		if !SameOrder(reference, order) {
			t.Fatalf("node %s canonical order diverges", id)
		}
	}
}

// Safety under faults: across many seeds with drops, duplicates and
// reordering, every delivery log of every node remains a valid causal
// linearization, and delivered sets are always causally closed.
func TestSafetyInvariantUnderFaults(t *testing.T) {
	members := simMembers("A")
	for seed := int64(0); seed < 20; seed++ {
		s := NewSim(Config{
			Members:   members,
			MaxBuffer: 4,
			MinDelay:  1, MaxDelay: 25,
			DropRate:   0.15,
			DupRate:    0.3,
			RetryAfter: 7,
			MaxTime:    5000,
			Seed:       seed,
		})
		for r := 0; r < 4; r++ {
			for _, id := range members {
				s.Broadcast(id, "x")
			}
		}
		s.Run()
		for _, id := range members {
			if err := VerifyLog(s.Node(id).Log()); err != nil {
				t.Fatalf("seed %d node %s: %v", seed, id, err)
			}
		}
	}
}
