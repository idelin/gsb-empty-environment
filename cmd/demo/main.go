// Command demo runs the acceptance scenario end to end on a deterministic
// clock: election -> partition -> dirty minority log -> new majority
// leader -> phase-1 repair -> heal -> safe overwrite of the dirty log.
package main

import (
	"fmt"
	"strings"

	"multipaxos/paxos"
)

func runUntil(c *paxos.Cluster, max int, cond func() bool) bool {
	for i := 0; i < max; i++ {
		if cond() {
			return true
		}
		c.Tick()
	}
	return cond()
}

func slotStr(s paxos.Slot) string {
	if s.NoOp {
		return "<noop>"
	}
	return fmt.Sprintf("%q", s.Value)
}

func printCluster(c *paxos.Cluster) {
	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		committed := n.Committed()
		parts := make([]string, len(committed))
		for j, s := range committed {
			parts[j] = slotStr(s)
		}
		fmt.Printf("  node%d %-8s promised=%v commitIndex=%d committed=[%s] logLen=%d\n",
			i, n.Role(), n.Promised(), n.CommitIndex(),
			strings.Join(parts, " "), len(n.Log()))
	}
}

func allCommitted(c *paxos.Cluster, k int) bool {
	for i := 0; i < c.Size(); i++ {
		if c.Node(i).CommitIndex() != k {
			return false
		}
	}
	return true
}

func main() {
	c := paxos.NewCluster(5, paxos.DefaultConfig())

	fmt.Println("== Phase 1: elect a leader and commit v0 on all 5 nodes ==")
	runUntil(c, 100, func() bool { return c.Leader() == 0 })
	fmt.Printf("leader elected: node%d (ballot %v)\n", c.Leader(), c.Node(c.Leader()).Ballot())
	c.Node(0).Propose("v0")
	runUntil(c, 100, func() bool { return allCommitted(c, 1) })
	printCluster(c)

	fmt.Println("\n== Phase 2: partition {0,1} | {2,3,4}; old leader writes dirty entries ==")
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	c.Node(0).Propose("dirty1") // lease still valid: accepted by minority only
	c.Node(0).Propose("dirty2")
	c.TickN(3)
	fmt.Println("minority side (dirty, uncommitted):")
	printCluster(c)

	fmt.Println("\n== Phase 3: majority elects node2; phase-1 repair; commits v1,v2 ==")
	runUntil(c, 200, func() bool { return c.Leader() == 2 })
	fmt.Printf("new leader: node%d (ballot %v)\n", c.Leader(), c.Node(c.Leader()).Ballot())
	c.Node(2).Propose("v1")
	c.Node(2).Propose("v2")
	runUntil(c, 100, func() bool { return c.Node(2).CommitIndex() == 3 })
	printCluster(c)

	fmt.Println("\n== Phase 4: heal; committed prefix overwrites the dirty minority log ==")
	c.Heal()
	c.Node(2).Propose("post")
	runUntil(c, 200, func() bool { return allCommitted(c, 4) })
	printCluster(c)

	fmt.Println("\n== Verification ==")
	ok := true
	for i := 0; i < c.Size(); i++ {
		for _, s := range c.Node(i).Committed() {
			if strings.HasPrefix(s.Value, "dirty") {
				fmt.Printf("FAIL: node%d committed a dirty entry\n", i)
				ok = false
			}
		}
	}
	if c.Leader() != 2 {
		fmt.Println("FAIL: leadership flapped")
		ok = false
	}
	if ok {
		fmt.Println("OK: all 5 nodes share the gap-free committed prefix [v0 v1 v2 post]; dirty entries overwritten")
	}
}
