package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func newCluster(t *testing.T, size int) (*Network, []*Node) {
	t.Helper()
	net := NewNetwork()
	nodes := make([]*Node, size)
	for i := 0; i < size; i++ {
		nodes[i] = NewNode(i+1, net)
		nodes[i].SetLeaseDuration(time.Minute)
	}
	return net, nodes
}

func mustBecomeLeader(t *testing.T, n *Node) {
	t.Helper()
	if !n.BecomeLeader() {
		t.Fatalf("node %d failed to become leader", n.ID())
	}
}

func mustPropose(t *testing.T, n *Node, v string) uint64 {
	t.Helper()
	slot, err := n.Propose(v)
	if err != nil {
		t.Fatalf("propose %q on node %d: %v", v, n.ID(), err)
	}
	return slot
}

func appliedValues(n *Node) []string {
	var out []string
	for _, e := range n.Applied() {
		if e.Noop {
			out = append(out, "<noop>")
		} else {
			out = append(out, e.Value)
		}
	}
	return out
}

func TestBasicCommitAllNodes(t *testing.T) {
	_, nodes := newCluster(t, 5)
	leader := nodes[0]
	mustBecomeLeader(t, leader)

	for i, v := range []string{"a", "b", "c"} {
		if slot := mustPropose(t, leader, v); slot != uint64(i+1) {
			t.Fatalf("slot = %d, want %d", slot, i+1)
		}
	}
	for _, n := range nodes {
		got := appliedValues(n)
		want := []string{"a", "b", "c"}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("node %d applied %v, want %v", n.ID(), got, want)
		}
		if n.CommitIndex() != 3 {
			t.Fatalf("node %d commitIndex = %d, want 3", n.ID(), n.CommitIndex())
		}
	}
}

func TestLeaseSkipsPhase1(t *testing.T) {
	_, nodes := newCluster(t, 5)
	leader := nodes[0]
	mustBecomeLeader(t, leader)

	p1, _ := leader.Stats()
	if p1 != 1 {
		t.Fatalf("phase1 rounds after election = %d, want 1", p1)
	}
	for i := 0; i < 10; i++ {
		mustPropose(t, leader, fmt.Sprintf("v%d", i))
	}
	p1, p2 := leader.Stats()
	if p1 != 1 {
		t.Fatalf("lease violated: phase1 rounds = %d, want 1", p1)
	}
	if p2 != 10 {
		t.Fatalf("phase2 rounds = %d, want 10", p2)
	}
}

// Acceptance scenario: 5-node cluster, network partition isolates the old
// leader with a minority; a new leader emerges from the majority, repairs
// uncommitted slots via Phase 1 and, after healing, safely overwrites the
// minority's dirty log entries.
func TestPartitionNewLeaderOverwritesDirtyMinority(t *testing.T) {
	net, nodes := newCluster(t, 5)
	old, newLeader := nodes[0], nodes[2] // n1 and n3

	mustBecomeLeader(t, old)
	mustPropose(t, old, "a")
	mustPropose(t, old, "b")

	// Partition: {n1,n2} | {n3,n4,n5}.
	net.Partition([]int{1, 2}, []int{3, 4, 5})

	// Old leader can no longer reach a majority; its entry lands only on
	// the minority as a dirty, uncommitted record.
	if _, err := old.Propose("dirty"); err != ErrLostMajority {
		t.Fatalf("old leader propose err = %v, want ErrLostMajority", err)
	}
	if old.IsLeader() {
		t.Fatal("old leader should have stepped down after losing majority")
	}
	for _, id := range []int{1, 2} {
		e, ok := nodes[id-1].LogEntry(3)
		if !ok || e.Value != "dirty" {
			t.Fatalf("minority node %d should hold dirty entry, got %+v ok=%v", id, e, ok)
		}
		if nodes[id-1].CommitIndex() != 2 {
			t.Fatalf("minority node %d commitIndex = %d, want 2", id, nodes[id-1].CommitIndex())
		}
	}

	// Majority side elects a new leader. Phase 1 finds no committed or
	// majority-accepted entry beyond slot 2, so slot 3 is free: the new
	// leader commits its own value there under the higher term.
	mustBecomeLeader(t, newLeader)
	mustPropose(t, newLeader, "c")

	e, _ := newLeader.LogEntry(3)
	if e.Value != "c" || e.Term.Round != 2 {
		t.Fatalf("slot 3 on new leader = %+v, want c@round2", e)
	}

	// Heal and let the new leader push one more entry; anti-entropy sync
	// must overwrite the dirty minority logs.
	net.Heal()
	mustPropose(t, newLeader, "d")

	want := []string{"a", "b", "c", "d"}
	for _, n := range nodes {
		got := appliedValues(n)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("node %d applied %v, want %v", n.ID(), got, want)
		}
	}
	// Dirty value is gone everywhere, including the old leader.
	for _, id := range []int{1, 2} {
		e, _ := nodes[id-1].LogEntry(3)
		if e.Value != "c" || e.Noop {
			t.Fatalf("node %d slot 3 = %+v, dirty entry not overwritten", id, e)
		}
	}
	// Old leader stays down; exactly one leader exists.
	leaders := 0
	for _, n := range nodes {
		if n.IsLeader() {
			leaders++
		}
	}
	if leaders != 1 || !newLeader.IsLeader() {
		t.Fatalf("leaders = %d, want exactly n3 as leader", leaders)
	}
}

