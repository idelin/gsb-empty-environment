// Demo: 5-node Multi-Paxos cluster with adaptive, load-aware limits.
//
// Part 1: consensus — leader commits under lease, a network partition
// isolates it, the majority elects a new leader which repairs the log via
// Phase 1 and safely overwrites the minority's dirty entry after healing.
// Part 2: adaptive controller scaling limits under varying pressure.
package main

import (
	"fmt"
	"time"

	"multipaxos/adaptive"
	"multipaxos/paxos"
)

func main() {
	fmt.Println("=== Part 1: Multi-Paxos consensus (5 nodes) ===")
	net := paxos.NewNetwork()
	ids := []string{"n0", "n1", "n2", "n3", "n4"}
	nodes := make([]*paxos.Node, len(ids))

	// Wire the adaptive controller as the leader-lease source.
	controller := adaptive.NewController(adaptive.DefaultConfig())
	for i, id := range ids {
		nodes[i] = paxos.NewNode(id, ids, net)
		nodes[i].SetLeaseSource(func() time.Duration {
			return controller.Snapshot().LeaseDuration
		})
	}
	leader := nodes[0]

	leader.Propose([]byte("set x=1"))
	leader.Propose([]byte("set y=2")) // lease valid: Phase 1 skipped
	fmt.Printf("leader=%s committed=%q (prepares handled cluster-wide: %d)\n",
		leader.ID(), leader.CommittedEntries(), totalPrepares(nodes))

	fmt.Println("\n--- partition: {n0,n1} | {n2,n3,n4} ---")
	net.Partition(ids[:2], ids[2:])
	if _, ok := leader.Propose([]byte("dirty")); !ok {
		fmt.Println("old leader (minority) cannot commit: dirty entry stays uncommitted")
	}

	newLeader := nodes[2]
	if newLeader.Campaign() {
		fmt.Printf("new leader elected by majority: %s\n", newLeader.ID())
	}
	newLeader.Propose([]byte("set z=3"))
	fmt.Printf("majority side committed=%q\n", newLeader.CommittedEntries())

	fmt.Println("\n--- heal: old leader's dirty log is safely overwritten ---")
	net.Heal()
	newLeader.SyncFollowers()
	for _, n := range nodes {
		fmt.Printf("%s committed=%q leader=%v\n", n.ID(), n.CommittedEntries(), n.IsLeader())
	}

	fmt.Println("\n=== Part 2: adaptive dynamic threshold scaling ===")
	scenarios := []struct {
		name string
		m    adaptive.Metrics
	}{
		{"warm-up, low load", adaptive.Metrics{Load: 0.10, RTT: 2 * time.Millisecond}},
		{"low load", adaptive.Metrics{Load: 0.15, RTT: 2 * time.Millisecond}},
		{"rising load", adaptive.Metrics{Load: 0.60, RTT: 8 * time.Millisecond}},
		{"high load", adaptive.Metrics{Load: 0.95, QueueDepth: 900, RTT: 40 * time.Millisecond}},
		{"errors spike (breaker)", adaptive.Metrics{Load: 0.90, ErrorRate: 0.45, RTT: 80 * time.Millisecond}},
		{"breaker window", adaptive.Metrics{Load: 0.10, RTT: 2 * time.Millisecond}},
	}
	for _, s := range scenarios {
		l := controller.Update(s.m)
		fmt.Printf("%-26s -> MaxInFlight=%-4d Batch=%-3d Lease=%-6v breaker=%v\n",
			s.name, l.MaxInFlight, l.BatchSize, l.LeaseDuration, controller.BreakerOpen())
		time.Sleep(60 * time.Millisecond) // let cooldown/breaker windows pass
	}
}

func totalPrepares(nodes []*paxos.Node) int {
	total := 0
	for _, n := range nodes {
		total += n.PreparesHandled()
	}
	return total
}
