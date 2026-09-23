package main

import (
	"fmt"
	"strings"
)

func newCluster() *Sim {
	s := NewSim()
	for _, id := range []int{1, 2, 3} {
		s.AddNode(id, Stable(1, 2, 3))
	}
	for _, id := range []int{4, 5, 6} {
		s.AddNode(id, Config{}) // not yet members
	}
	return s
}

func report(name string, s *Sim) {
	fmt.Printf("===== %s =====\n", name)
	for _, line := range s.Trace {
		fmt.Println(" ", line)
	}
	if err := s.CheckSafety(); err != nil {
		fmt.Println("  SAFETY CHECK: FAILED ->", err)
	} else {
		fmt.Println("  SAFETY CHECK: OK (all committed prefixes consistent, all commit certs carry valid quorums)")
	}
	fmt.Println()
}

func scenarioReconfig() {
	s := newCluster()
	s.Elect(1)
	s.RunUntil(50)
	s.Propose(1, "set x=1")
	s.RunUntil(100)
	s.ProposeReconfig(1, []int{4, 5, 6}) // disjoint new set, via joint phase
	s.RunUntil(400)
	// Old leader stepped down; old majority is no longer needed.
	s.Crash(1)
	s.Crash(2)
	s.Crash(3)
	s.Elect(4)
	s.RunUntil(600)
	s.Propose(4, "set y=2")
	s.RunUntil(800)
	report("Scenario A: reconfiguration {1,2,3} -> {4,5,6} via joint phase", s)
	fmt.Printf("  node4 applied=%v commit=%d cfg=%s\n\n", s.Node(4).applied, s.Node(4).commit, s.Node(4).cfg)
}

func scenarioDirectSwitch() {
	s := newCluster()
	s.Elect(1)
	s.RunUntil(50)
	err := s.ProposeDirectSwitch(1, []int{4, 5, 6})
	fmt.Println("===== Scenario B: direct switch attempt (disjoint majorities) =====")
	fmt.Println("  ProposeDirectSwitch({4,5,6}) ->", err)
	// Even a buggy/Byzantine leader that sends the forbidden entry on the
	// wire is rejected by followers' config-transition guard.
	bad := Stable(4, 5, 6)
	for _, to := range []int{2, 3} {
		s.deliver(Message{Type: MsgAppend, View: 7, From: 1, To: to,
			PrevIndex: 1, PrevView: 1,
			Entries: []Entry{{Index: 2, View: 7, Cfg: &bad}}})
	}
	for _, line := range s.Trace {
		if strings.Contains(line, "REJECT") {
			fmt.Println(" ", line)
		}
	}
	fmt.Printf("  node2 cfg=%s commit=%d | node3 cfg=%s commit=%d\n", s.Node(2).cfg, s.Node(2).commit, s.Node(3).cfg, s.Node(3).commit)
	if err := s.CheckSafety(); err != nil {
		fmt.Println("  SAFETY CHECK: FAILED ->", err)
	} else {
		fmt.Println("  SAFETY CHECK: OK (nothing committed, configs unchanged)")
	}
	fmt.Println()
}

func scenarioLateVote() {
	s := newCluster()
	// Drop 1's vote request to 3, and delay 2's granted vote to 1 by 30 ticks.
	s.Drop = func(m Message) bool {
		return m.Type == MsgVoteRequest && m.From == 1 && m.To == 3
	}
	s.Delay = func(m Message) int {
		if m.Type == MsgVoteResponse && m.From == 2 && m.To == 1 {
			return 30
		}
		return 1
	}
	s.Elect(1) // view 1: gets only its own vote, stuck
	s.RunUntil(10)
	s.Elect(2) // view 2: wins with votes from 1 and 3
	s.RunUntil(20)
	s.Drop = nil
	s.Delay = nil
	s.RunUntil(100) // the delayed view-1 vote now arrives at node 1
	report("Scenario C: late vote from an old view", s)
	fmt.Printf("  node1 role=%s view=%d | node2 role=%s view=%d\n\n",
		s.Node(1).role, s.Node(1).view, s.Node(2).role, s.Node(2).view)
}

func scenarioRecovery() {
	s := newCluster()
	s.Elect(1)
	s.RunUntil(50)
	s.ProposeReconfig(1, []int{4, 5, 6})
	// Interrupt the switch: the completion entry (stable new config) is
	// dropped on the wire, so only the joint entry gets committed.
	s.Drop = func(m Message) bool {
		if m.Type == MsgAppend {
			for _, e := range m.Entries {
				if e.Cfg != nil && !e.Cfg.Joint {
					return true
				}
			}
		}
		return false
	}
	s.RunUntil(200) // joint entry commits; completion entry never spreads
	s.Crash(1)      // leader dies mid-switch
	s.Drop = nil
	s.Elect(2) // new view: joint election needs majorities of BOTH sets
	s.RunUntil(500)
	report("Scenario D: leader crash mid-switch, recovery by next view", s)
	fmt.Printf("  node5 cfg=%s commit=%d | node6 cfg=%s commit=%d\n\n",
		s.Node(5).cfg, s.Node(5).commit, s.Node(6).cfg, s.Node(6).commit)
}

func main() {
	scenarioReconfig()
	scenarioDirectSwitch()
	scenarioLateVote()
	scenarioRecovery()
}
