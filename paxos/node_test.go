package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

const testLease = 60 * time.Millisecond

func newCluster(t *testing.T, n int) (*Network, []*Node) {
	t.Helper()
	net := NewNetwork()
	nodes := make([]*Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = NewNode(net, i, testLease)
	}
	return net, nodes
}

func waitLeaseExpiry() { time.Sleep(testLease + 40*time.Millisecond) }

func TestBasicElectionAndPropose(t *testing.T) {
	_, nodes := newCluster(t, 5)
	if err := nodes[0].BecomeLeader(); err != nil {
		t.Fatalf("BecomeLeader: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := nodes[0].Propose(fmt.Sprintf("v%d", i)); err != nil {
			t.Fatalf("Propose %d: %v", i, err)
		}
	}
	want := make([]string, 10)
	for i := range want {
		want[i] = fmt.Sprintf("v%d", i)
	}
	for _, nd := range nodes {
		got := nd.Applied()
		if len(got) != 10 {
			t.Fatalf("node%d applied %d entries, want 10", nd.ID(), len(got))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("node%d slot %d = %q, want %q", nd.ID(), i+1, got[i], want[i])
			}
		}
		if nd.CommitIndex() != 10 {
			t.Fatalf("node%d commitIndex=%d, want 10", nd.ID(), nd.CommitIndex())
		}
	}
}

func TestLeaseSkipsPhase1AndBlocksOthers(t *testing.T) {
	_, nodes := newCluster(t, 3)
	if err := nodes[0].BecomeLeader(); err != nil {
		t.Fatal(err)
	}
	// While the lease is valid, another node's Prepare must be rejected.
	if p := nodes[1].HandlePrepare(PrepareRequest{From: 1, Ballot: Ballot{Num: 99, Node: 1}, Since: 1}); p.OK {
		t.Fatal("Prepare accepted despite valid lease held by node0")
	}
	// After expiry, a higher ballot wins.
	waitLeaseExpiry()
	if p := nodes[1].HandlePrepare(PrepareRequest{From: 1, Ballot: Ballot{Num: 99, Node: 1}, Since: 1}); !p.OK {
		t.Fatal("Prepare rejected after lease expiry")
	}
}

func TestProposeRequiresLeaderAndLease(t *testing.T) {
	_, nodes := newCluster(t, 3)
	if _, err := nodes[1].Propose("x"); err != ErrNotLeader {
		t.Fatalf("err=%v, want ErrNotLeader", err)
	}
	if err := nodes[0].BecomeLeader(); err != nil {
		t.Fatal(err)
	}
	waitLeaseExpiry()
	if _, err := nodes[0].Propose("x"); err != ErrLeaseExpired {
		t.Fatalf("err=%v, want ErrLeaseExpired", err)
	}
	// Heartbeat renews the lease and Propose works again.
	if !nodes[0].Heartbeat() {
		t.Fatal("heartbeat failed")
	}
	if _, err := nodes[0].Propose("x"); err != nil {
		t.Fatalf("Propose after heartbeat: %v", err)
	}
}

