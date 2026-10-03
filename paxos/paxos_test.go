package paxos

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

const testLease = time.Hour // effectively non-expiring lease for tests

// newCluster builds a fully connected cluster of n nodes.
func newCluster(n int) (*Network, []*Node) {
	net := NewNetwork()
	nodes := make([]*Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = NewNode(i, n, testLease)
		net.Attach(nodes[i])
	}
	return net, nodes
}

func committedValues(n *Node) []string {
	c := n.Committed()
	out := make([]string, len(c))
	for i, e := range c {
		out[i] = e.Value
	}
	return out
}

func assertCommitted(t *testing.T, n *Node, want []string) {
	t.Helper()
	got := committedValues(n)
	if len(got) != len(want) {
		t.Fatalf("node %d committed %v, want %v", n.ID(), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("node %d committed %v, want %v", n.ID(), got, want)
		}
	}
}

// TestBasicConsensus: campaign once, propose several entries, every node
// commits the same gap-free prefix.
func TestBasicConsensus(t *testing.T) {
	_, nodes := newCluster(5)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	for i := 0; i < 5; i++ {
		if _, err := nodes[0].Propose(fmt.Sprintf("v%d", i)); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	want := []string{"v0", "v1", "v2", "v3", "v4"}
	for _, n := range nodes {
		assertCommitted(t, n, want)
		if n.CommitIndex() != len(n.Log()) {
			t.Fatalf("node %d: commitIndex %d != log len %d (hole?)",
				n.ID(), n.CommitIndex(), len(n.Log()))
		}
	}
}

// TestLeaseBypassesPhase1: with a valid lease, proposing must not emit any
// further Prepare messages (Phase 1 is skipped on the happy path).
func TestLeaseBypassesPhase1(t *testing.T) {
	net, nodes := newCluster(5)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	preparesAfterCampaign, _, _ := net.Stats()
	if preparesAfterCampaign != 4 { // 4 remote prepares (self is local)
		t.Fatalf("campaign sent %d prepares, want 4", preparesAfterCampaign)
	}
	for i := 0; i < 10; i++ {
		if _, err := nodes[0].Propose("x"); err != nil {
			t.Fatalf("propose: %v", err)
		}
	}
	prepares, accepts, commits := net.Stats()
	if prepares != preparesAfterCampaign {
		t.Fatalf("lease violated: prepares grew %d -> %d", preparesAfterCampaign, prepares)
	}
	if accepts != 10*4 || commits != 10*4+4 { // +4: campaign's commit broadcast
		t.Fatalf("accepts=%d commits=%d, want 40/44", accepts, commits)
	}
}

// TestProposeRequiresLeadership: non-leaders and expired leases are rejected.
func TestProposeRequiresLeadership(t *testing.T) {
	_, nodes := newCluster(3)
	if _, err := nodes[1].Propose("x"); err != ErrNotLeader {
		t.Fatalf("got %v, want ErrNotLeader", err)
	}
	// Leader with an expired lease must refuse.
	nodes[0].lease = time.Nanosecond
	nodes[0].SetClock(time.Now)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	time.Sleep(time.Millisecond)
	if _, err := nodes[0].Propose("x"); err != ErrNotLeader {
		t.Fatalf("expired lease: got %v, want ErrNotLeader", err)
	}
}

// TestPartitionNewLeaderRepairsAndOverwrites is the acceptance scenario:
//
//  1. node0 leads a 5-node cluster and commits v0.
//  2. The cluster partitions {0,1} | {2,3,4}. The old leader keeps its
//     lease but cannot reach a majority; its write ("dirty") lands only on
//     the minority and must never be committed.
//  3. node2 wins Phase 1 in the majority partition and commits v1, v2.
//  4. After healing, a repair campaign backfills the minority with the
//     chosen values, safely overwriting the dirty slot; all 5 nodes
//     converge on the identical gap-free committed log.
func TestPartitionNewLeaderRepairsAndOverwrites(t *testing.T) {
	net, nodes := newCluster(5)

	if !nodes[0].Campaign() {
		t.Fatal("initial campaign failed")
	}
	if _, err := nodes[0].Propose("v0"); err != nil {
		t.Fatalf("propose v0: %v", err)
	}
	for _, n := range nodes {
		assertCommitted(t, n, []string{"v0"})
	}

	// Partition: old leader node0 isolated with node1.
	net.Partition([]int{0, 1}, []int{2, 3, 4})

	// Old leader still holds its lease and attempts a write: it must fail
	// to reach a majority and must not commit.
	if _, err := nodes[0].Propose("dirty"); err != ErrNoMajority {
		t.Fatalf("old leader propose: got %v, want ErrNoMajority", err)
	}
	if nodes[0].CommitIndex() != 1 {
		t.Fatalf("old leader committed under partition: ci=%d", nodes[0].CommitIndex())
	}
	if got := nodes[0].Log()[1].Value; got != "dirty" {
		t.Fatalf("expected dirty slot on minority, got %q", got)
	}

	// Majority partition elects node2 via Phase 1 and continues the log.
	if !nodes[2].Campaign() {
		t.Fatal("majority campaign failed")
	}
	if _, err := nodes[2].Propose("v1"); err != nil {
		t.Fatalf("propose v1: %v", err)
	}
	if _, err := nodes[2].Propose("v2"); err != nil {
		t.Fatalf("propose v2: %v", err)
	}
	for _, id := range []int{2, 3, 4} {
		assertCommitted(t, nodes[id], []string{"v0", "v1", "v2"})
	}
	// Minority is stuck at the old committed prefix.
	assertCommitted(t, nodes[0], []string{"v0"})
	assertCommitted(t, nodes[1], []string{"v0"})

	// Heal and run a repair campaign: the new leader backfills the
	// minority and overwrites the dirty slot with the chosen value.
	net.Heal()
	if !nodes[2].Campaign() {
		t.Fatal("repair campaign failed")
	}

	want := []string{"v0", "v1", "v2"}
	for _, n := range nodes {
		assertCommitted(t, n, want)
		if n.CommitIndex() != len(n.Log()) {
			t.Fatalf("node %d: hole detected, ci=%d len=%d",
				n.ID(), n.CommitIndex(), len(n.Log()))
		}
	}
	// Dirty value must be gone everywhere, including the minority's raw log.
	for _, id := range []int{0, 1} {
		for i, e := range nodes[id].Log() {
			if e.Value == "dirty" {
				t.Fatalf("node %d slot %d still holds dirty value", id, i)
			}
		}
	}
	// Old leader must have stepped down after seeing the higher ballot.
	if nodes[0].IsLeader() {
		t.Fatal("old leader did not step down after heal")
	}
	if !nodes[2].IsLeader() {
		t.Fatal("node2 should be leader")
	}
}

// TestHoleRepair: holes (slots with no chosen value anywhere) are filled
// with committed no-ops during Phase 1, keeping the log gap-free.
func TestHoleRepair(t *testing.T) {
	net, nodes := newCluster(5)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	if _, err := nodes[0].Propose("v0"); err != nil {
		t.Fatalf("propose: %v", err)
	}
	// Simulate a lost Accept: slot 2 exists on node1 only, slot 1 was
	// never proposed anywhere -> a hole.
	b := nodes[0].Ballot()
	if _, ok := net.Accept(0, 1, AcceptReq{Ballot: b, From: 0, Index: 2, Entry: Entry{Ballot: b, Value: "orphan"}}); !ok {
		t.Fatal("inject accept failed")
	}

	// New leader in the majority partition repairs the log.
	net.Partition([]int{0, 1}, []int{2, 3, 4})
	if !nodes[2].Campaign() {
		t.Fatal("repair campaign failed")
	}
	net.Heal()
	if !nodes[2].Campaign() {
		t.Fatal("converge campaign failed")
	}

	// Slot 1 must be a committed no-op on every node; slot 2 carries the
	// orphan value (re-proposed safely by the new leader).
	for _, n := range nodes {
		c := n.Committed()
		if len(c) != 3 {
			t.Fatalf("node %d committed %d entries, want 3", n.ID(), len(c))
		}
		if !c[1].IsNoop() {
			t.Fatalf("node %d slot 1 = %q, want committed no-op hole fill", n.ID(), c[1].Value)
		}
		if c[0].Value != "v0" || c[2].Value != "orphan" {
			t.Fatalf("node %d committed %v", n.ID(), committedValues(n))
		}
	}
}

// TestSplitCampaignMinorityLoses: a candidate in a minority partition can
// never become leader.
func TestSplitCampaignMinorityLoses(t *testing.T) {
	net, nodes := newCluster(5)
	net.Partition([]int{0, 1}, []int{2, 3, 4})
	if nodes[0].Campaign() {
		t.Fatal("minority candidate must not win")
	}
	if !nodes[3].Campaign() {
		t.Fatal("majority candidate must win")
	}
}

// TestConcurrentProposers: concurrent Propose calls on one leader are
// serialized and produce a gap-free, fully committed log.
func TestConcurrentProposers(t *testing.T) {
	_, nodes := newCluster(5)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	const writers = 8
	const perWriter = 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := nodes[0].Propose(fmt.Sprintf("w%d-%d", w, i)); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent propose: %v", err)
	}
	total := writers * perWriter
	for _, n := range nodes {
		if n.CommitIndex() != total || len(n.Log()) != total {
			t.Fatalf("node %d: ci=%d len=%d, want %d (hole or loss)",
				n.ID(), n.CommitIndex(), len(n.Log()), total)
		}
	}
	// All nodes must hold byte-identical logs.
	ref := nodes[0].Log()
	for _, n := range nodes[1:] {
		for i, e := range n.Log() {
			if e != ref[i] {
				t.Fatalf("node %d slot %d diverges: %+v vs %+v", n.ID(), i, e, ref[i])
			}
		}
	}
}
