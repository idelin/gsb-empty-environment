package main

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func testCluster() *Sim {
	s := NewSim()
	for _, id := range []int{1, 2, 3} {
		s.AddNode(id, Stable(1, 2, 3))
	}
	for _, id := range []int{4, 5, 6} {
		s.AddNode(id, Config{})
	}
	return s
}

func electAsLeader(t *testing.T, s *Sim, id int) {
	t.Helper()
	s.Elect(id)
	s.RunUntil(100)
	if s.Node(id).role != Leader {
		t.Fatalf("node %d did not become leader; trace:\n%s", id, strings.Join(s.Trace, "\n"))
	}
}

func certFor(s *Sim, index int) *Cert {
	for i := range s.Certs {
		if s.Certs[i].Index == index {
			return &s.Certs[i]
		}
	}
	return nil
}

func countFrom(voters []int, set []int) int {
	n := 0
	for _, v := range voters {
		if contains(set, v) {
			n++
		}
	}
	return n
}

// SAFETY: during the joint phase, a majority of the old set alone (even a
// unanimous old set) plus a minority of the new set must not commit.
func TestJointMajorityRequired(t *testing.T) {
	s := testCluster()
	electAsLeader(t, s, 1)
	base := s.Node(1).commit // no-op of view 1

	// Block the new-set nodes 5 and 6: leader can reach old majority
	// {1,2,3} plus only a minority {4} of the new set.
	blocked := map[int]bool{5: true, 6: true}
	s.Drop = func(m Message) bool {
		return m.Type == MsgAppend && blocked[m.To]
	}
	if err := s.ProposeReconfig(1, []int{4, 5, 6}); err != nil {
		t.Fatal(err)
	}
	s.Heartbeat(1)
	s.RunUntil(300)

	if got := s.Node(1).commit; got != base {
		t.Fatalf("joint entry committed without new-set majority: commit=%d (was %d)", got, base)
	}
	if err := s.CheckSafety(); err != nil {
		t.Fatal(err)
	}

	// Unblock node 5: now a new-set majority {4,5} is reachable.
	delete(blocked, 5)
	s.Heartbeat(1)
	s.RunUntil(600)

	if s.Node(1).commit <= base {
		t.Fatalf("joint entry still not committed after both majorities reachable")
	}
	c := certFor(s, base+1)
	if c == nil {
		t.Fatalf("no commit certificate recorded for joint entry")
	}
	if !c.Cfg.Joint {
		t.Fatalf("joint entry committed under non-joint config %s", c.Cfg)
	}
	if countFrom(c.Voters, []int{1, 2, 3}) < 2 || countFrom(c.Voters, []int{4, 5, 6}) < 2 {
		t.Fatalf("joint commit certificate lacks dual majority: %+v", c)
	}
	if err := s.CheckSafety(); err != nil {
		t.Fatal(err)
	}
}

// SAFETY: a vote cast for an old view must not count in a newer view.
func TestLateVoteIgnored(t *testing.T) {
	s := testCluster()
	s.Drop = func(m Message) bool {
		return m.Type == MsgVoteRequest && m.From == 1 && m.To == 3
	}
	s.Delay = func(m Message) int {
		if m.Type == MsgVoteResponse && m.From == 2 && m.To == 1 {
			return 30 // 2's granted vote for view 1 arrives very late
		}
		return 1
	}
	s.Elect(1) // view 1: only self vote, stuck
	s.RunUntil(10)
	s.Elect(2) // view 2: wins with votes from 1 and 3
	s.RunUntil(20)
	s.Drop, s.Delay = nil, nil
	s.RunUntil(200) // late view-1 vote arrives now

	if s.Node(1).role == Leader {
		t.Fatalf("node 1 became leader using a late vote from an old view")
	}
	if s.Node(1).view != 2 || s.Node(2).role != Leader {
		t.Fatalf("unexpected state: n1 view=%d role=%s, n2 role=%s",
			s.Node(1).view, s.Node(1).role, s.Node(2).role)
	}
	if err := s.CheckSafety(); err != nil {
		t.Fatal(err)
	}
}

