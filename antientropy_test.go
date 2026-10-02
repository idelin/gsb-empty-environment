package antientropy

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const testDepth = 8

// scenarioEvents mirrors the demo: 20 events, the two node-down events
// ride on packet seq 1 and 2, which the 90% dropper blocks.
func scenarioEvents() []NodeState {
	events := []NodeState{
		{ID: "node-03", Status: StatusAlive, Version: 1}, // seq 0: delivered
		{ID: "node-03", Status: StatusDead, Version: 2},  // seq 1: DROPPED
		{ID: "node-07", Status: StatusDead, Version: 2},  // seq 2: DROPPED
	}
	ids := []string{"node-00", "node-01", "node-02", "node-04", "node-05", "node-06", "node-08"}
	for _, id := range ids { // seq 3..9: dropped
		events = append(events, NodeState{ID: id, Status: StatusAlive, Version: 1})
	}
	events = append(events, NodeState{ID: "node-07", Status: StatusAlive, Version: 1}) // seq 10: delivered
	for i := 9; i <= 17; i++ {                                                         // seq 11..19: dropped
		events = append(events, NodeState{ID: fmt.Sprintf("node-%02d", i), Status: StatusAlive, Version: 1})
	}
	return events
}

// TestDeterministicDropperBlocks90Percent verifies the dropper blocks
// exactly 90% of the gossip piggyback packets.
func TestDeterministicDropperBlocks90Percent(t *testing.T) {
	a, b := NewStore(), NewStore()
	g := &Gossiper{Local: a, Peers: []*Store{b}, ShouldDrop: DeterministicDropper(10)}
	for i := 0; i < 100; i++ {
		g.Broadcast(NodeState{ID: fmt.Sprintf("n-%03d", i), Status: StatusAlive, Version: 1})
	}
	if g.Sent != 10 || g.Dropped != 90 {
		t.Fatalf("want sent=10 dropped=90, got sent=%d dropped=%d", g.Sent, g.Dropped)
	}
	if b.Len() != 10 {
		t.Fatalf("peer should hold 10 states, got %d", b.Len())
	}
}

// TestAntiEntropyRepairsMissedNodeDownEvents is the acceptance test:
// 90% of gossip packets are blocked, then the background anti-entropy
// coroutine precisely locates and repairs the 2 missed node-down events.
func TestAntiEntropyRepairsMissedNodeDownEvents(t *testing.T) {
	a, b := NewStore(), NewStore()
	g := &Gossiper{Local: a, Peers: []*Store{b}, ShouldDrop: DeterministicDropper(10)}
	for _, ev := range scenarioEvents() {
		g.Broadcast(ev)
	}

	// Preconditions: 90% blocked, B holds only the 2 delivered packets and
	// still believes node-03/node-07 are alive (stale v1).
	if g.Dropped != 18 || g.Sent != 2 {
		t.Fatalf("want sent=2 dropped=18, got sent=%d dropped=%d", g.Sent, g.Dropped)
	}
	if b.Len() != 2 {
		t.Fatalf("B should hold 2 stale states, got %d", b.Len())
	}
	for _, id := range []string{"node-03", "node-07"} {
		st, ok := b.Get(id)
		if !ok || st.Status != StatusAlive {
			t.Fatalf("precondition: B should see %s alive, got %+v (ok=%v)", id, st, ok)
		}
	}
	if a.Len() != 18 {
		t.Fatalf("A should hold 18 states, got %d", a.Len())
	}

	// Start the background anti-entropy coroutine on B (and A for symmetry).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repairerB := &Repairer{Local: b, Peers: []*Store{a}, Depth: testDepth, Interval: 20 * time.Millisecond}
	repairerA := &Repairer{Local: a, Peers: []*Store{b}, Depth: testDepth, Interval: 20 * time.Millisecond}
	go repairerB.Run(ctx)
	go repairerA.Run(ctx)

	// Wait for convergence: identical Merkle roots.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if RootHash(a.Snapshot(), testDepth) == RootHash(b.Snapshot(), testDepth) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if RootHash(a.Snapshot(), testDepth) != RootHash(b.Snapshot(), testDepth) {
		t.Fatal("replicas did not converge within 5s")
	}

	// The 2 missed node-down events must be repaired exactly.
	for _, id := range []string{"node-03", "node-07"} {
		st, ok := b.Get(id)
		if !ok {
			t.Fatalf("B is still missing %s", id)
		}
		if st.Status != StatusDead || st.Version != 2 {
			t.Fatalf("B has %s = %s (v%d), want dead (v2)", id, st.Status, st.Version)
		}
	}
	if b.Len() != 18 {
		t.Fatalf("B should hold all 18 states after repair, got %d", b.Len())
	}
}

// TestMerkleDiffLocatesExactlyDifferingKeys checks that the tree walk
// transfers exactly the differing entries and prunes identical subtrees.
func TestMerkleDiffLocatesExactlyDifferingKeys(t *testing.T) {
	a, b := NewStore(), NewStore()
	for i := 0; i < 50; i++ {
		st := NodeState{ID: fmt.Sprintf("node-%02d", i), Status: StatusAlive, Version: 1}
		a.Apply(st)
		b.Apply(st)
	}
	// Diverge on exactly 2 keys.
	a.Apply(NodeState{ID: "node-11", Status: StatusDead, Version: 2})
	a.Apply(NodeState{ID: "node-42", Status: StatusDead, Version: 2})

	res := b.SyncFrom(a, testDepth)
	if len(res.Applied) != 2 {
		t.Fatalf("want exactly 2 applied entries, got %d: %+v", len(res.Applied), res.Applied)
	}
	got := map[string]NodeState{}
	for _, st := range res.Applied {
		got[st.ID] = st
	}
	for _, id := range []string{"node-11", "node-42"} {
		st, ok := got[id]
		if !ok || st.Status != StatusDead || st.Version != 2 {
			t.Fatalf("missing/wrong repair for %s: %+v", id, st)
		}
	}
	// Pruning: far fewer nodes compared than the 511-node full tree.
	if res.ComparedNodes >= res.TotalNodes {
		t.Fatalf("expected pruned traversal, compared %d of %d nodes", res.ComparedNodes, res.TotalNodes)
	}
	t.Logf("compared %d/%d merkle nodes, %d differing leaf buckets",
		res.ComparedNodes, res.TotalNodes, res.DifferBuckets)
	if RootHash(a.Snapshot(), testDepth) != RootHash(b.Snapshot(), testDepth) {
		t.Fatal("roots should match after sync")
	}
}

// TestLastWriterWins ensures stale gossip cannot overwrite newer state.
func TestLastWriterWins(t *testing.T) {
	s := NewStore()
	s.Apply(NodeState{ID: "node-03", Status: StatusDead, Version: 2})
	if changed := s.Apply(NodeState{ID: "node-03", Status: StatusAlive, Version: 1}); changed {
		t.Fatal("stale v1 must not overwrite v2")
	}
	st, _ := s.Get("node-03")
	if st.Status != StatusDead || st.Version != 2 {
		t.Fatalf("got %+v, want dead v2", st)
	}
}
