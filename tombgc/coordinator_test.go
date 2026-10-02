package tombgc

import (
	"fmt"
	"testing"
)

func TestVersionVectorMergeAndDominate(t *testing.T) {
	a := VersionVector{"A": 3, "B": 1}
	b := VersionVector{"B": 5, "C": 2}
	a.Merge(b)
	if a["A"] != 3 || a["B"] != 5 || a["C"] != 2 {
		t.Fatalf("unexpected merge result: %v", a)
	}
	if !a.Dominates(VersionVector{"A": 3, "B": 4}) {
		t.Fatal("expected a to dominate {A:3 B:4}")
	}
	if a.Dominates(VersionVector{"A": 4}) {
		t.Fatal("did not expect a to dominate {A:4}")
	}
	if a.Dominates(VersionVector{"D": 1}) {
		t.Fatal("did not expect a to dominate a vector with an unknown node")
	}
}

func TestMinFrontier(t *testing.T) {
	m := Min(
		VersionVector{"A": 10, "B": 10},
		VersionVector{"A": 7, "B": 12},
		VersionVector{"A": 9, "B": 8},
	)
	if m["A"] != 7 || m["B"] != 8 {
		t.Fatalf("unexpected min frontier: %v", m)
	}
}

func TestStabilityFrontierTracksSlowestNode(t *testing.T) {
	c := NewCoordinator("A", "B", "C")
	c.Observe(VersionVector{"A": 50, "B": 50, "C": 50})
	c.ReceiveFrontier("B", VersionVector{"A": 50, "B": 50, "C": 50})
	c.ReceiveFrontier("C", VersionVector{"A": 49, "B": 49, "C": 49})

	f := c.StabilityFrontier()
	if f["A"] != 49 || f["B"] != 49 || f["C"] != 49 {
		t.Fatalf("stability frontier should track the slowest node, got %v", f)
	}
}

func TestGCRetainsUnstableTombstones(t *testing.T) {
	c := NewCoordinator("A", "B")
	c.Observe(VersionVector{"A": 50, "B": 50})
	c.ReceiveFrontier("B", VersionVector{"A": 10, "B": 10}) // B lags behind

	c.AddTombstone(Tombstone{Key: "old", Version: VersionVector{"A": 20}, SizeBytes: 100})
	stats := c.CollectGarbage()
	if stats.Reclaimed != 0 || c.TombstoneCount() != 1 {
		t.Fatalf("no tombstone may be reclaimed while a peer lags: %+v", stats)
	}

	// Once B catches up past the delete, the tombstone becomes collectable.
	c.ReceiveFrontier("B", VersionVector{"A": 50, "B": 50})
	stats = c.CollectGarbage()
	if stats.Reclaimed != 1 || stats.ReclaimedBytes != 100 || c.TombstoneCount() != 0 {
		t.Fatalf("expected tombstone reclaimed after stability, got %+v", stats)
	}
}

func TestGCIsIdempotent(t *testing.T) {
	c := NewCoordinator("A", "B")
	c.Observe(VersionVector{"A": 50, "B": 50})
	c.ReceiveFrontier("B", VersionVector{"A": 50, "B": 50})
	c.AddTombstone(Tombstone{Key: "k", Version: VersionVector{"A": 1}, SizeBytes: 10})

	if s := c.CollectGarbage(); s.Reclaimed != 1 {
		t.Fatalf("first pass should reclaim 1 tombstone, got %+v", s)
	}
	if s := c.CollectGarbage(); s.Reclaimed != 0 || s.ReclaimedBytes != 0 {
		t.Fatalf("second pass should reclaim nothing, got %+v", s)
	}
}

// TestClusterReclaimsTombstonesBelowT50 is the acceptance scenario: a
// 5-node cluster where, once nodes A and B have confirmed that every other
// node has advanced past T=50, all tombstones with T<50 are physically
// reclaimed and cluster tombstone memory drops by at least 90%.
func TestClusterReclaimsTombstonesBelowT50(t *testing.T) {
	ids := []string{"A", "B", "C", "D", "E"}
	cl := NewCluster(ids...)

	// Every node accumulates tombstones: 100 stale deletes at T in [1,49]
	// and 5 recent deletes at T in [51,60]. Stale deletes account for
	// ~95% of the tombstone memory footprint.
	const stalePerNode, recentPerNode = 100, 5
	const sizeBytes = 1024
	for _, id := range ids {
		n := cl.Nodes[id]
		for i := 0; i < stalePerNode; i++ {
			ts := uint64(i%49) + 1 // T in [1,49]
			n.AddTombstone(Tombstone{
				Key:       fmt.Sprintf("%s/stale-%d", id, i),
				Version:   VersionVector{id: ts},
				SizeBytes: sizeBytes,
			})
		}
		for i := 0; i < recentPerNode; i++ {
			ts := uint64(51 + i) // T in [51,55]
			n.AddTombstone(Tombstone{
				Key:       fmt.Sprintf("%s/recent-%d", id, i),
				Version:   VersionVector{id: ts},
				SizeBytes: sizeBytes,
			})
		}
	}

	before := cl.TotalTombstoneBytes()
	wantBefore := int64(len(ids)*(stalePerNode+recentPerNode)) * sizeBytes
	if before != wantBefore {
		t.Fatalf("before GC: got %d bytes, want %d", before, wantBefore)
	}

	// GC before the frontier moves must reclaim nothing.
	for _, stats := range cl.CollectGarbage() {
		if stats.Reclaimed != 0 {
			t.Fatalf("premature reclaim before causal stability: %+v", stats)
		}
	}

	// All nodes advance past T=50 and gossip their frontiers.
	for _, id := range ids {
		cl.Advance(id, 50)
	}
	cl.GossipAll()

	// Nodes A and B now confirm every node has passed T=50.
	for _, id := range []string{"A", "B"} {
		f := cl.Nodes[id].StabilityFrontier()
		for _, n := range ids {
			if f[n] < 50 {
				t.Fatalf("node %s sees %s at T=%d, want >= 50", id, n, f[n])
			}
		}
	}

	// The coordinated GC physically reclaims every tombstone with T<50.
	stats := cl.CollectGarbage()
	after := cl.TotalTombstoneBytes()

	var reclaimedTotal int
	for id, s := range stats {
		reclaimedTotal += s.Reclaimed
		if s.Reclaimed != stalePerNode {
			t.Fatalf("node %s reclaimed %d tombstones, want %d", id, s.Reclaimed, stalePerNode)
		}
		if s.Remaining != recentPerNode {
			t.Fatalf("node %s retained %d tombstones, want %d", id, s.Remaining, recentPerNode)
		}
	}
	if reclaimedTotal != len(ids)*stalePerNode {
		t.Fatalf("cluster reclaimed %d tombstones, want %d", reclaimedTotal, len(ids)*stalePerNode)
	}

	drop := float64(before-after) / float64(before)
	t.Logf("tombstone memory: before=%d bytes after=%d bytes drop=%.1f%%", before, after, drop*100)
	if drop < 0.90 {
		t.Fatalf("memory drop %.1f%% is below the required 90%%", drop*100)
	}
}