// SAFETY: messages stamped with an older view are rejected outright.
func TestOldViewMessagesRejected(t *testing.T) {
	s := testCluster()
	electAsLeader(t, s, 1) // cluster is now at view 1
	n2 := s.Node(2)
	logLen, votedFor := n2.lastIndex(), n2.votedFor

	// Append from an older view must be rejected and not touch the log.
	s.deliver(Message{Type: MsgAppend, View: 0, From: 3, To: 2,
		Entries: []Entry{{Index: 1, View: 0, Cmd: "poison"}}})
	if n2.lastIndex() != logLen {
		t.Fatalf("stale-view Append modified the log")
	}
	// Vote request for the same view from a node we did not vote for.
	s.deliver(Message{Type: MsgVoteRequest, View: 1, From: 3, To: 2})
	if n2.votedFor != votedFor {
		t.Fatalf("node changed its vote within the same view")
	}
	if err := s.CheckSafety(); err != nil {
		t.Fatal(err)
	}
}

// SAFETY: switching directly from the old majority to a disjoint new
// majority is rejected at both the leader API and the follower guard.
func TestDirectSwitchRejected(t *testing.T) {
	s := testCluster()
	electAsLeader(t, s, 1)

	if err := s.ProposeDirectSwitch(1, []int{4, 5, 6}); err == nil {
		t.Fatalf("direct switch to disjoint majority was accepted by the leader")
	}
	// A Byzantine leader bypassing the API is stopped by follower guards.
	bad := Stable(4, 5, 6)
	logLen := make(map[int]int)
	for _, id := range []int{2, 3} {
		logLen[id] = s.Node(id).lastIndex()
	}
	for _, to := range []int{2, 3} {
		s.deliver(Message{Type: MsgAppend, View: 5, From: 1, To: to,
			PrevIndex: 1, PrevView: 1,
			Entries: []Entry{{Index: 2, View: 5, Cfg: &bad}}})
	}
	for _, id := range []int{2, 3} {
		n := s.Node(id)
		if n.cfg.Joint || !equalSets(n.cfg.Old, []int{1, 2, 3}) {
			t.Fatalf("node %d adopted forbidden config %s", id, n.cfg)
		}
		if n.lastIndex() != logLen[id] {
			t.Fatalf("node %d appended a forbidden config entry", id)
		}
	}
	if err := s.CheckSafety(); err != nil {
		t.Fatal(err)
	}
}

// LIVENESS: with timely delivery, the reconfiguration from {1,2,3} to the
// disjoint set {4,5,6} completes in bounded virtual time, and afterwards
// commands commit using only the new majority (all old nodes crashed).
func TestReconfigurationCompletesLiveness(t *testing.T) {
	s := testCluster()
	electAsLeader(t, s, 1)
	if err := s.Propose(1, "set x=1"); err != nil {
		t.Fatal(err)
	}
	s.RunUntil(100)
	if err := s.ProposeReconfig(1, []int{4, 5, 6}); err != nil {
		t.Fatal(err)
	}
	s.RunUntil(400)

	// The switch must be finished: stable new config committed.
	if cc := s.Node(1).committedCfg(); cc.Joint || !equalSets(cc.Old, []int{4, 5, 6}) {
		t.Fatalf("switch not completed by t=%d: committed cfg=%s", s.now, cc)
	}
	finishTime := s.now

	// Old majority is gone for good; the new set alone must make progress.
	s.Crash(1)
	s.Crash(2)
	s.Crash(3)
	s.Elect(4)
	s.RunUntil(finishTime + 200)
	if s.Node(4).role != Leader {
		t.Fatalf("new-set node 4 could not be elected without the old set")
	}
	if err := s.Propose(4, "set y=2"); err != nil {
		t.Fatal(err)
	}
	s.RunUntil(finishTime + 400)
	n4 := s.Node(4)
	if len(n4.applied) == 0 || n4.applied[len(n4.applied)-1] != "set y=2" {
		t.Fatalf("command not committed by new majority alone; applied=%v", n4.applied)
	}
	if finishTime > 500 {
		t.Fatalf("reconfiguration took too long: %d ticks", finishTime)
	}
	if err := s.CheckSafety(); err != nil {
		t.Fatal(err)
	}
}

