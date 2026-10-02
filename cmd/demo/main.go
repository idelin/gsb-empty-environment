// Command demo runs the acceptance scenario end to end and prints the
// cluster state after each phase.
package main

import (
	"fmt"

	"logrepl"
)

func runTicks(c *logrepl.Cluster, n int) {
	for i := 0; i < n; i++ {
		c.Tick()
	}
}

func printCluster(title string, c *logrepl.Cluster) {
	fmt.Println("== " + title)
	for _, id := range []int{1, 2, 3} {
		n := c.Node(id)
		var log []string
		for _, e := range n.Entries() {
			log = append(log, fmt.Sprintf("%s(t%d)", e.Cmd, e.Term))
		}
		fmt.Printf("  N%d term=%d role=%-8s committed=%d log=%v\n",
			id, n.Term(), n.Role(), n.Committed(), log)
	}
	fmt.Println()
}

func mustPropose(c *logrepl.Cluster, id int, cmd string) {
	if err := c.Propose(id, cmd); err != nil {
		panic(err)
	}
}

func main() {
	c := logrepl.NewCluster(1, 2, 3)

	// Phase 1: C (N3) becomes term-1 leader and commits 3 entries.
	c.TriggerTimeout(3)
	runTicks(c, 10)
	leader, _ := c.Leader()
	for _, cmd := range []string{"e1", "e2", "e3"} {
		mustPropose(c, leader, cmd)
	}
	runTicks(c, 10)
	printCluster("phase 1: term 1, 3 entries committed on all nodes", c)

	// Phase 2: isolate C; as a stale leader it accepts a write that can
	// never be committed (conflicting uncommitted entry at index 4).
	c.Isolate(3)
	mustPropose(c, 3, "X-uncommitted")
	runTicks(c, 2)
	printCluster("phase 2: C isolated with an uncommitted write", c)

	// Phase 3: majority A/B moves to term 2 and commits 4 more entries.
	runTicks(c, 60)
	leader2, _ := c.Leader()
	for _, cmd := range []string{"e4", "e5", "e6", "e7"} {
		mustPropose(c, leader2, cmd)
	}
	runTicks(c, 10)
	printCluster(fmt.Sprintf("phase 3: A/B advanced to term 2 (leader=N%d), 4 entries committed", leader2), c)

	// Phase 4: heal C; the higher-term heartbeat forces C to roll back its
	// conflicting uncommitted entry and catch up.
	c.Heal(3)
	runTicks(c, 20)
	printCluster("phase 4: C healed, higher-term heartbeat forced rollback of X", c)
}
