package main

import "testing"

// Acceptance test: 5-node cluster, coordinators A and B confirm every
// node has passed T=50, then all tombstones with T<50 are physically
// reclaimed and cluster memory drops by at least 90%.
func TestTombstoneGCReclaimsStableTombstones(t *testing.T) {
	nodes := buildCluster()
	coord := NewCoordinator(nodes, "A", "B")

	// Precondition: coordinators A and B both confirm stability at T=50.
	for _, id := range []string{"A", "B"} {
		var n *Node
		for _, cand := range nodes {
			if cand.ID == id {
				n = cand
			}
		}
		if !n.StableAt(gcThreshold, len(nodes)) {
			t.Fatalf("coordinator %s does not see cluster stable at T=%d", id, gcThreshold)
		}
	}

	stats := coord.RunGC(gcThreshold)

	if !stats.Stable {
		t.Fatal("GC did not run although the frontier is stable")
	}

	// After gossip every node holds a replica of all 5*22=110 distinct
	// tombstones, so cluster-wide reclamation removes 5*110=550 copies.
	wantReclaimed := len(nodes) * len(nodes) * oldTombstonesPerNode
	if stats.Reclaimed != wantReclaimed {
		t.Errorf("reclaimed %d tombstones, want %d", stats.Reclaimed, wantReclaimed)
	}

	if got := stats.ReductionPct(); got < 90.0 {
		t.Errorf("memory reduction %.1f%%, want >= 90%% (before=%d after=%d)",
			got, stats.MemBefore, stats.MemAfter)
	}

	// All tombstones with T<50 must be physically gone on every node.
	for _, n := range nodes {
		for _, e := range n.Store.Entries() {
			if e.Tombstone && e.Dot.Clock < gcThreshold {
				t.Errorf("node %s still holds stale tombstone %s (T=%d)",
					n.ID, e.Key, e.Dot.Clock)
			}
		}
	}

	// Live entries and late tombstones (T>=50) must survive.
	for _, n := range nodes {
		for i := 0; i < liveKeysPerNode; i++ {
			e, ok := n.Store.Get(keyOf("live", "A", i))
			if !ok || e.Tombstone {
				t.Errorf("node %s lost live entry %s", n.ID, keyOf("live", "A", i))
			}
		}
		for i := 0; i < lateTombstones; i++ {
			e, ok := n.Store.Get(keyOf("late", "A", i))
			if !ok || !e.Tombstone {
				t.Errorf("node %s lost late tombstone %s (T>=50 must be kept)",
					n.ID, keyOf("late", "A", i))
			}
		}
	}
}

// GC must not run while any node has not passed the threshold: here node
// E is isolated and stuck below T=50, so coordinators cannot confirm
// stability and no tombstone may be reclaimed.
func TestNoGCWhenClusterNotStable(t *testing.T) {
	ids := []string{"A", "B", "C", "D", "E"}
	nodes := make([]*Node, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, NewNode(id))
	}
	for _, n := range nodes {
		for i := 0; i < 5; i++ {
			key := keyOf("old", n.ID, i)
			n.Store.Put(key, []byte("v"))
			n.Store.Delete(key)
		}
	}
	// Advance A-D past T=50; E stays at T=10 and never gossips.
	for _, n := range nodes[:4] {
		for i := 0; i < 45; i++ {
			n.Store.Put(keyOf("pad", n.ID, i), []byte("v"))
		}
	}
	for round := 0; round < 3; round++ {
		for i := 0; i < 4; i++ {
			for j := i + 1; j < 4; j++ {
				nodes[i].Gossip(nodes[j])
			}
		}
	}

	coord := NewCoordinator(nodes, "A", "B")
	stats := coord.RunGC(gcThreshold)

	if stats.Stable {
		t.Fatal("GC ran even though node E has not passed T=50")
	}
	if stats.Reclaimed != 0 {
		t.Errorf("reclaimed %d tombstones while unstable, want 0", stats.Reclaimed)
	}
	if stats.MemAfter != 0 {
		t.Errorf("memory changed during unstable GC pass: after=%d", stats.MemAfter)
	}
	// Tombstones must still be present.
	for _, n := range nodes {
		for i := 0; i < 5; i++ {
			if e, ok := n.Store.Get(keyOf("old", n.ID, i)); !ok || !e.Tombstone {
				t.Errorf("node %s lost tombstone %s during unstable pass", n.ID, keyOf("old", n.ID, i))
			}
		}
	}
}

func TestVectorClockMergeAndDominates(t *testing.T) {
	a := VectorClock{"A": 3, "B": 1}
	b := VectorClock{"A": 2, "B": 5}
	a.Merge(b)
	if a["A"] != 3 || a["B"] != 5 {
		t.Errorf("merge produced %v, want {A:3 B:5}", a)
	}
	if !a.Dominates(Dot{Node: "B", Clock: 5}) {
		t.Error("clock should dominate dot (B,5)")
	}
	if a.Dominates(Dot{Node: "B", Clock: 6}) {
		t.Error("clock should not dominate dot (B,6)")
	}
	if a.Dominates(Dot{Node: "C", Clock: 1}) {
		t.Error("clock should not dominate unseen node C")
	}
}

func TestFrontierIsComponentWiseMin(t *testing.T) {
	f := Frontier(
		VectorClock{"A": 10, "B": 7},
		VectorClock{"A": 12, "B": 9},
		VectorClock{"A": 8, "B": 20},
	)
	if f["A"] != 8 || f["B"] != 7 {
		t.Errorf("frontier = %v, want {A:8 B:7}", f)
	}
	// A member that has never been heard of drags the frontier to 0.
	f = Frontier(VectorClock{"A": 10}, VectorClock{})
	if f["A"] != 0 {
		t.Errorf("frontier with missing clock = %v, want {A:0}", f)
	}
}
