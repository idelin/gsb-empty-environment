package paxos

import (
	"fmt"
	"testing"
	"time"
)

func newCluster(size int, opts ...Option) (*Network, []*Node) {
	net := NewNetwork()
	nodes := make([]*Node, size)
	for i := 0; i < size; i++ {
		nodes[i] = NewNode(fmt.Sprintf("n%d", i+1), net, opts...)
	}
	return net, nodes
}

func TestBallotOrdering(t *testing.T) {
	cases := []struct {
		a, b Ballot
		want int
	}{
		{Ballot{1, "n1"}, Ballot{2, "n1"}, -1},
		{Ballot{2, "n1"}, Ballot{1, "n9"}, 1},
		{Ballot{1, "n1"}, Ballot{1, "n2"}, -1},
		{Ballot{1, "n2"}, Ballot{1, "n1"}, 1},
		{Ballot{3, "n3"}, Ballot{3, "n3"}, 0},
	}
	for _, c := range cases {
		if got := c.a.Compare(c.b); got != c.want {
			t.Fatalf("Compare(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSingleProposalCommitsOnQuorum(t *testing.T) {
	_, nodes := newCluster(3)
	slot, err := nodes[0].Propose("x")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if slot != 1 {
		t.Fatalf("slot = %d, want 1", slot)
	}
	committed := 0
	for _, n := range nodes {
		e, ok := n.LogEntry(1)
		if !ok || e.Value != "x" || !e.Committed {
			continue
		}
		committed++
	}
	if committed < 2 {
		t.Fatalf("only %d nodes have committed entry, want quorum >= 2", committed)
	}
	if nodes[0].CommitIndex() != 1 {
		t.Fatalf("commitIndex = %d, want 1", nodes[0].CommitIndex())
	}
}

func TestPromiseRejectsLowerOrEqualBallot(t *testing.T) {
	_, nodes := newCluster(3)
	n := nodes[0]
	high := Ballot{Num: 5, Node: "n2"}
	rep := n.Handle(Prepare{Ballot: high, From: "n2"}).(Promise)
	if !rep.OK {
		t.Fatal("first prepare should be promised")
	}
	for _, b := range []Ballot{{Num: 4, Node: "n9"}, {Num: 5, Node: "n1"}, {Num: 5, Node: "n2"}} {
		rep := n.Handle(Prepare{Ballot: b, From: "n1"}).(Promise)
		if rep.OK {
			t.Fatalf("prepare with ballot %v should be rejected after promise %v", b, high)
		}
	}
}

func TestAcceptRejectsBelowPromisedBallot(t *testing.T) {
	_, nodes := newCluster(3)
	n := nodes[0]
	n.Handle(Prepare{Ballot: Ballot{Num: 5, Node: "n2"}, From: "n2"})
	rep := n.Handle(Accept{Slot: 1, Ballot: Ballot{Num: 4, Node: "n1"}, Value: "stale", From: "n1"}).(Accepted)
	if rep.OK {
		t.Fatal("accept below promised ballot must be rejected")
	}
	rep = n.Handle(Accept{Slot: 1, Ballot: Ballot{Num: 5, Node: "n2"}, Value: "fresh", From: "n2"}).(Accepted)
	if !rep.OK {
		t.Fatal("accept at promised ballot must succeed")
	}
	e, _ := n.LogEntry(1)
	if e.Value != "fresh" {
		t.Fatalf("log[1] = %q, want %q", e.Value, "fresh")
	}
}

func TestLeaseSkipsPhaseOne(t *testing.T) {
	net, nodes := newCluster(5, WithLeaseDuration(time.Minute))
	leader := nodes[0]
	if _, err := leader.Propose("first"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if !leader.IsLeader() {
		t.Fatal("node should hold a lease after a successful proposal")
	}
	preparesBefore, acceptsBefore, _ := net.Stats()
	for i := 0; i < 3; i++ {
		if _, err := leader.Propose(fmt.Sprintf("v%d", i)); err != nil {
			t.Fatalf("Propose: %v", err)
		}
	}
	preparesAfter, acceptsAfter, _ := net.Stats()
	if d := preparesAfter - preparesBefore; d != 0 {
		t.Fatalf("lease fast path sent %d Prepare messages, want 0", d)
	}
	if d := acceptsAfter - acceptsBefore; d != 15 {
		t.Fatalf("3 proposals x 5 members = 15 Accepts expected, got %d", d)
	}
	if leader.CommitIndex() != 4 {
		t.Fatalf("commitIndex = %d, want 4", leader.CommitIndex())
	}
}

func TestLeaseExpiryTriggersPhaseOne(t *testing.T) {
	net, nodes := newCluster(3, WithLeaseDuration(30*time.Millisecond))
	leader := nodes[0]
	if _, err := leader.Propose("a"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	preparesBefore, _, _ := net.Stats()
	time.Sleep(60 * time.Millisecond)
	if leader.IsLeader() {
		t.Fatal("lease should have expired")
	}
	if _, err := leader.Propose("b"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	preparesAfter, _, _ := net.Stats()
	if preparesAfter <= preparesBefore {
		t.Fatal("expired lease must trigger a new Phase 1")
	}
}

func TestHoleFillingByNewLeader(t *testing.T) {
	_, nodes := newCluster(3)
	// Commit slot 1 normally.
	if _, err := nodes[0].Propose("a"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	// Inject an accepted-but-uncommitted entry at slot 3 on a majority,
	// leaving slot 2 as a hole. Ballot 99 outranks anything so far.
	injected := Ballot{Num: 99, Node: "n1"}
	for _, n := range nodes[:2] {
		rep := n.Handle(Accept{Slot: 3, Ballot: injected, Value: "orphan", From: "n1"}).(Accepted)
		if !rep.OK {
			t.Fatal("injected accept should succeed")
		}
	}
	// A new leader must repair: slot 2 becomes a no-op, slot 3 keeps the
	// highest-ballot value, and the new proposal lands at slot 4.
	slot, err := nodes[1].Propose("x")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if slot != 4 {
		t.Fatalf("slot = %d, want 4", slot)
	}
	for i, n := range nodes {
		if got := n.CommitIndex(); got != 4 {
			t.Fatalf("node %d commitIndex = %d, want 4", i, got)
		}
		e2, _ := n.LogEntry(2)
		if e2.Value != NoOpValue || !e2.Committed {
			t.Fatalf("node %d slot 2 = %+v, want committed no-op", i, e2)
		}
		e3, _ := n.LogEntry(3)
		if e3.Value != "orphan" || !e3.Committed {
			t.Fatalf("node %d slot 3 = %+v, want committed %q", i, e3, "orphan")
		}
	}
}

func TestCommitMessageTeachesMissingFollower(t *testing.T) {
	net, nodes := newCluster(5, WithLeaseDuration(30*time.Millisecond))
	// n5 never sees the accept traffic.
	net.Cut("n1", "n5")
	if _, err := nodes[0].Propose("replicated"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	// Commit messages are cut too, so n5 is still ignorant.
	if _, ok := nodes[4].LogEntry(1); ok {
		t.Fatal("n5 should not have the entry while cut off")
	}
	net.Heal()
	// Let the lease expire so the next proposal runs Phase 1 and its
	// repair teaches n5 the missing entry.
	time.Sleep(60 * time.Millisecond)
	if _, err := nodes[0].Propose("second"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	e, ok := nodes[4].LogEntry(1)
	if !ok || e.Value != "replicated" || !e.Committed {
		t.Fatalf("n5 log[1] = %+v (ok=%v), want committed %q", e, ok, "replicated")
	}
	if nodes[4].CommitIndex() != 2 {
		t.Fatalf("n5 commitIndex = %d, want 2", nodes[4].CommitIndex())
	}
}
