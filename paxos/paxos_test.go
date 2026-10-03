package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func mustCampaign(t *testing.T, n *Node) {
	t.Helper()
	if err := n.Campaign(); err != nil {
		t.Fatalf("node %d campaign: %v", n.ID(), err)
	}
}

func mustPropose(t *testing.T, n *Node, op Op) uint64 {
	t.Helper()
	slot, err := n.Propose(op)
	if err != nil {
		t.Fatalf("node %d propose %+v: %v", n.ID(), op, err)
	}
	return slot
}

func TestBasicProposeCommit(t *testing.T) {
	c := NewCluster(5, nil)
	leader := c.Node(1)
	mustCampaign(t, leader)
	if !leader.IsLeader() {
		t.Fatal("expected node 1 to hold the lease")
	}
	mustPropose(t, leader, Op{Kind: OpSet, Key: "a", Value: "1"})
	mustPropose(t, leader, Op{Kind: OpSet, Key: "b", Value: "2"})

	for _, n := range c.Nodes {
		if got := n.CommitIndex(); got != 2 {
			t.Fatalf("node %d commitIndex=%d, want 2", n.ID(), got)
		}
		if v, ok := n.KV().Get("b"); !ok || v != "2" {
			t.Fatalf("node %d missing committed value", n.ID())
		}
	}
}

func TestLeaseSkipsPhase1(t *testing.T) {
	c := NewCluster(3, nil)
	leader := c.Node(1)
	mustCampaign(t, leader)
	if got := leader.Prepares(); got != 1 {
		t.Fatalf("prepares=%d, want 1", got)
	}
	for i := 0; i < 10; i++ {
		mustPropose(t, leader, Op{Kind: OpSet, Key: fmt.Sprintf("k%d", i), Value: "v"})
	}
	if got := leader.Prepares(); got != 1 {
		t.Fatalf("lease fast path ran Phase 1: prepares=%d, want 1", got)
	}
	if got := leader.CommitIndex(); got != 10 {
		t.Fatalf("commitIndex=%d, want 10", got)
	}
}

func TestLeaseExpiryForcesRecampaign(t *testing.T) {
	c := NewCluster(3, func(int) Config { return Config{Lease: 40 * time.Millisecond} })
	leader := c.Node(1)
	mustCampaign(t, leader)
	mustPropose(t, leader, Op{Kind: OpSet, Key: "x", Value: "1"})
	time.Sleep(80 * time.Millisecond)
	if _, err := leader.Propose(Op{Kind: OpSet, Key: "y", Value: "2"}); err != ErrNotLeader {
		t.Fatalf("propose after lease expiry: err=%v, want ErrNotLeader", err)
	}
	mustCampaign(t, leader)
	mustPropose(t, leader, Op{Kind: OpSet, Key: "y", Value: "2"})
	if got := leader.CommitIndex(); got != 2 {
		t.Fatalf("commitIndex=%d, want 2", got)
	}
}

