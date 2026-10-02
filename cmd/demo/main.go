// Command demo runs the acceptance scenario of the view-change log
// replicator end to end on a discrete test clock and prints each phase.
package main

import (
	"fmt"

	"raft"
)

func show(s *raft.Sim, ids ...int) {
	for _, id := range ids {
		n := s.Nodes[id]
		fmt.Printf("  node %d: %-8s term=%d commit=%d log=%v\n",
			id, n.State, n.Term, n.CommitIndex, data(n))
	}
}

func data(n *raft.Node) []string {
	out := []string{}
	for _, e := range n.Log[1:] {
		out = append(out, fmt.Sprintf("%s(t%d)", e.Data, e.Term))
	}
	return out
}

func tick(s *raft.Sim, n int) {
	for i := 0; i < n; i++ {
		s.Tick()
	}
}

func main() {
	s := raft.NewSim([]int{1, 2, 3})
	s.Nodes[1].SetTimeouts(10, 3)
	s.Nodes[2].SetTimeouts(12, 3)
	s.Nodes[3].SetTimeouts(8, 3)

	fmt.Println("== phase 1: 3-node cluster elects a leader in term 1 ==")
	tick(s, 10)
	fmt.Printf("tick=%d: node 3 is %s of term %d\n", s.Now, s.Nodes[3].State, s.Nodes[3].Term)
	for _, d := range []string{"e1", "e2", "e3"} {
		s.Propose(3, d)
	}
	fmt.Println("committed 3 entries on every node:")
	show(s, 1, 2, 3)

	fmt.Println("\n== phase 2: partition node 3; majority {1,2} moves to term 2 ==")
	s.Isolate(3)
	s.Propose(3, "c4") // old leader appends locally; can never commit
	s.Propose(3, "c5")
	tick(s, 25)
	fmt.Printf("tick=%d: node 1 is %s of term %d; node 3 still term %d (pre-vote blocked term inflation)\n",
		s.Now, s.Nodes[1].State, s.Nodes[1].Term, s.Nodes[3].Term)
	for _, d := range []string{"a4", "a5", "a6", "a7"} {
		s.Propose(1, d)
	}
	fmt.Println("majority committed 4 more entries; node 3 holds 2 uncommitted conflicting entries:")
	show(s, 1, 2, 3)

	fmt.Println("\n== phase 3: heal node 3 and inject a higher-term heartbeat ==")
	s.Heal(3)
	hb := s.Nodes[1].AppendMessage(3)
	fmt.Printf("injecting heartbeat: from=1 to=3 term=%d prevLogIndex=%d entries=%d leaderCommit=%d\n",
		hb.Term, hb.PrevLogIndex, len(hb.Entries), hb.LeaderCommit)
	s.Send(hb)
	tick(s, 10)
	fmt.Println("node 3 rolled back its uncommitted c4/c5 and adopted the leader's log:")
	show(s, 1, 2, 3)

	fmt.Println("\ncommitted entries preserved on node 3:", data(s.Nodes[3])[:3])
}
