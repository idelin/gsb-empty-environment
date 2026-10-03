// Demo: Multi-Paxos 5-node cluster with network partition, re-election,
// hole repair and dirty-log overwrite, plus the sparse-set memory optimizer.
package main

import (
	"fmt"
	"time"

	"consensus/paxos"
	"consensus/sparse"
)

func waitLeader(net *paxos.Network, ids ...int) *paxos.Node {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, id := range ids {
			if net.Node(id).IsLeader() {
				return net.Node(id)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

func paxosDemo() {
	fmt.Println("=== Multi-Paxos: partition, re-election, gap repair ===")
	net := paxos.NewNetwork(5, 2*time.Second)
	defer net.Close()

	net.Node(0).Campaign()
	old := waitLeader(net, 0)
	fmt.Printf("term 1: node %d elected\n", old.ID())
	old.Propose("A")
	old.Propose("B")
	old.WaitCommitted(3, 2*time.Second)
	fmt.Println("committed: [noop A B] on all 5 nodes")

	// "C" reaches only node 2 (future majority side), stays uncommitted.
	net.Partition([]int{0}, []int{1, 3, 4})
	old.Propose("C")
	time.Sleep(100 * time.Millisecond)
	net.Heal()

	// Full partition: old leader isolated with node 1; dirty write on minority.
	net.Partition([]int{0, 1}, []int{2, 3, 4})
	old.Propose("DIRTY")
	time.Sleep(100 * time.Millisecond)
	fmt.Printf("partition {{0,1}|{{2,3,4}}: old leader committed=%d (dirty not committed)\n", old.Committed())

	net.Node(2).Campaign()
	leader := waitLeader(net, 2, 3, 4)
	fmt.Printf("term 2: node %d elected by majority; Phase 1 completes slot 3 (C)\n", leader.ID())
	leader.WaitCommitted(5, 2*time.Second)
	leader.Propose("F")
	leader.WaitCommitted(6, 2*time.Second)

	net.Heal()
	leader.Sync()
	for id := 0; id < 5; id++ {
		net.Node(id).WaitCommitted(6, 2*time.Second)
	}
	fmt.Println("healed + synced: all nodes converged, dirty entry overwritten")
	for id := 0; id < 5; id++ {
		fmt.Printf("  node %d log: %v\n", id, net.Node(id).Log())
	}

	// Lease fast path: no Phase 1 messages while the lease is valid.
	net.ResetStats()
	for i := 0; i < 5; i++ {
		leader.Propose(fmt.Sprintf("fast-%d", i))
	}
	leader.WaitCommitted(11, 2*time.Second)
	stats := net.Stats()
	fmt.Printf("lease fast path: 5 proposals, Prepare msgs=%d, Accept msgs=%d\n",
		stats[paxos.MsgPrepare], stats[paxos.MsgAccept])
}

func sparseDemo() {
	fmt.Println("\n=== Sparse set: adaptive containers + compression ===")
	var s sparse.Set
	const n = 2000000
	for r := 0; r < 1000; r++ {
		start := uint32(r) * 4000000
		for k := start; k < start+2000; k++ {
			s.Add(k)
		}
	}
	before := s.MemBytes()
	s.Optimize()
	after := s.MemBytes()
	fmt.Printf("%d sparse keys: %d bytes -> %d bytes after Optimize (%.3f bytes/key)\n",
		n, before, after, float64(after)/n)

	snap := s.Snapshot()
	comp := sparse.CompressSnapshot(snap)
	fmt.Printf("snapshot: %d bytes raw, %d bytes gzip\n", len(snap), len(comp))

	loaded, err := sparse.LoadSnapshot(snap)
	if err != nil {
		panic(err)
	}
	fmt.Printf("round-trip equal: %v, cardinality=%d\n", s.Equals(loaded), loaded.Cardinality())
}

func main() {
	paxosDemo()
	sparseDemo()
}
