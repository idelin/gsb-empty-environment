package paxos

import (
	"runtime"
	"testing"
	"time"
)

func newCluster(n int) (*Network, []*Node) {
	net := NewNetwork()
	nodes := make([]*Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = NewNode(i, net, nil)
	}
	return net, nodes
}

func committedLog(t *testing.T, n *Node, upto uint64) []string {
	t.Helper()
	out := make([]string, 0, upto)
	for slot := uint64(1); slot <= upto; slot++ {
		e, ok := n.EntryAt(slot)
		if !ok {
			t.Fatalf("node %d: hole at slot %d", n.ID(), slot)
		}
		if !e.Committed {
			t.Fatalf("node %d: slot %d not committed", n.ID(), slot)
		}
		out = append(out, e.Value)
	}
	return out
}

// TestPartitionRecovery is the acceptance scenario: a 5-node cluster splits
// {0,1}|{2,3,4}; the old leader (0) is deposed by a new majority leader (2),
// which repairs uncommitted slots via Phase 1 and safely overwrites the
// minority's dirty log after healing.
func TestPartitionRecovery(t *testing.T) {
	net, nodes := newCluster(5)

	if !nodes[0].Campaign() {
		t.Fatal("node 0 failed to campaign")
	}
	for _, v := range []string{"v1", "v2", "v3"} {
		if _, err := nodes[0].Propose(v); err != nil {
			t.Fatalf("propose %q: %v", v, err)
		}
	}
	for _, nd := range nodes {
		if got := nd.CommitIndex(); got != 3 {
			t.Fatalf("node %d commitIndex=%d, want 3", nd.ID(), got)
		}
	}

	// Net-split: old leader stuck in the minority partition.
	net.Partition([]int{0, 1}, []int{2, 3, 4})

	// Old leader still holds its lease and accepts the write locally, but can
	// never reach a majority: slot 4 stays dirty on {0,1} only.
	if _, err := nodes[0].Propose("dirty"); err != ErrNoMajority {
		t.Fatalf("minority leader propose err=%v, want ErrNoMajority", err)
	}
	if e, ok := nodes[1].EntryAt(4); !ok || e.Committed || e.Value != "dirty" {
		t.Fatalf("node 1 slot 4 = %+v ok=%v, want dirty uncommitted", e, ok)
	}

	// Majority side elects a new leader. Phase 1 must find no committed tail
	// beyond slot 3 on its side and must not resurrect "dirty".
	if !nodes[2].Campaign() {
		t.Fatal("node 2 failed to campaign in majority partition")
	}
	if !nodes[2].LeaseValid() {
		t.Fatal("node 2 lease invalid right after campaign")
	}
	if _, err := nodes[2].Propose("v4"); err != nil {
		t.Fatalf("new leader propose v4: %v", err)
	}
	if _, err := nodes[2].Propose("v5"); err != nil {
		t.Fatalf("new leader propose v5: %v", err)
	}
	for _, id := range []int{2, 3, 4} {
		if got := nodes[id].CommitIndex(); got != 5 {
			t.Fatalf("node %d commitIndex=%d, want 5", id, got)
		}
	}

	// Heal: heartbeats depose the old leader and sync the minority.
	net.Heal()
	nodes[2].SendHeartbeats()

	if nodes[0].IsLeader() {
		t.Fatal("old leader still believes it is leader after heal")
	}
	if _, err := nodes[0].Propose("stale"); err != ErrNotLeader {
		t.Fatalf("deposed leader propose err=%v, want ErrNotLeader", err)
	}

	want := []string{"v1", "v2", "v3", "v4", "v5"}
	for _, nd := range nodes {
		if got := nd.CommitIndex(); got != 5 {
			t.Fatalf("node %d commitIndex=%d, want 5", nd.ID(), got)
		}
		got := committedLog(t, nd, 5)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("node %d slot %d = %q, want %q (dirty overwrite failed)", nd.ID(), i+1, got[i], want[i])
			}
		}
	}
}

// TestHoleRepair drives a hole into the tail, then forces a new leader to
// plug it with a noop during Phase 1 reconstruction.
func TestHoleRepair(t *testing.T) {
	_, nodes := newCluster(5)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	if _, err := nodes[0].Propose("a"); err != nil {
		t.Fatal(err)
	}

	// Inject an accepted-but-uncommitted entry at slot 3 on a majority,
	// leaving slot 2 empty (the hole).
	hole := Entry{Slot: 3, Ballot: Ballot{N: 1, ID: 0}, Value: "orphan"}
	for _, id := range []int{0, 1, 2} {
		if resp := nodes[id].HandleAccept(AcceptRequest{Entry: hole}); !resp.OK {
			t.Fatalf("node %d rejected inject", id)
		}
	}

	// New leader: reconstruction must commit slot 2 as noop and slot 3 as
	// "orphan" (highest-ballot accepted value wins).
	if !nodes[3].Campaign() {
		t.Fatal("node 3 campaign failed")
	}
	for _, nd := range nodes {
		if got := nd.CommitIndex(); got != 3 {
			t.Fatalf("node %d commitIndex=%d, want 3", nd.ID(), got)
		}
		log := committedLog(t, nd, 3)
		if log[0] != "a" || log[1] != NoopValue || log[2] != "orphan" {
			t.Fatalf("node %d log=%q, want [a noop orphan]", nd.ID(), log)
		}
	}
}

