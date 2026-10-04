package main

import (
	"fmt"

	"rbcast-sim/rbcast"
)

func main() {
	// Lossy, duplicating, reordering network; nodes keep a bounded gap table.
	sim := rbcast.NewSimulator(rbcast.NewNetwork(7, 0.15, 0.2, 5), 8)
	src := sim.AddSource("sensor-1")
	sim.Join("alpha")
	sim.Join("beta")
	sim.Join("gamma")

	fmt.Println("== phase 1: broadcast with beta offline ==")
	sim.GoOffline("beta")
	for i := 1; i <= 10; i++ {
		sim.Broadcast("sensor-1", fmt.Sprintf("reading-%d", i))
	}
	sim.Converge(20)
	sim.CollectAcks()
	sim.CompactAll()
	fmt.Printf("beta offline: source retains %d messages (compaction pinned by beta)\n", src.Retained())

	fmt.Println("== phase 2: beta rejoins and catches up ==")
	sim.GoOnline("beta")
	sim.Converge(20)
	fmt.Printf("beta watermark after catch-up: %d, duplicates recognised: %d\n",
		sim.Nodes["beta"].Watermark("sensor-1"), sim.Nodes["beta"].Duplicates)
	sim.CollectAcks()
	sim.CompactAll()
	fmt.Printf("all valid members acked: source retains %d messages\n", src.Retained())

	fmt.Println("== phase 3: gamma leaves, stale ack rejected, rejoins with new epoch ==")
	staleGen := sim.Members.Generation("gamma")
	sim.Leave("gamma")
	sim.Join("gamma")
	stale := rbcast.Ack{MemberID: "gamma", Generation: staleGen, Source: "sensor-1", Watermark: 10}
	fmt.Printf("replay pre-leave ack (gen %d): accepted=%v (current gen %d)\n",
		staleGen, sim.SubmitAck(stale), sim.Members.Generation("gamma"))

	for i := 11; i <= 15; i++ {
		sim.Broadcast("sensor-1", fmt.Sprintf("reading-%d", i))
	}
	sim.Converge(20)
	sim.CollectAcks()
	sim.CompactAll()
	for _, id := range []string{"alpha", "beta", "gamma"} {
		n := sim.Nodes[id]
		fmt.Printf("%s: %s, watermark=%d\n", id, n, n.Watermark("sensor-1"))
	}
	fmt.Printf("source: retained=%d compacted=%d, rejected acks=%d, network drops=%d\n",
		src.Retained(), src.Compacted(), sim.RejectedAcks, sim.Net.Dropped)
}
