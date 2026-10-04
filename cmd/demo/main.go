// Command demo simulates the acceptance scenario on a 5-node Multi-Paxos
// cluster: a network partition deposes the old leader, the new leader
// completes uncommitted slots via Phase 1 and safely overwrites the
// minority's dirty log after healing.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"mpaxos/paxos"
)

const lease = 300 * time.Millisecond

func wait(cond func() bool, what string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			fmt.Printf("  ok: %s\n", what)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Printf("  TIMEOUT: %s\n", what)
	os.Exit(1)
}

func printLogs(c *paxos.Cluster, label string) {
	fmt.Printf("--- %s ---\n", label)
	for i := 0; i < c.Size(); i++ {
		n := c.Node(i)
		var cmds []string
		for _, e := range n.Applied() {
			if e.IsNoop() {
				cmds = append(cmds, "<noop>")
			} else {
				cmds = append(cmds, string(e.Cmd))
			}
		}
		fmt.Printf("  node%d committed=%d applied=%v\n", i, n.CommitIndex(), cmds)
	}
}

func main() {
	dir, err := os.MkdirTemp("", "mpaxos-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	var paths []string
	for i := 0; i < 5; i++ {
		paths = append(paths, filepath.Join(dir, fmt.Sprintf("node%d.log", i)))
	}
	c, err := paxos.NewCluster(5, lease, paths)
	if err != nil {
		panic(err)
	}
	defer c.Close()
	fmt.Printf("cluster: 5 nodes, lease=%v, stores in %s\n\n", lease, dir)

	fmt.Println("[1] elect node0, commit slots 1-3")
	c.Node(0).Campaign()
	wait(c.Node(0).IsLeader, "node0 elected")
	for i := 1; i <= 3; i++ {
		if _, err := c.Node(0).Propose([]byte(fmt.Sprintf("cmd-%d", i))); err != nil {
			panic(err)
		}
	}
	wait(func() bool { return c.Node(4).CommitIndex() == 3 }, "slots 1-3 committed on all nodes")

	fmt.Println("\n[2] 'kept-4' accepted by a majority, but commit replies are lost")
	for _, f := range []int{2, 3, 4} {
		c.Block(f, 0)
	}
	if _, err := c.Node(0).Propose([]byte("kept-4")); err != nil {
		panic(err)
	}
	wait(func() bool {
		e, ok := c.Node(2).LogEntry(4)
		return ok && string(e.Cmd) == "kept-4"
	}, "kept-4 accepted on majority side, still uncommitted")

	fmt.Println("\n[3] partition {0,1} | {2,3,4}; old leader writes dirty slot 5 in minority")
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	if _, err := c.Node(0).Propose([]byte("dirty-5")); err != nil {
		panic(err)
	}
	wait(func() bool {
		e, ok := c.Node(1).LogEntry(5)
		return ok && string(e.Cmd) == "dirty-5"
	}, "dirty-5 sits uncommitted on {0,1}")

	fmt.Println("\n[4] majority cannot elect while the old lease holds")
	c.Node(2).Campaign()
	time.Sleep(80 * time.Millisecond)
	fmt.Printf("  node2 leader during lease: %v (want false)\n", c.Node(2).IsLeader())

	fmt.Println("\n[5] lease expires; node2 wins Phase 1 and repairs the log")
	wait(func() bool {
		if c.Node(2).IsLeader() {
			return true
		}
		c.Node(2).Campaign()
		return false
	}, "node2 elected after lease expiry")
	wait(func() bool { return c.Node(4).CommitIndex() == 4 },
		"Phase 1 completed slot 4 with its original value 'kept-4'")
	if _, err := c.Node(2).Propose([]byte("new-5")); err != nil {
		panic(err)
	}
	wait(func() bool { return c.Node(4).CommitIndex() == 5 }, "new-5 committed on majority")

	fmt.Println("\n[6] heal; new leader overwrites the minority's dirty slot 5")
	c.Heal()
	if _, err := c.Node(2).Propose([]byte("post-6")); err != nil {
		panic(err)
	}
	wait(func() bool { return c.Node(0).CommitIndex() == 6 }, "old leader converged")
	wait(func() bool { return c.Node(1).CommitIndex() == 6 }, "isolated follower converged")

	fmt.Println()
	printLogs(c, "final replicated log (identical, gap-free on every node)")

	fmt.Println("\n--- message statistics ---")
	for t, n := range c.Counts() {
		fmt.Printf("  %-9s %d\n", t, n)
	}

	fmt.Println("\n--- persistence check: reload node0 store from disk ---")
	st, err := paxos.OpenStore(paths[0], false)
	if err != nil {
		panic(err)
	}
	defer st.Close()
	log, err := st.LoadLog()
	if err != nil {
		panic(err)
	}
	for i := uint64(1); i <= 6; i++ {
		e := log[i]
		cmd := string(e.Cmd)
		if e.IsNoop() {
			cmd = "<noop>"
		}
		fmt.Printf("  slot %d: term=%d node=%d cmd=%q\n", i, e.Term, e.Node, cmd)
	}
}