// TestLeaseFastPath verifies that a leader with a valid lease commits
// consecutive entries without re-running Phase 1 (ballot stays constant).
func TestLeaseFastPath(t *testing.T) {
	_, nodes := newCluster(3)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	var firstBallot Ballot
	for i := 0; i < 5; i++ {
		slot, err := nodes[0].Propose("x")
		if err != nil {
			t.Fatal(err)
		}
		if slot != uint64(i+1) {
			t.Fatalf("slot=%d, want %d (dense allocation)", slot, i+1)
		}
		e, _ := nodes[0].EntryAt(slot)
		if i == 0 {
			firstBallot = e.Ballot
		} else if e.Ballot != firstBallot {
			t.Fatalf("slot %d ballot changed to %v; Phase 1 re-ran under a valid lease", slot, e.Ballot)
		}
	}
}

// TestAcceptorSafety covers the acceptor invariants: promises are monotone,
// committed entries are immutable, and stale ballots cannot overwrite.
func TestAcceptorSafety(t *testing.T) {
	_, nodes := newCluster(3)
	nd := nodes[0]

	if resp := nd.HandlePrepare(PrepareRequest{Ballot: Ballot{N: 5, ID: 1}}); !resp.OK {
		t.Fatal("first prepare rejected")
	}
	if resp := nd.HandlePrepare(PrepareRequest{Ballot: Ballot{N: 4, ID: 4}}); resp.OK {
		t.Fatal("lower ballot prepare accepted")
	}

	dirty := Entry{Slot: 1, Ballot: Ballot{N: 5, ID: 1}, Value: "dirty"}
	if resp := nd.HandleAccept(AcceptRequest{Entry: dirty}); !resp.OK {
		t.Fatal("accept at promised ballot rejected")
	}
	stale := Entry{Slot: 1, Ballot: Ballot{N: 4, ID: 4}, Value: "stale"}
	if resp := nd.HandleAccept(AcceptRequest{Entry: stale}); resp.OK {
		t.Fatal("stale ballot accept allowed")
	}

	nd.HandleCommit(CommitRequest{Entry: Entry{Slot: 1, Ballot: Ballot{N: 5, ID: 1}, Value: "final"}})
	// Even a higher ballot must not mutate a committed slot.
	higher := Entry{Slot: 1, Ballot: Ballot{N: 9, ID: 2}, Value: "evil"}
	if resp := nd.HandleAccept(AcceptRequest{Entry: higher}); !resp.OK {
		t.Fatal("higher-ballot accept should be acked")
	}
	e, _ := nd.EntryAt(1)
	if e.Value != "final" || !e.Committed {
		t.Fatalf("committed entry mutated: %+v", e)
	}
	if got := nd.CommitIndex(); got != 1 {
		t.Fatalf("commitIndex=%d, want 1", got)
	}
}

// TestMinorityCannotCampaign ensures a partitioned minority never elects.
func TestMinorityCannotCampaign(t *testing.T) {
	net, nodes := newCluster(5)
	net.Partition([]int{0, 1}, []int{2, 3, 4})
	if nodes[0].Campaign() {
		t.Fatal("minority partition elected a leader")
	}
	if !nodes[2].Campaign() {
		t.Fatal("majority partition failed to elect")
	}
}

// TestConcurrentProposes hammers the leader from many goroutines: slots must
// be dense, committed once, and identical on every replica.
func TestConcurrentProposes(t *testing.T) {
	_, nodes := newCluster(5)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	const writers = 8
	const perWriter = 25
	done := make(chan struct{})
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < perWriter; i++ {
				if _, err := nodes[0].Propose("w"); err != nil {
					t.Errorf("propose: %v", err)
					return
				}
			}
		}(w)
	}
	for w := 0; w < writers; w++ {
		<-done
	}
	const total = writers * perWriter
	for _, nd := range nodes {
		if got := nd.CommitIndex(); got != total {
			t.Fatalf("node %d commitIndex=%d, want %d", nd.ID(), got, total)
		}
		committedLog(t, nd, total)
	}
}

// TestNoGoroutineLeak asserts that heartbeat loops and tuners shut down
// cleanly: goroutine count returns to baseline after Stop.
func TestNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()

	_, nodes := newCluster(5)
	ctls := make([]*Controller, 0, len(nodes))
	for _, nd := range nodes {
		nd.StartHeartbeatLoop(5 * time.Millisecond)
		ctls = append(ctls, nd.ctl)
		nd.ctl.StartTuner(5 * time.Millisecond)
	}
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	time.Sleep(50 * time.Millisecond) // let loops actually run

	for _, nd := range nodes {
		nd.Stop()
	}
	for _, c := range ctls {
		c.Stop()
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline=%d now=%d", baseline, runtime.NumGoroutine())
}
