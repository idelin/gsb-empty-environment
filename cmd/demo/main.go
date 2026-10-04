// Command demo simulates a 10-node cluster that splits into two isolated
// partitions [1..5] and [6..10], lets both evolve independently, then heals
// the network. The first successful cross-partition PingReq triggers the
// merge handshake and the two halves fuse into one cluster.
package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	merge "partitionmerge"
)

func printTable(title string, nodes []*merge.Node) {
	fmt.Println(title)
	fmt.Println("  node | cluster   | epoch | lamport | members")
	fmt.Println("  -----+-----------+-------+---------+------------------------------")
	for _, n := range nodes {
		ids := n.MemberIDs()
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = fmt.Sprint(id)
		}
		fmt.Printf("  %4d | %-9s | %5d | %7d | {%s}\n",
			n.ID(), n.ClusterID(), n.Epoch(), n.Lamport(), strings.Join(parts, ","))
	}
	fmt.Println()
}

func main() {
	net := merge.NewNetwork()
	nodes := make([]*merge.Node, 0, 10)
	for id := 1; id <= 10; id++ {
		nodes = append(nodes, merge.NewNode(id, net))
	}

	// Two closed rings form and the link between them is cut.
	merge.FormCluster(nodes[0:5]...)
	merge.FormCluster(nodes[5:10]...)
	net.Partition([]int{1, 2, 3, 4, 5}, []int{6, 7, 8, 9, 10})
	fmt.Println("== network partitioned: [1..5] x [6..10], both evolve independently ==")

	for _, n := range nodes {
		n.Run(20*time.Millisecond, 2)
	}
	time.Sleep(300 * time.Millisecond)
	printTable("state after independent evolution:", nodes)

	// A probe across the partition is silently dropped.
	if ok := nodes[0].PingReq(6); !ok {
		fmt.Println("node 1 -> PingReq(6): DROPPED (partitioned)")
	}

	fmt.Println("== network healed ==")
	net.Heal()

	// The first successful cross-partition PingReq triggers the merge.
	if ok := nodes[0].PingReq(6); ok {
		fmt.Println("node 1 -> PingReq(6): OK -> merge handshake triggered")
	}

	deadline := time.Now().Add(5 * time.Second)
	for !merge.Converged(net) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !merge.Converged(net) {
		fmt.Println("!! merge did not converge")
	} else {
		fmt.Println("== merge converged: one cluster, one epoch, full topology ==")
	}
	time.Sleep(100 * time.Millisecond) // let clocks re-sync via gossip
	printTable("state after merge:", nodes)

	// Sanity: every node agrees on cluster, epoch and topology.
	cid, epoch := nodes[0].ClusterID(), nodes[0].Epoch()
	ok := true
	for _, n := range nodes {
		if n.ClusterID() != cid || n.Epoch() != epoch || len(n.Members()) != 10 {
			ok = false
		}
	}
	ids := nodes[0].MemberIDs()
	sort.Ints(ids)
	fmt.Printf("final: cluster=%s epoch=%d members=%v consistent=%v\n", cid, epoch, ids, ok)

	for _, n := range nodes {
		n.Stop()
	}
}
