package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

const testLease = 2 * time.Second

func newCluster(ids ...string) (*Network, map[string]*Node) {
	net := NewNetwork()
	nodes := make(map[string]*Node)
	for _, id := range ids {
		nodes[id] = NewNode(net, id, testLease)
	}
	return net, nodes
}

func setOp(k, v string) Op { return Op{Kind: OpSet, Key: k, Value: v} }

// waitGoroutines settles and reports the goroutine count.
func goroutineCount() int {
	time.Sleep(50 * time.Millisecond)
	return runtime.NumGoroutine()
}

func TestBasicCommitAndConvergence(t *testing.T) {
	_, nodes := newCluster("A", "B", "C", "D", "E")
	a := nodes["A"]

	if _, err := a.Propose(setOp("x", "1")); err != nil {
		t.Fatalf("propose 1: %v", err)
	}
	if _, err := a.Propose(setOp("y", "2")); err != nil {
		t.Fatalf("propose 2: %v", err)
	}

	for id, n := range nodes {
		if got := n.CommitIndex(); got != 2 {
			t.Fatalf("node %s commitIndex=%d, want 2", id, got)
		}
		sm := n.StateMachine()
		if sm["x"] != "1" || sm["y"] != "2" {
			t.Fatalf("node %s sm=%v", id, sm)
		}
	}
}

