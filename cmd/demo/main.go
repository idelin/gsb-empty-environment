// Command demo simulates a 5-node Multi-Paxos cluster end to end:
// election, lease-based replication, a network partition that strands the
// old leader with a dirty uncommitted entry, majority-side takeover with
// Phase-1 repair, and post-heal convergence.
package main

import (
	"fmt"
	"strings"

	"multipaxos/paxos"
)

func tickAll(nodes []*paxos.Node, n int) {
	for t := 0; t < n; t++ {
		for _, nd := range nodes {
			nd.Tick()
		}
	}
}

func findLeader(nodes []*paxos.Node) *paxos.Node {
	for _, n := range nodes {
		if n.LeaseValid() {
			return n
		}
	}
	return nil
}

func show(nodes []*paxos.Node, title string) {
	fmt.Printf("--- %s\n", title)
	for _, n := range nodes {
		st := n.Snapshot()
		applied := make([]string, len(st.Applied))
		for i, a := range st.Applied {
			s := string(a)
			if s == "" {
				s = "<noop>"
			}
			applied[i] = s
		}
		fmt.Printf("  node%d  role=%-8s ballot=%+v  commit=%d  applied=[%s]\n",
			st.ID, st.Role, st.Ballot, st.CommitIndex, strings.Join(applied, " "))
	}
}

func main() {
	net, nodes := paxos.NewCluster(5)

	fmt.Println("== phase 1: deterministic election (tick-driven) ==")
	tickAll(nodes, 10)
	leader := findLeader(nodes)
	fmt.Printf("leader: node%d (ballot %+v)\n", leader.Snapshot().ID, leader.Snapshot().Ballot)

	fmt.Println("\n== phase 2: lease-valid leader commits via Phase 2 only ==")
	idx, _ := leader.Propose([]byte("set x=1"))
	fmt.Printf("proposed \"set x=1\" at log index %d\n", idx)
	tickAll(nodes, 4)
	show(nodes, "after commit")

	fmt.Println("\n== phase 3: partition {0,1} | {2,3,4}; old leader keeps a dirty entry ==")
	net.SetPartition([]int{0, 1}, []int{2, 3, 4})
	if _, err := leader.Propose([]byte("set y=2")); err == nil {
		fmt.Println("old leader accepted \"set y=2\" -> replicated to minority {0,1} only, cannot commit")
	}
	tickAll(nodes, 40)
	show(nodes, "old leader's lease expired; majority side elected a new leader")

	fmt.Println("\n== phase 4: new majority leader commits on the same slot ==")
	newLeader := findLeader(nodes)
	fmt.Printf("new leader: node%d (ballot %+v)\n", newLeader.Snapshot().ID, newLeader.Snapshot().Ballot)
	if _, err := newLeader.Propose([]byte("set z=3")); err != nil {
		panic(err)
	}
	tickAll(nodes, 4)
	show(nodes, "majority side committed [set x=1, set z=3]")

	fmt.Println("\n== phase 5: heal; dirty minority logs are safely overwritten ==")
	net.Heal()
	tickAll(nodes, 60)
	show(nodes, "converged")

	fmt.Println("\n== invariant check ==")
	var ref []string
	ok := true
	for _, n := range nodes {
		st := n.Snapshot()
		cur := make([]string, len(st.Applied))
		for i, a := range st.Applied {
			cur[i] = string(a)
		}
		if ref == nil {
			ref = cur
			continue
		}
		if strings.Join(cur, ",") != strings.Join(ref, ",") {
			ok = false
		}
	}
	fmt.Printf("all nodes share identical committed prefix %v: %v\n", ref, ok)
}