// TestPartitionReplacesOldLeader is the acceptance scenario: a 5-node
// cluster partitions 2|3, the old leader in the minority is replaced, the
// new leader completes uncommitted slots via Phase 1 and, after healing,
// safely overwrites the minority's dirty log.
func TestPartitionReplacesOldLeader(t *testing.T) {
	c := NewCluster(5, nil)
	oldLeader := c.Node(1)
	mustCampaign(t, oldLeader)
	mustPropose(t, oldLeader, Op{Kind: OpSet, Key: "stable", Value: "1"}) // slot 1, committed

	// Simulate an interrupted broadcast by the old leader at ballot {1,1}:
	//   slot 2 "kept"  -> accepted by 1,2,3 (reaches a majority member, uncommitted)
	//   slot 3 "dirty" -> accepted by 1,2 only (minority-only dirty value)
	b1 := Ballot{N: 1, Node: 1}
	inject := func(id, slot uint64, key string) {
		req := AcceptReq{Ballot: b1, Entry: LogEntry{Slot: slot, Ballot: b1,
			Op: Op{Kind: OpSet, Key: key, Value: "v"}}}
		if r := c.Node(int(id)).HandleAccept(req); !r.Ok {
			t.Fatalf("node %d rejected injected slot %d", id, slot)
		}
	}
	for _, id := range []uint64{1, 2, 3} {
		inject(id, 2, "kept")
	}
	for _, id := range []uint64{1, 2} {
		inject(id, 3, "dirty")
	}

	// Partition: {1,2} minority (with the old leader) | {3,4,5} majority.
	c.Hub.Partition([]int{1, 2}, []int{3, 4, 5})

	// The old leader cannot commit anything through the partition.
	if _, err := oldLeader.Propose(Op{Kind: OpSet, Key: "x", Value: "x"}); err != ErrNoMajority {
		t.Fatalf("minority leader propose: err=%v, want ErrNoMajority", err)
	}

	// The majority elects a new leader. Phase 1 must discover slot 2 on
	// node 3 and commit it (value survival); slot 3 is invisible to the
	// majority and stays open for the new leader's own proposals.
	newLeader := c.Node(4)
	mustCampaign(t, newLeader)
	if got := newLeader.CommitIndex(); got != 2 {
		t.Fatalf("after Phase 1 repair commitIndex=%d, want 2", got)
	}
	if e, _ := newLeader.Entry(2); e.Op.Key != "kept" {
		t.Fatalf("slot 2 = %+v, want surviving value kept", e)
	}
	for _, id := range []int{3, 4, 5} {
		if got := c.Node(id).CommitIndex(); got != 2 {
			t.Fatalf("majority node %d commitIndex=%d, want 2", id, got)
		}
	}

	// Under the lease the new leader commits straight away (no Phase 1).
	preparesBefore := newLeader.Prepares()
	mustPropose(t, newLeader, Op{Kind: OpSet, Key: "post", Value: "2"}) // takes slot 3
	if newLeader.Prepares() != preparesBefore {
		t.Fatal("leader ran Phase 1 despite a valid lease")
	}

	// Heal. The old leader's stale lease must not let it commit: the
	// majority promised a higher ballot and rejects its accepts.
	c.Hub.Heal()
	if _, err := oldLeader.Propose(Op{Kind: OpSet, Key: "stale", Value: "y"}); err == nil {
		t.Fatal("old leader committed after being replaced")
	}
	if oldLeader.IsLeader() {
		t.Fatal("old leader still believes it leads after rejection")
	}

	// The new leader syncs the minority: the dirty slot 3 (and the stale
	// slot-4 attempt on node 2) are overwritten by the chosen values.
	c.SyncAll(newLeader)

	for _, n := range c.Nodes {
		if got := n.CommitIndex(); got != 3 {
			t.Fatalf("node %d commitIndex=%d, want 3", n.ID(), got)
		}
		if e, _ := n.Entry(2); e.Op.Key != "kept" {
			t.Fatalf("node %d slot 2 = %+v, want kept", n.ID(), e)
		}
		if e, _ := n.Entry(3); e.Op.Key != "post" {
			t.Fatalf("node %d slot 3 = %+v, want post (dirty value overwritten)", n.ID(), e)
		}
		for _, gone := range []string{"dirty", "stale", "x"} {
			if _, ok := n.KV().Get(gone); ok {
				t.Fatalf("node %d applied uncommitted value %q", n.ID(), gone)
			}
		}
		if v, _ := n.KV().Get("stable"); v != "1" {
			t.Fatalf("node %d lost committed value stable", n.ID())
		}
		if v, _ := n.KV().Get("kept"); v != "v" {
			t.Fatalf("node %d missing Phase-1-repaired value kept", n.ID())
		}
		if v, _ := n.KV().Get("post"); v != "2" {
			t.Fatalf("node %d missing post-partition value", n.ID())
		}
	}
	if !c.Node(1).KV().Equal(c.Node(5).KV()) {
		t.Fatal("minority and majority state machines diverged after sync")
	}
}

// TestHoleRepair builds a sparse log by hand (slots 1 and 3 accepted, slot
// 2 missing, slot 3 known to only one node) and checks that a new leader's
// Phase 1 fills the hole with a noop and commits everything in order.
func TestHoleRepair(t *testing.T) {
	c := NewCluster(3, nil)
	b1 := Ballot{N: 1, Node: 1}
	entry := func(slot uint64, key string) LogEntry {
		return LogEntry{Slot: slot, Ballot: b1, Op: Op{Kind: OpSet, Key: key, Value: "v"}}
	}
	// Slot 1 on nodes 1,2; slot 3 only on node 1; slot 2 nowhere.
	for _, id := range []int{1, 2} {
		if r := c.Node(id).HandleAccept(AcceptReq{Ballot: b1, Entry: entry(1, "a")}); !r.Ok {
			t.Fatalf("node %d rejected slot 1", id)
		}
	}
	if r := c.Node(1).HandleAccept(AcceptReq{Ballot: b1, Entry: entry(3, "c")}); !r.Ok {
		t.Fatal("node 1 rejected slot 3")
	}

	leader := c.Node(2)
	mustCampaign(t, leader)
	c.SyncAll(leader)

	for _, n := range c.Nodes {
		if got := n.CommitIndex(); got != 3 {
			t.Fatalf("node %d commitIndex=%d, want 3", n.ID(), got)
		}
		if e, _ := n.Entry(2); e.Op.Kind != OpNoop {
			t.Fatalf("node %d slot 2 = %+v, want noop hole fill", n.ID(), e)
		}
		if v, _ := n.KV().Get("a"); v != "v" {
			t.Fatalf("node %d lost slot-1 value", n.ID())
		}
		if v, _ := n.KV().Get("c"); v != "v" {
			t.Fatalf("node %d lost slot-3 value (highest-ballot value must survive)", n.ID())
		}
	}
}