// LIVENESS + SAFETY: a switch interrupted by a leader crash (joint
// committed, completion entry lost) is recovered by the next view's
// leader through a joint election requiring both majorities.
func TestInterruptedSwitchRecovery(t *testing.T) {
	s := testCluster()
	electAsLeader(t, s, 1)
	if err := s.ProposeReconfig(1, []int{4, 5, 6}); err != nil {
		t.Fatal(err)
	}
	// Interrupt: drop the completion entry on the wire.
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
	s.RunUntil(300)
	if cc := s.Node(1).committedCfg(); !cc.Joint {
		t.Fatalf("expected joint config committed before crash, got %s", cc)
	}
	s.Crash(1)
	s.Drop = nil

	s.Elect(2) // must win majorities of BOTH sets under the joint config
	s.RunUntil(600)
	if s.Node(2).role != Follower { // leader that finishes the switch steps down
		t.Fatalf("unexpected role for node 2: %s", s.Node(2).role)
	}
	if cc := s.Node(2).committedCfg(); cc.Joint || !equalSets(cc.Old, []int{4, 5, 6}) {
		t.Fatalf("recovering leader did not commit the new config: %s", cc)
	}
	// A new-set leader propagates the final commit to the rest.
	s.Elect(4)
	s.RunUntil(900)
	for _, id := range []int{4, 5, 6} {
		if cc := s.Node(id).committedCfg(); cc.Joint || !equalSets(cc.Old, []int{4, 5, 6}) {
			t.Fatalf("node %d did not reach stable new config: %s", id, cc)
		}
	}
	// Every commit certificate along the way must carry a valid quorum.
	if err := s.CheckSafety(); err != nil {
		t.Fatal(err)
	}
}

// SAFETY: when the completion entry reached only the new set, a stale
// old-set candidate cannot get elected (log up-to-date check), while a
// new-set candidate finishes the switch.
func TestStaleCandidateCannotBeElected(t *testing.T) {
	s := testCluster()
	electAsLeader(t, s, 1)
	if err := s.ProposeReconfig(1, []int{4, 5, 6}); err != nil {
		t.Fatal(err)
	}
	s.RunUntil(300) // full switch completes on the leader
	s.Crash(1)

	s.Elect(2) // stale log (misses the completion entry): must lose
	s.RunUntil(200)
	if s.Node(2).role == Leader {
		t.Fatalf("stale old-set candidate became leader")
	}
	s.Elect(4) // new-set node with the full log: must win and commit
	s.RunUntil(400)
	if s.Node(4).role != Leader {
		t.Fatalf("new-set candidate with complete log was not elected")
	}
	if err := s.CheckSafety(); err != nil {
		t.Fatal(err)
	}
}

// SAFETY (randomized evidence): under random drops, delays, crashes,
// restarts, elections and reconfigurations, the committed-prefix
// invariant and all commit certificates must hold at every step.
func TestRandomizedSafety(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s := testCluster()
	s.Drop = func(m Message) bool { return rng.Intn(100) < 15 }
	s.Delay = func(m Message) int { return 1 + rng.Intn(5) }

	check := func(step int) {
		if err := s.CheckSafety(); err != nil {
			t.Fatalf("step %d: %v\ntrace tail:\n%s", step, err,
				strings.Join(s.Trace[max(0, len(s.Trace)-40):], "\n"))
		}
	}
	for step := 0; step < 5000; step++ {
		switch rng.Intn(10) {
		case 0:
			s.Elect(1 + rng.Intn(6))
		case 1:
			id := 1 + rng.Intn(6)
			if rng.Intn(2) == 0 {
				s.Propose(id, fmt.Sprintf("cmd%d", step))
			} else {
				s.ProposeReconfig(id, []int{4, 5, 6})
			}
		case 2:
			s.Crash(1 + rng.Intn(6))
		case 3:
			s.Restart(1 + rng.Intn(6))
		case 4:
			s.Heartbeat(1 + rng.Intn(6))
		}
		s.Step()
		check(step)
	}
	for s.Step() {
		check(-1)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