// Gap repair: a hole at slot 1 (minority only) with slot 2 accepted by a
// majority. The new leader's Phase 1 must fill slot 1 with a no-op and
// adopt the majority-accepted value at slot 2 (Paxos safety).
func TestGapRepairViaPhase1(t *testing.T) {
	net, nodes := newCluster(t, 5)
	term1 := ProposalID{Round: 1, Node: 1}

	// White-box: inject a torn state as if a crashed leader had sent
	// slot 1 to a minority and slot 2 to a majority, committing nothing.
	put := func(id, slot uint64, value string) {
		n := nodes[id-1]
		n.log[slot] = Entry{Slot: slot, Term: term1, Value: value}
		n.promised = term1
		if slot > n.maxSlot {
			n.maxSlot = slot
		}
	}
	put(1, 1, "x")
	put(2, 1, "x") // slot 1: minority only
	put(1, 2, "y")
	put(2, 2, "y")
	put(3, 2, "y") // slot 2: majority, but cannot commit (hole at 1)

	// Isolate the minority so the new leader's Phase 1 only sees the
	// majority state: slot 1 is a hole to fill, slot 2 must be adopted.
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	mustBecomeLeader(t, nodes[2]) // n3

	// Slot 1 must be a no-op; slot 2 must preserve the majority value "y".
	e1, _ := nodes[2].LogEntry(1)
	e2, _ := nodes[2].LogEntry(2)
	if !e1.Noop {
		t.Fatalf("slot 1 = %+v, want no-op", e1)
	}
	if e2.Value != "y" || e2.Noop {
		t.Fatalf("slot 2 = %+v, want adopted value y", e2)
	}

	// Heal and commit one more entry; anti-entropy must repair the
	// minority nodes (including their dirty slot-1 "x" entries).
	net.Heal()
	mustPropose(t, nodes[2], "z")

	// The committed prefix is contiguous on every node: no holes.
	want := []string{"<noop>", "y", "z"}
	for _, n := range nodes {
		got := appliedValues(n)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("node %d applied %v, want %v", n.ID(), got, want)
		}
		if n.CommitIndex() != 3 {
			t.Fatalf("node %d commitIndex = %d, want 3", n.ID(), n.CommitIndex())
		}
	}
	if e, _ := nodes[0].LogEntry(1); !e.Noop || e.Value == "x" {
		t.Fatalf("dirty slot 1 on node 1 not repaired: %+v", e)
	}
}

// A stale accept from an old term must be rejected after a higher promise.
func TestStaleTermRejected(t *testing.T) {
	_, nodes := newCluster(t, 3)
	n1, n2 := nodes[0], nodes[1]

	mustBecomeLeader(t, n1) // term (1,1)
	mustBecomeLeader(t, n2) // term (2,2)

	// n1 lost its lease majority; a direct stale accept must be rejected.
	resp := nodes[2].HandleAccept(AcceptRequest{
		Entry: Entry{Slot: 1, Term: ProposalID{Round: 1, Node: 1}, Value: "stale"},
	})
	if resp.Accepted {
		t.Fatal("stale-term accept was not rejected")
	}
	if _, err := n1.Propose("x"); err != ErrLostMajority && err != ErrNotLeader {
		t.Fatalf("old leader propose err = %v", err)
	}
}

func TestConcurrentPropose(t *testing.T) {
	_, nodes := newCluster(t, 5)
	leader := nodes[0]
	mustBecomeLeader(t, leader)

	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := leader.Propose(fmt.Sprintf("w%d-%d", w, i)); err != nil {
					t.Errorf("propose: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	const total = workers * perWorker
	for _, n := range nodes {
		applied := n.Applied()
		if len(applied) != total {
			t.Fatalf("node %d applied %d entries, want %d", n.ID(), len(applied), total)
		}
		// Contiguity: slot i+1 at position i, no holes, no duplicates.
		for i, e := range applied {
			if e.Slot != uint64(i+1) {
				t.Fatalf("node %d applied[%d].Slot = %d, want %d", n.ID(), i, e.Slot, i+1)
			}
		}
	}
	// All replicas converged to the identical log.
	ref := appliedValues(nodes[0])
	for _, n := range nodes[1:] {
		if fmt.Sprint(appliedValues(n)) != fmt.Sprint(ref) {
			t.Fatalf("node %d log diverged", n.ID())
		}
	}
}

func TestConcurrentElectionAndPartition(t *testing.T) {
	net, nodes := newCluster(t, 5)
	mustBecomeLeader(t, nodes[0])
	mustPropose(t, nodes[0], "seed")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				net.Partition([]int{1, 2}, []int{3, 4, 5})
			} else {
				net.Heal()
			}
		}(i)
	}
	for _, n := range nodes {
		wg.Add(1)
		go func(n *Node) {
			defer wg.Done()
			n.BecomeLeader()
			_, _ = n.Propose("x")
		}(n)
	}
	wg.Wait()

	// Whatever happened, the applied prefix of every node must be
	// contiguous starting at slot 1.
	for _, n := range nodes {
		for i, e := range n.Applied() {
			if e.Slot != uint64(i+1) {
				t.Fatalf("node %d applied[%d].Slot = %d, hole detected", n.ID(), i, e.Slot)
			}
		}
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	net, nodes := newCluster(t, 5)
	mustBecomeLeader(t, nodes[0])
	for i := 0; i < 50; i++ {
		mustPropose(t, nodes[0], fmt.Sprintf("v%d", i))
	}
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	mustBecomeLeader(t, nodes[2])
	mustPropose(t, nodes[2], "after-partition")
	net.Heal()
	mustPropose(t, nodes[2], "after-heal")

	// Allow any transient runtime goroutines to settle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
