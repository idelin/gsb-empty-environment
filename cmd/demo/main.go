// Command demo runs a 5-node Multi-Paxos cluster through election, leased
// fast-path commits, a network partition, leader replacement, hole repair
// and minority dirty-log overwrite, printing metrics along the way.
package main

import (
	"fmt"
	"os"

	"paxos/paxos"
)

func main() {
	c := paxos.NewCluster(5)
	c.Net.SetLatency(0) // set >0 to simulate real network delay

	// 1. Elect node 0 and commit through the lease fast path.
	if err := c.Nodes[0].Campaign(); err != nil {
		fatal(err)
	}
	fmt.Println("== leader elected: node 0 ==")
	for i := 0; i < 5; i++ {
		if _, err := c.Nodes[0].Propose([]byte(fmt.Sprintf("cmd-%d", i))); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("committed 5 entries, lease hit rate on leader: %.2f\n",
		c.Nodes[0].Metrics().HitRate(paxos.RateLease))

	// 2. Partition: old leader + one follower vs the majority.
	c.Net.Partition([]int{0, 1}, []int{2, 3, 4})
	fmt.Println("== partition: {0,1} | {2,3,4} ==")
	if _, err := c.Nodes[0].Propose([]byte("dirty")); err != nil {
		fmt.Println("old leader propose fails as expected:", err)
	}

	// 3. Majority elects a new leader; Phase 1 repairs uncommitted slots.
	if err := c.Nodes[2].Campaign(); err != nil {
		fatal(err)
	}
	fmt.Println("== new leader elected: node 2 (repairs holes via Phase 1) ==")
	if _, err := c.Nodes[2].Propose([]byte("cmd-5")); err != nil {
		fatal(err)
	}

	// 4. Heal; the new leader's commits converge the minority's dirty logs.
	c.Net.Heal()
	fmt.Println("== partition healed ==")
	if _, err := c.Nodes[2].Propose([]byte("cmd-6")); err != nil {
		fatal(err)
	}
	for _, n := range c.Nodes {
		fmt.Printf("node %d: commitIndex=%d applied=%d dirty-overwrites=%d\n",
			n.ID(), n.Status().CommitIndex, len(n.Applied()),
			n.Metrics().Counter(paxos.CDirtyOverwrite))
	}

	// 5. Export aggregated metrics.
	fmt.Println("== aggregated cluster metrics ==")
	if err := paxos.ExportAggregateJSON(os.Stdout, c.Aggregate()); err != nil {
		fatal(err)
	}
	c.Shutdown()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fatal:", err)
	os.Exit(1)
}
