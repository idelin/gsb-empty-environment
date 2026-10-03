// Command demo runs the Multi-Paxos partition-takeover scenario and the
// sparse-data memory optimizer showcase.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"mpaxos/paxos"
	"mpaxos/sparse"
)

func printLogs(c *paxos.Cluster, tag string) {
	fmt.Printf("  %-28s", tag)
	for _, n := range c.Nodes {
		fmt.Printf(" n%d(ci=%d):", n.ID(), n.CommitIndex())
		for _, e := range n.Log() {
			if e.Noop {
				fmt.Printf(" <noop>")
			} else {
				fmt.Printf(" %s", e.Value)
			}
		}
		fmt.Print(" |")
	}
	fmt.Println()
}

func paxosDemo() {
	fmt.Println("=== Multi-Paxos: 5-node cluster, network partition & takeover ===")
	c := paxos.NewCluster(5, time.Minute)

	fmt.Println("[1] node 0 elected, commits v1 on all 5 nodes")
	if _, err := c.Nodes[0].Propose([]byte("v1")); err != nil {
		panic(err)
	}
	c.Nodes[0].BroadcastHeartbeat()
	printLogs(c, "after v1")

	fmt.Println("[2] partition {0,1} | {2,3,4}; old leader writes 'dirty' to minority")
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	_, err := c.Nodes[0].Propose([]byte("dirty"))
	fmt.Printf("    old leader propose -> %v (dirty log only on nodes 0,1)\n", err)
	printLogs(c, "minority partitioned")

	fmt.Println("[3] leases expire; majority elects node 2, commits 'real'")
	c.Advance(2 * time.Minute)
	if _, err := c.Nodes[2].Propose([]byte("real")); err != nil {
		panic(err)
	}
	printLogs(c, "majority commits real")

	fmt.Println("[4] heal: new leader's heartbeat overwrites dirty minority logs")
	c.Heal()
	c.Nodes[2].BroadcastHeartbeat()
	printLogs(c, "after heal")
	fmt.Println()
}

func sparseDemo() {
	fmt.Println("=== Sparse optimizer: 1,000,000 entries over a 1e9 key space ===")
	v := sparse.New()
	rng := rand.New(rand.NewSource(1))
	const n = 1000000
	for i := 0; i < n; i++ {
		v.Set(uint64(rng.Int63n(1e9)), int64(i))
	}
	fmt.Printf("  mode=%s count=%d density=%.6f\n", v.Mode(), v.Count(), v.Density())
	fmt.Printf("  footprint: %d bytes (%.1f MiB); dense array would need ~7.5 GiB\n",
		v.Bytes(), float64(v.Bytes())/(1<<20))
	v.Compact()
	fmt.Printf("  after Compact: mode=%s footprint=%d bytes (%.1f MiB)\n",
		v.Mode(), v.Bytes(), float64(v.Bytes())/(1<<20))

	fmt.Println("  adaptive conversion: filling keys 0..9999 densely")
	d := sparse.New()
	for i := uint64(0); i < 10000; i++ {
		d.Set(i, int64(i))
	}
	fmt.Printf("  mode=%s density=%.2f footprint=%d bytes\n", d.Mode(), d.Density(), d.Bytes())
	for i := uint64(0); i < 10000; i++ {
		if i%10 != 0 {
			d.Delete(i)
		}
	}
	fmt.Printf("  after deleting 90%% (spread): mode=%s footprint=%d bytes\n", d.Mode(), d.Bytes())
}

func main() {
	paxosDemo()
	sparseDemo()
}