// TestPartitionLeaderReplacement is the acceptance scenario:
// 5 nodes, old leader partitioned into the minority, a new leader emerges
// in the majority, repairs uncommitted slots via Phase 1, and after healing
// the minority's dirty log is safely overwritten.
func TestPartitionLeaderReplacement(t *testing.T) {
	net, nodes := newCluster(t, 5)
	old := nodes[0]
	if err := old.BecomeLeader(); err != nil {
		t.Fatal(err)
	}
	// Commit entries 1..3 cluster-wide.
	for i := 1; i <= 3; i++ {
		if _, err := old.Propose(fmt.Sprintf("committed-%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	// Partition: {0,1} minority, {2,3,4} majority.
	net.Cut([]int{0, 1}, []int{2, 3, 4})

	// Old leader writes slot 4 into the minority only (dirty, uncommitted).
	req := AcceptRequest{From: 0, Ballot: Ballot{Num: 1, Node: 0}, Slot: 4, Value: "dirty-minority"}
	if a := nodes[0].HandleAccept(req); !a.OK {
		t.Fatal("self accept failed")
	}
	if a := nodes[1].HandleAccept(req); !a.OK {
		t.Fatal("minority accept failed")
	}
	if v, _ := nodes[1].SlotValue(4); v != "dirty-minority" {
		t.Fatalf("minority dirty value missing: %q", v)
	}

	// Majority waits out the lease, then elects a new leader.
	waitLeaseExpiry()
	newLeader := nodes[2]
	if err := newLeader.BecomeLeader(); err != nil {
		t.Fatalf("new leader election: %v", err)
	}
	// The majority never saw dirty slot 4, so the new leader's log ends at 3;
	// its next proposals occupy slots 4 and 5.
	if ci := newLeader.CommitIndex(); ci != 3 {
		t.Fatalf("new leader commitIndex=%d, want 3", ci)
	}
	// New leader appends fresh entries under its lease.
	if _, err := newLeader.Propose("post-partition-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := newLeader.Propose("post-partition-6"); err != nil {
		t.Fatal(err)
	}

	// Old leader is fully superseded: its accepts are rejected by the majority.
	stale := AcceptRequest{From: 0, Ballot: Ballot{Num: 1, Node: 0}, Slot: 7, Value: "stale"}
	if a := nodes[3].HandleAccept(stale); a.OK {
		t.Fatal("majority node accepted from superseded leader")
	}

	// Heal: the new leader's commits converge the minority, overwriting dirt.
	net.Heal()
	if !newLeader.Heartbeat() {
		t.Fatal("heartbeat after heal failed")
	}
	if _, err := newLeader.Propose("post-heal-7"); err != nil {
		t.Fatal(err)
	}

	want := []string{"committed-1", "committed-2", "committed-3",
		"post-partition-5", "post-partition-6", "post-heal-7"}
	for _, nd := range nodes {
		got := nd.Applied()
		if len(got) != len(want) {
			t.Fatalf("node%d applied %v, want %v", nd.ID(), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("node%d slot %d = %q, want %q", nd.ID(), i+1, got[i], want[i])
			}
		}
	}
	// The dirty value must be gone everywhere, including the minority nodes:
	// slot 4 now holds the value chosen by the majority's new leader.
	for _, id := range []int{0, 1} {
		if v, _ := nodes[id].SlotValue(4); v != "post-partition-5" {
			t.Fatalf("node%d slot 4 = %q, dirty value survived", id, v)
		}
	}
}

// TestHoleRepair: slots 1,2 committed; slot 3 accepted only on an isolated
// node (invisible to Phase 1); slot 5 accepted with a real value on the
// majority but never committed. The new leader must commit 1..5 with no
// holes, keeping the majority value at 5 and no-oping the gaps; after the
// heal, the isolated node's dirty slot 3 is overwritten by the no-op.
func TestHoleRepair(t *testing.T) {
	net, nodes := newCluster(t, 5)
	if err := nodes[0].BecomeLeader(); err != nil {
		t.Fatal(err)
	}
	nodes[0].Propose("a")
	nodes[0].Propose("b")

	// Slot 3 only on node 4 (dirty), slot 5 accepted on majority only.
	dirty := AcceptRequest{From: 0, Ballot: Ballot{Num: 1, Node: 0}, Slot: 3, Value: "dirty-3"}
	nodes[4].HandleAccept(dirty)
	hole5 := AcceptRequest{From: 0, Ballot: Ballot{Num: 1, Node: 0}, Slot: 5, Value: "real-5"}
	for _, id := range []int{1, 2, 3} {
		if a := nodes[id].HandleAccept(hole5); !a.OK {
			t.Fatalf("node%d accept slot5 failed", id)
		}
	}

	// Isolate node 4 so its dirty slot 3 is invisible to Phase 1.
	net.Isolate(4)
	waitLeaseExpiry()
	if err := nodes[1].BecomeLeader(); err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", NoopValue, NoopValue, "real-5"}
	got := nodes[1].Applied()
	if len(got) != len(want) {
		t.Fatalf("applied=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("slot %d = %q, want %q", i+1, got[i], want[i])
		}
	}
	// Heal, converge everyone via heartbeat repair, and verify the dirty
	// slot 3 is overwritten by the committed no-op.
	net.Heal()
	if !nodes[1].Heartbeat() {
		t.Fatal("heartbeat after heal failed")
	}
	nodes[1].Propose("c")
	for _, nd := range nodes {
		if v, _ := nd.SlotValue(3); v != NoopValue {
			t.Fatalf("node%d slot3=%q, want noop", nd.ID(), v)
		}
		if v, _ := nd.SlotValue(5); v != "real-5" {
			t.Fatalf("node%d slot5=%q, want real-5", nd.ID(), v)
		}
	}
}

// TestNoMajorityCannotElect: a node isolated from the majority must fail.
func TestNoMajorityCannotElect(t *testing.T) {
	net, nodes := newCluster(t, 5)
	net.Isolate(0)
	waitLeaseExpiry()
	if err := nodes[0].BecomeLeader(); err != ErrNoMajority {
		t.Fatalf("err=%v, want ErrNoMajority", err)
	}
}

// TestCommittedValueNeverLost: a value committed by a majority must survive
// any subsequent election, even if the new leader never saw it accepted.
func TestCommittedValueNeverLost(t *testing.T) {
	net, nodes := newCluster(t, 5)
	if err := nodes[0].BecomeLeader(); err != nil {
		t.Fatal(err)
	}
	nodes[0].Propose("durable") // committed on nodes 0,1,2,3,4
	// Partition so the new leader comes from nodes that missed slot 2's
	// accept: simulate by cutting 0,1,2 (which hold a pending slot 2).
	pending := AcceptRequest{From: 0, Ballot: Ballot{Num: 2, Node: 0}, Slot: 2, Value: "pending"}
	for _, id := range []int{0, 1, 2} {
		nodes[id].HandleAccept(pending) // majority accepted => potentially chosen
	}
	net.Cut([]int{0, 1, 2}, []int{3, 4})
	waitLeaseExpiry()
	// Only 3,4 visible to each other: no majority. Heal one link group.
	net.Heal()
	net.Cut([]int{0}, []int{1, 2, 3, 4})
	waitLeaseExpiry()
	if err := nodes[3].BecomeLeader(); err != nil {
		t.Fatal(err)
	}
	// Slot 2 must be "pending" (it was accepted by a majority at ballot 2).
	if v, _ := nodes[3].SlotValue(2); v != "pending" {
		t.Fatalf("slot2=%q, want pending (majority-accepted value lost)", v)
	}
}

func TestConcurrentProposalsAndCommits(t *testing.T) {
	_, nodes := newCluster(t, 5)
	if err := nodes[0].BecomeLeader(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				for {
					_, err := nodes[0].Propose(fmt.Sprintf("w%d-%d", w, i))
					if err == nil {
						break
					}
					if err == ErrLeaseExpired {
						nodes[0].Heartbeat()
						continue
					}
					t.Errorf("propose: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	ci := nodes[0].CommitIndex()
	if ci != 200 {
		t.Fatalf("commitIndex=%d, want 200", ci)
	}
	// All followers converge to the identical log.
	want := nodes[0].Applied()
	for _, nd := range nodes[1:] {
		got := nd.Applied()
		if len(got) != len(want) {
			t.Fatalf("node%d applied %d, want %d", nd.ID(), len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("node%d diverged at slot %d", nd.ID(), i+1)
			}
		}
	}
}

// TestNoGoroutineLeak: the engine spawns no background goroutines; assert
// the goroutine count is stable across heavy use.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	_, nodes := newCluster(t, 5)
	if err := nodes[0].BecomeLeader(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		nodes[0].Propose("x")
	}
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