func TestLeaseSkipsPhase1(t *testing.T) {
	_, nodes := newCluster("A", "B", "C")
	a := nodes["A"]

	for i := 0; i < 5; i++ {
		if _, err := a.Propose(setOp(fmt.Sprintf("k%d", i), "v")); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	if a.PrepareRounds != 1 {
		t.Fatalf("PrepareRounds=%d, want 1 (lease must bypass Phase 1)", a.PrepareRounds)
	}
	if a.AcceptRounds != 5 {
		t.Fatalf("AcceptRounds=%d, want 5", a.AcceptRounds)
	}
	if a.CommitIndex() != 5 {
		t.Fatalf("commitIndex=%d, want 5", a.CommitIndex())
	}
}

func TestGapFillWithNoop(t *testing.T) {
	_, nodes := newCluster("A", "B", "C")
	// Dirty acceptor state: slots 1 and 3 accepted, slot 2 is a hole.
	for _, n := range nodes {
		n.InjectAccepted(1, Ballot{N: 1, Node: "A"}, setOp("a", "1"))
		n.InjectAccepted(3, Ballot{N: 1, Node: "A"}, setOp("c", "3"))
	}

	a := nodes["A"]
	if err := a.BecomeLeader(); err != nil {
		t.Fatalf("BecomeLeader: %v", err)
	}

	for id, n := range nodes {
		e, ok := n.LogEntry(2)
		if !ok || e.Op.Kind != OpNoop {
			t.Fatalf("node %s slot2=%+v ok=%v, want noop hole fill", id, e, ok)
		}
		if got := n.CommitIndex(); got != 3 {
			t.Fatalf("node %s commitIndex=%d, want 3 (gap-free)", id, got)
		}
		sm := n.StateMachine()
		if sm["a"] != "1" || sm["c"] != "3" {
			t.Fatalf("node %s sm=%v", id, sm)
		}
	}
}

func TestCommitEngineAppliesInOrder(t *testing.T) {
	_, nodes := newCluster("A", "B", "C")
	n := nodes["B"]
	bal := Ballot{N: 1, Node: "A"}

	// Commit notifications arrive out of order: slot 2 before slot 1.
	n.handleCommit(CommitMsg{Slot: 2, Bal: bal, Op: setOp("b", "2")})
	if got := n.CommitIndex(); got != 0 {
		t.Fatalf("commitIndex=%d after hole, want 0 (no skip)", got)
	}
	n.handleCommit(CommitMsg{Slot: 1, Bal: bal, Op: setOp("a", "1")})
	if got := n.CommitIndex(); got != 2 {
		t.Fatalf("commitIndex=%d, want 2", got)
	}
	sm := n.StateMachine()
	if sm["a"] != "1" || sm["b"] != "2" {
		t.Fatalf("sm=%v", sm)
	}
}

// TestPartitionAcceptance is the acceptance scenario: a 5-node cluster is
// partitioned [A,B]|[C,D,E]. Old leader A is isolated; C becomes the new
// majority leader, repairs uncommitted slots via Phase 1 and safely
// overwrites the minority's dirty log after healing.
func TestPartitionAcceptance(t *testing.T) {
	net, nodes := newCluster("A", "B", "C", "D", "E")
	a, c := nodes["A"], nodes["C"]

	// Slots 1-2 committed by A on a full majority before the partition.
	if _, err := a.Propose(setOp("x", "1")); err != nil {
		t.Fatalf("propose x: %v", err)
	}
	if _, err := a.Propose(setOp("y", "2")); err != nil {
		t.Fatalf("propose y: %v", err)
	}

	// Network partition: old leader A is in the minority.
	net.Partition([]string{"A", "B"}, []string{"C", "D", "E"})

	// A keeps a valid lease and tries to commit slot 3: only B is reachable,
	// so the write lands as a dirty, uncommitted minority entry.
	if _, err := a.Propose(setOp("z", "dirty")); err != ErrNoMajority {
		t.Fatalf("old leader propose under partition: err=%v, want ErrNoMajority", err)
	}
	for _, id := range []string{"A", "B"} {
		e, ok := nodes[id].LogEntry(3)
		if !ok || e.Op.Value != "dirty" {
			t.Fatalf("node %s slot3=%+v ok=%v, want dirty entry", id, e, ok)
		}
	}
	if a.CommitIndex() != 2 {
		t.Fatalf("old leader commitIndex=%d, want 2 (slot 3 must not commit)", a.CommitIndex())
	}

	// C campaigns in the majority partition: Phase 1 promises carry the log,
	// slots 1-2 are repaired/re-committed, and C becomes leader.
	if err := c.BecomeLeader(); err != nil {
		t.Fatalf("C BecomeLeader: %v", err)
	}
	if !c.IsLeader() {
		t.Fatal("C should be leader")
	}
	// New leader commits its own slot 3, bypassing Phase 1 under its lease.
	slot, err := c.Propose(setOp("z", "new"))
	if err != nil {
		t.Fatalf("C propose: %v", err)
	}
	if slot != 3 {
		t.Fatalf("C proposed slot=%d, want 3", slot)
	}
	for _, id := range []string{"C", "D", "E"} {
		if got := nodes[id].CommitIndex(); got != 3 {
			t.Fatalf("node %s commitIndex=%d, want 3", id, got)
		}
		if sm := nodes[id].StateMachine(); sm["z"] != "new" {
			t.Fatalf("node %s sm[z]=%q, want new", id, sm["z"])
		}
	}

	// Heal and sync: the minority's dirty slot 3 is safely overwritten.
	net.Heal()
	if err := c.SyncAll(); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	for id, n := range nodes {
		e, ok := n.LogEntry(3)
		if !ok || e.Op.Value != "new" {
			t.Fatalf("node %s slot3=%+v ok=%v, want overwritten value new", id, e, ok)
		}
		if got := n.CommitIndex(); got != 3 {
			t.Fatalf("node %s commitIndex=%d, want 3", id, got)
		}
		sm := n.StateMachine()
		if sm["x"] != "1" || sm["y"] != "2" || sm["z"] != "new" {
			t.Fatalf("node %s sm=%v", id, sm)
		}
	}

	// Safety: the stale old leader cannot commit with its old ballot.
	if _, err := a.Propose(setOp("w", "stale")); err != ErrNoMajority {
		t.Fatalf("stale leader propose: err=%v, want ErrNoMajority", err)
	}
	if a.IsLeader() {
		t.Fatal("old leader must step down after rejection by higher ballot")
	}
	for id, n := range nodes {
		if _, ok := n.StateMachine()["w"]; ok {
			t.Fatalf("node %s applied stale write", id)
		}
	}
}

func TestConcurrentPropose(t *testing.T) {
	_, nodes := newCluster("A", "B", "C", "D", "E")
	a := nodes["A"]
	if err := a.BecomeLeader(); err != nil {
		t.Fatalf("BecomeLeader: %v", err)
	}

	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%02d", i)
			if _, err := a.Propose(setOp(key, "v")); err != nil {
				errs <- fmt.Errorf("propose %s: %w", key, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	for id, nd := range nodes {
		if got := nd.CommitIndex(); got != n {
			t.Fatalf("node %s commitIndex=%d, want %d (contiguous, no holes)", id, got, n)
		}
		sm := nd.StateMachine()
		if len(sm) != n {
			t.Fatalf("node %s applied %d keys, want %d", id, len(sm), n)
		}
		for slot := 1; slot <= n; slot++ {
			if _, ok := nd.CommittedEntry(slot); ok == false {
				t.Fatalf("node %s missing committed slot %d", id, slot)
			}
		}
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := goroutineCount()
	net, nodes := newCluster("A", "B", "C", "D", "E")
	a := nodes["A"]
	for i := 0; i < 3; i++ {
		if _, err := a.Propose(setOp(fmt.Sprintf("k%d", i), "v")); err != nil {
			t.Fatalf("propose: %v", err)
		}
	}
	net.Partition([]string{"A"}, []string{"B", "C", "D", "E"})
	if err := nodes["B"].BecomeLeader(); err != nil {
		t.Fatalf("B BecomeLeader: %v", err)
	}
	net.Heal()
	if err := nodes["B"].SyncAll(); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if after := goroutineCount(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