// TestConcurrentPropose hammers the leader from many goroutines; slots
// must be unique, contiguous and fully committed (run with -race).
func TestConcurrentPropose(t *testing.T) {
	c := NewCluster(5, nil)
	leader := c.Node(1)
	mustCampaign(t, leader)

	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	slots := make(chan uint64, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				slot, err := leader.Propose(Op{Kind: OpSet, Key: fmt.Sprintf("w%02d-%02d", w, i), Value: "v"})
				if err != nil {
					t.Errorf("propose: %v", err)
					return
				}
				slots <- slot
			}
		}(w)
	}
	wg.Wait()
	close(slots)

	seen := map[uint64]bool{}
	for s := range slots {
		if seen[s] {
			t.Fatalf("slot %d assigned twice", s)
		}
		seen[s] = true
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("got %d slots, want %d", len(seen), workers*perWorker)
	}
	for s := uint64(1); s <= uint64(workers*perWorker); s++ {
		if !seen[s] {
			t.Fatalf("slot %d missing: log has a hole", s)
		}
	}
	if got := leader.CommitIndex(); got != uint64(workers*perWorker) {
		t.Fatalf("commitIndex=%d, want %d", got, workers*perWorker)
	}
	for _, n := range c.Nodes {
		if got := n.KV().Applied(); got != uint64(workers*perWorker) {
			t.Fatalf("node %d applied=%d, want %d", n.ID(), got, workers*perWorker)
		}
	}
}

// TestSyncViaSnapshotTransfer: a follower isolated past the leader's
// checkpoint+compaction point catches up via snapshot install, not log
// replay.
func TestSyncViaSnapshotTransfer(t *testing.T) {
	dirs := make([]string, 3)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}
	cfg := func(id int) Config {
		return Config{Lease: defaultLease, IncrEvery: 2, ChkEvery: 4,
			SnapshotDir: dirs[id-1], Retain: 2}
	}
	c := NewCluster(3, cfg)
	leader := c.Node(1)
	mustCampaign(t, leader)

	// Isolate node 3, then commit 4 ops: the leader checkpoints at 4 and
	// compacts its whole log, so log replay to node 3 is impossible.
	c.Hub.Partition([]int{1, 2}, []int{3})
	for i := 1; i <= 4; i++ {
		mustPropose(t, leader, Op{Kind: OpSet, Key: fmt.Sprintf("k%d", i), Value: "v"})
	}
	if got := leader.LogLen(); got != 0 {
		t.Fatalf("leader log not compacted: %d slots retained", got)
	}

	c.Hub.Heal()
	c.SyncAll(leader)

	laggard := c.Node(3)
	if got := laggard.CommitIndex(); got != 4 {
		t.Fatalf("laggard commitIndex=%d, want 4 (snapshot transfer)", got)
	}
	if !laggard.KV().Equal(leader.KV()) {
		t.Fatal("laggard state diverges after snapshot transfer")
	}
	// And the cluster keeps making progress on top of the snapshot.
	mustPropose(t, leader, Op{Kind: OpSet, Key: "k5", Value: "v"})
	if got := laggard.CommitIndex(); got != 5 {
		t.Fatalf("laggard commitIndex=%d, want 5", got)
	}
}

// TestNoGoroutineLeak: the library spawns no background goroutines, so
// building, running and abandoning a cluster must not leak any.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		c := NewCluster(5, nil)
		leader := c.Node(1)
		mustCampaign(t, leader)
		for j := 0; j < 20; j++ {
			mustPropose(t, leader, Op{Kind: OpSet, Key: "k", Value: "v"})
		}
	}
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
