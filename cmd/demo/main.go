// Command demo runs the anti-entropy repair scenario end to end:
// lossy gossip misses 2 node-offline events, then the background
// anti-entropy goroutines locate and repair them via Merkle trees.
package main

import (
	"context"
	"fmt"
	"time"

	"antientropy"
)

func main() {
	const nodeCount = 6
	net := antientropy.NewNetwork(0.9, 99) // 90% of gossip piggyback packets dropped

	nodes := make([]*antientropy.Node, nodeCount)
	for i := 0; i < nodeCount; i++ {
		nodes[i] = antientropy.NewNode(fmt.Sprintf("n%d", i), net, int64(100+i))
	}
	for _, n := range nodes {
		for i := 0; i < nodeCount; i++ {
			n.Store.Apply(antientropy.State{
				NodeID: fmt.Sprintf("n%d", i), Status: antientropy.StatusAlive, Version: 1,
			})
		}
	}

	fmt.Println("=== phase 1: failure injection ===")
	events := []antientropy.State{
		{NodeID: "n2", Status: antientropy.StatusOffline, Version: 2},
		{NodeID: "n5", Status: antientropy.StatusOffline, Version: 2},
	}
	for _, ev := range events {
		nodes[0].Publish(ev)
		fmt.Printf("n0 detected: %s\n", ev)
	}

	fmt.Println("\n=== phase 2: lossy gossip (90% packets blocked) ===")
	for round := 1; round <= 8; round++ {
		for _, n := range nodes {
			n.GossipRound(nodes)
		}
		fmt.Printf("round %d: sent=%d dropped=%d\n", round, net.Sent, net.Dropped)
	}
	fmt.Printf("gossip loss rate: %.0f%%\n", 100*float64(net.Dropped)/float64(net.Sent))
	for _, n := range nodes {
		var missing []string
		for _, ev := range events {
			st, _ := n.Store.Get(ev.NodeID)
			if st.Version < ev.Version {
				missing = append(missing, ev.NodeID)
			}
		}
		if len(missing) > 0 {
			fmt.Printf("node %s missed offline events: %v\n", n.ID, missing)
		}
	}

	fmt.Println("\n=== phase 3: Merkle tree depth-first comparison (n0 vs n1) ===")
	diff := antientropy.Diff(
		antientropy.BuildTree(nodes[0].Store.Snapshot()),
		antientropy.BuildTree(nodes[1].Store.Snapshot()),
	)
	for depth, mismatched := range diff.LevelStats {
		fmt.Printf("depth %d: %d mismatched tree node(s)\n", depth, mismatched)
	}
	fmt.Printf("located divergent leaf buckets: %v (compared only %d tree nodes)\n",
		diff.Buckets, diff.Compared)

	fmt.Println("\n=== phase 4: background anti-entropy repair ===")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, n := range nodes {
		n.StartAntiEntropy(ctx, nodes, 5*time.Millisecond)
	}
	root := nodes[0].Store.RootHash()
	for deadline := time.Now().Add(5 * time.Second); ; {
		converged := true
		for _, n := range nodes[1:] {
			if n.Store.RootHash() != root {
				converged = false
				break
			}
		}
		if converged {
			break
		}
		if time.Now().After(deadline) {
			fmt.Println("TIMEOUT: cluster did not converge")
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	for _, n := range nodes {
		if repairs := n.Repairs(); len(repairs) > 0 {
			fmt.Printf("node %s repaired via anti-entropy: %v\n", n.ID, repairs)
		}
	}

	fmt.Println("\n=== phase 5: final converged membership ===")
	for _, st := range nodes[0].Store.Snapshot() {
		fmt.Printf("  %s\n", st)
	}
	fmt.Printf("all %d nodes converged, merkle root = %x\n", nodeCount, root)
}
