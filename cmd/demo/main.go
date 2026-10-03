// Command demo runs the acceptance scenario end to end: a 5-node
// Multi-Paxos cluster, a network partition that strands the old leader
// in the minority, failover with Phase-1 catch-up, safe overwrite of
// the minority's dirty log, and a final offline integrity inspection.
package main

import (
	"fmt"

	"multipaxos/paxos"
)

func printCluster(title string, c *paxos.Cluster) {
	fmt.Println(title)
	for _, n := range c.Nodes() {
		fmt.Println("  " + n.String())
	}
}

func inspect(title string, c *paxos.Cluster) {
	fmt.Println(title)
	rep := paxos.Inspect(c.Nodes())
	fmt.Println("  " + rep.String())
	if vs := paxos.AssertInvariants(c.Nodes()); len(vs) > 0 {
		fmt.Println("  invariant violations:")
		for _, v := range vs {
			fmt.Println("   - " + v)
		}
	} else {
		fmt.Println("  invariants: all hold (ElectionSafety, LogMatching, StateMachineSafety, LeaderCompleteness)")
	}
}

func main() {
	c := paxos.NewCluster(5)
	leader := c.Node(0)

	fmt.Println("== 1. elect node0 as leader, commit x1,x2 under lease ==")
	if err := leader.BecomeLeader(); err != nil {
		panic(err)
	}
	for _, v := range []string{"x1", "x2"} {
		if _, err := leader.Propose(v); err != nil {
			panic(err)
		}
	}
	printCluster("", c)

	fmt.Println("\n== 2. partition: {0,1} | {2,3,4}; old leader's dirty write stays minority-only ==")
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	if _, err := leader.Propose("dirty"); err != nil {
		fmt.Println("  old leader Propose(\"dirty\") failed as expected:", err)
	}
	printCluster("", c)

	fmt.Println("\n== 3. majority elects node2; Phase 1 catch-up, then commit v3 ==")
	newLeader := c.Node(2)
	if err := newLeader.BecomeLeader(); err != nil {
		panic(err)
	}
	if _, err := newLeader.Propose("v3"); err != nil {
		panic(err)
	}
	printCluster("", c)
	inspect("  -- inspection during partition --", c)

	fmt.Println("\n== 4. heal: heartbeat converges minority, dirty slot 3 is overwritten ==")
	c.Heal()
	newLeader.SendHeartbeats()
	if _, err := newLeader.Propose("v4"); err != nil {
		panic(err)
	}
	printCluster("", c)
	inspect("  -- final inspection --", c)
}
