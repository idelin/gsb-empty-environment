// Command demo runs a scripted Raft scenario on the event simulator:
// election, replication, a network partition that forks the log, and
// recovery showing the committed prefix converging on every node.
package main

import (
	"fmt"
	"strings"

	"raftsim"
)

func main() {
	s := raftsim.NewSim(5, 7)
	s.Trace = true

	fmt.Println("== phase 1: election (time is simulator ticks) ==")
	s.Run(40)
	fmt.Printf("leaders: %v\n\n", s.LeaderIDs())

	fmt.Println("== phase 2: replicate 'a' to all 5 nodes ==")
	s.Propose("a")
	s.Run(60)
	printState(s)

	oldLeader := s.LeaderIDs()[0]
	minorityFollower := (oldLeader + 1) % 5
	var majority []int
	for i := 0; i < 5; i++ {
		if i != oldLeader && i != minorityFollower {
			majority = append(majority, i)
		}
	}

	fmt.Printf("== phase 3: partition {%d,%d} | %v ==\n", oldLeader, minorityFollower, majority)
	s.Partition([]int{oldLeader, minorityFollower}, majority)
	s.ProposeOn(oldLeader, "stale") // accepted by old leader, can never commit
	s.Run(80)
	fmt.Println("old leader (minority) keeps appending; majority elects a new leader:")
	for _, id := range s.LeaderIDs() {
		for _, m := range majority {
			if id == m {
				s.ProposeOn(id, "c")
				s.ProposeOn(id, "d")
			}
		}
	}
	s.Run(80)
	printState(s)

	fmt.Println("== phase 4: heal; long committed log wins, short suffix truncated ==")
	s.Heal()
	s.Run(200)
	printState(s)

	fmt.Println("== phase 5: node 4 down while log grows and leader changes; ==")
	fmt.Println("==           on restart it catches up via conflict hints      ==")
	s.Crash(4)
	s.Propose("e")
	s.Propose("f")
	s.Run(60)
	s.Crash(s.LeaderIDs()[0]) // force a re-election so nextIndex overshoots
	s.Run(80)
	s.Propose("g")
	s.Run(60)
	fmt.Println("restarting lagging nodes; watch the hint-driven back-up:")
	s.Restart(4)
	s.Restart(1)
	s.Run(120)
	printState(s)
}

func printState(s *raftsim.Sim) {
	fmt.Printf("t=%04d cluster state:\n", s.Now)
	for _, nd := range s.Nodes {
		fmt.Printf("  node %d  term=%d  role=%-9s log=[%s]  committed=[%s]\n",
			nd.ID, nd.Term, nd.Role(),
			strings.Join(cmds(nd.Log), " "),
			strings.Join(cmds(nd.Applied), " "))
	}
	fmt.Println()
}

func cmds(entries []raftsim.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		if e.Cmd == "" {
			out[i] = fmt.Sprintf("·(t%d)", e.Term) // no-op
		} else {
			out[i] = fmt.Sprintf("%s(t%d)", e.Cmd, e.Term)
		}
	}
	return out
}
