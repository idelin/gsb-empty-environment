package main

import "fmt"

func main() {
	nodes := buildCluster()
	coord := NewCoordinator(nodes, "A", "B")

	fmt.Println("=== Tombstone GC via Causal Stability Frontier ===")
	fmt.Printf("cluster: 5 nodes (A-E), coordinators: A, B, threshold: T=%d\n", gcThreshold)
	for _, n := range nodes {
		fmt.Printf("node %s: local clock T=%d, entries=%d, memory=%d B\n",
			n.ID, n.Store.Clock()[n.ID], len(n.Store.Entries()), n.Store.Memory())
	}

	stats := coord.RunGC(gcThreshold)

	fmt.Println("--- GC pass ---")
	fmt.Printf("causally stable at T=%d: %v\n", gcThreshold, stats.Stable)
	fmt.Printf("tombstones reclaimed: %d (freed %d B)\n", stats.Reclaimed, stats.FreedBytes)
	fmt.Printf("cluster memory: %d B -> %d B (reduction %.1f%%)\n",
		stats.MemBefore, stats.MemAfter, stats.ReductionPct())
}
