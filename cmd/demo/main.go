// Demo: 90% of gossip piggyback packets are blocked, then the background
// anti-entropy coroutine repairs the two missed node-down events by
// walking the Merkle tree top-down.
package main

import (
	"context"
	"fmt"
	"time"

	"antientropy"
)

const depth = 8 // 256 leaf buckets

// scenarioEvents builds 20 events. The two node-down events ride on
// packet seq 1 and 2, which the deterministic 90% dropper blocks.
func scenarioEvents() []antientropy.NodeState {
	events := []antientropy.NodeState{
		{ID: "node-03", Status: antientropy.StatusAlive, Version: 1}, // seq 0: delivered
		{ID: "node-03", Status: antientropy.StatusDead, Version: 2},  // seq 1: DROPPED
		{ID: "node-07", Status: antientropy.StatusDead, Version: 2},  // seq 2: DROPPED
	}
	ids := []string{"node-00", "node-01", "node-02", "node-04", "node-05", "node-06", "node-08"}
	for _, id := range ids { // seq 3..9: dropped
		events = append(events, antientropy.NodeState{ID: id, Status: antientropy.StatusAlive, Version: 1})
	}
	events = append(events, antientropy.NodeState{ID: "node-07", Status: antientropy.StatusAlive, Version: 1}) // seq 10: delivered (stale v1, ignored by A)
	for i := 9; i <= 17; i++ {                                                                                 // seq 11..19: dropped
		events = append(events, antientropy.NodeState{ID: fmt.Sprintf("node-%02d", i), Status: antientropy.StatusAlive, Version: 1})
	}
	return events
}

func main() {
	replicaA := antientropy.NewStore()
	replicaB := antientropy.NewStore()

	// Phase 1: gossip with 90% of piggyback packets deterministically blocked.
	gossip := &antientropy.Gossiper{
		Local:      replicaA,
		Peers:      []*antientropy.Store{replicaB},
		ShouldDrop: antientropy.DeterministicDropper(10),
	}
	for _, ev := range scenarioEvents() {
		gossip.Broadcast(ev)
	}
	fmt.Printf("[gossip] packets sent=%d dropped=%d (%.0f%% blocked)\n",
		gossip.Sent, gossip.Dropped, 100*float64(gossip.Dropped)/float64(gossip.Sent+gossip.Dropped))
	fmt.Printf("[gossip] replica A holds %d states, replica B holds %d states\n", replicaA.Len(), replicaB.Len())
	for _, id := range []string{"node-03", "node-07"} {
		st, _ := replicaB.Get(id)
		fmt.Printf("[gossip] B's stale view: %s = %s (v%d)  <-- missed the node-down event\n", id, st.Status, st.Version)
	}

	// Phase 2: start the background anti-entropy coroutine on both replicas.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logf := func(format string, args ...any) { fmt.Printf(format+"\n", args...) }
	// One repairer is enough: each round reconciles both directions.
	repairerB := &antientropy.Repairer{Local: replicaB, Peers: []*antientropy.Store{replicaA}, Depth: depth, Interval: 100 * time.Millisecond, Logf: logf}
	go repairerB.Run(ctx)

	// Wait for convergence (identical Merkle roots).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if antientropy.RootHash(replicaA.Snapshot(), depth) == antientropy.RootHash(replicaB.Snapshot(), depth) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	fmt.Printf("[anti-entropy] converged: A=%d states, B=%d states, merkle roots equal=%v\n",
		replicaA.Len(), replicaB.Len(),
		antientropy.RootHash(replicaA.Snapshot(), depth) == antientropy.RootHash(replicaB.Snapshot(), depth))
	for _, id := range []string{"node-03", "node-07"} {
		st, _ := replicaB.Get(id)
		fmt.Printf("[anti-entropy] B's repaired view: %s = %s (v%d)\n", id, st.Status, st.Version)
	}
}
