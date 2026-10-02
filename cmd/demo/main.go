// Command demo simulates a 5-node CRDT cluster running coordinated
// Tombstone GC driven by the Causal Stability Frontier.
package main

import (
	"fmt"

	"tombgc/tombgc"
)

func main() {
	ids := []string{"A", "B", "C", "D", "E"}
	cl := tombgc.NewCluster(ids...)

	// Simulate long-running deletes: each node accumulates 100 stale
	// tombstones (T in [1,49]) and 5 recent ones (T in [51,55]).
	for _, id := range ids {
		n := cl.Nodes[id]
		for i := 0; i < 100; i++ {
			n.AddTombstone(tombgc.Tombstone{
				Key:       fmt.Sprintf("%s/stale-%d", id, i),
				Version:   tombgc.VersionVector{id: uint64(i%49) + 1},
				SizeBytes: 1024,
			})
		}
		for i := 0; i < 5; i++ {
			n.AddTombstone(tombgc.Tombstone{
				Key:       fmt.Sprintf("%s/recent-%d", id, i),
				Version:   tombgc.VersionVector{id: uint64(51 + i)},
				SizeBytes: 1024,
			})
		}
	}

	before := cl.TotalTombstoneBytes()
	fmt.Printf("cluster before GC (%d bytes of tombstones):\n%s", before, cl)

	// All nodes advance past T=50 and gossip their causal frontiers.
	for _, id := range ids {
		cl.Advance(id, 50)
	}
	cl.GossipAll()

	// Nodes A and B confirm the whole cluster has passed T=50.
	for _, id := range []string{"A", "B"} {
		fmt.Printf("node %s causal stability frontier: %v\n", id, cl.Nodes[id].StabilityFrontier())
	}

	// Coordinated GC physically reclaims every tombstone with T<50.
	stats := cl.CollectGarbage()
	after := cl.TotalTombstoneBytes()
	for _, id := range ids {
		fmt.Printf("node %s GC: reclaimed=%d (%d bytes), remaining=%d (%d bytes)\n",
			id, stats[id].Reclaimed, stats[id].ReclaimedBytes,
			stats[id].Remaining, stats[id].RemainingBytes)
	}

	drop := float64(before-after) / float64(before) * 100
	fmt.Printf("tombstone memory: %d -> %d bytes (drop %.1f%%)\n", before, after, drop)
}
