package multipaxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func newCluster(size int, lease time.Duration) (*Network, []*Node) {
	net := NewNetwork()
	nodes := make([]*Node, size)
	for i := 0; i < size; i++ {
		nodes[i] = NewNode(i+1, net, lease)
	}
	return net, nodes
}

func snapshots(nodes []*Node) []Snapshot {
	out := make([]Snapshot, len(nodes))
	for i, n := range nodes {
		out[i] = n.Snapshot()
	}
	return out
}

func assertClean(t *testing.T, nodes []*Node) {
	t.Helper()
	vs := NewInspector().Inspect(snapshots(nodes))
	if len(vs) != 0 {
		for _, v := range vs {
			t.Logf("violation: %s", v)
		}
		t.Fatalf("inspector reported %d violations", len(vs))
	}
}

func TestBasicConsensus(t *testing.T) {
	_, nodes := newCluster(3, time.Second)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	slot, ok := nodes[0].Propose("alpha")
	if !ok || slot != 1 {
		t.Fatalf("propose failed: slot=%d ok=%v", slot, ok)
	}
	for _, n := range nodes {
		v, committed, exists := n.Value(1)
		if !exists || !committed || v != "alpha" {
			t.Fatalf("node %d: value=%q committed=%v exists=%v", n.ID(), v, committed, exists)
		}
		if n.CommitIndex() != 1 {
			t.Fatalf("node %d commitIndex=%d", n.ID(), n.CommitIndex())
		}
	}
	assertClean(t, nodes)
}

func TestLeaseSkipsPhase1(t *testing.T) {
	net, nodes := newCluster(5, 200*time.Millisecond)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	net.ResetStats()
	for i := 0; i < 3; i++ {
		if _, ok := nodes[0].Propose(fmt.Sprintf("v%d", i)); !ok {
			t.Fatalf("propose %d failed under valid lease", i)
		}
	}
	prepares, accepts, _ := net.Stats()
	if prepares != 0 {
		t.Fatalf("lease should skip Phase 1, got %d prepares", prepares)
	}
	if accepts != 15 { // 3 proposals x 5 nodes
		t.Fatalf("expected 15 accepts, got %d", accepts)
	}
	// Lease expiry forces the leader to stop committing.
	time.Sleep(250 * time.Millisecond)
	if _, ok := nodes[0].Propose("late"); ok {
		t.Fatal("propose after lease expiry should fail")
	}
	assertClean(t, nodes)
}

func TestMinorityCannotCampaign(t *testing.T) {
	net, nodes := newCluster(5, time.Second)
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if nodes[0].Campaign() {
		t.Fatal("minority partition must not win a campaign")
	}
	if nodes[2].Campaign() {
		// majority side wins
	} else {
		t.Fatal("majority partition should win a campaign")
	}
	net.Heal()
	assertClean(t, nodes)
}

// TestPartitionAcceptance is the acceptance boundary: a 5-node cluster is
// partitioned; the old leader is cut off and replaced by a majority-elected
// leader, which fills uncommitted slots via Phase 1 and safely overwrites the
// minority's dirty log.
func TestPartitionAcceptance(t *testing.T) {
	net, nodes := newCluster(5, 500*time.Millisecond)
	oldLeader, newLeader := nodes[0], nodes[2]

	if !oldLeader.Campaign() {
		t.Fatal("initial campaign failed")
	}
	if slot, ok := oldLeader.Propose("A"); !ok || slot != 1 {
		t.Fatalf("slot 1 propose failed: slot=%d ok=%v", slot, ok)
	}

	// Partition {1,2} | {3,4,5}: the old leader's write lands only on the
	// minority as an uncommitted dirty entry.
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if _, ok := oldLeader.Propose("dirty"); ok {
		t.Fatal("isolated leader must not commit through a minority")
	}
	for _, id := range []int{1, 2} {
		v, committed, exists := nodes[id-1].Value(2)
		if !exists || committed || v != "dirty" {
			t.Fatalf("node %d: expected uncommitted dirty entry, got %q committed=%v", id, v, committed)
		}
	}

	// Heal and let the old leader commit slot 3 cluster-wide; nodes 3-5 now
	// have a hole at slot 2 (they never saw the dirty entry).
	net.Heal()
	if slot, ok := oldLeader.Propose("C"); !ok || slot != 3 {
		t.Fatalf("slot 3 propose failed: slot=%d ok=%v", slot, ok)
	}
	if nodes[3].CommitIndex() != 1 {
		t.Fatalf("node 4 commitIndex=%d, want 1 (hole at 2)", nodes[3].CommitIndex())
	}

	// Partition again. The majority elects a new leader whose Phase 1 sees
	// slots {1,3} but nothing at slot 2: the hole is filled with a no-op.
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if !newLeader.Campaign() {
		t.Fatal("majority campaign failed")
	}
	for _, id := range []int{3, 4, 5} {
		v, committed, _ := nodes[id-1].Value(2)
		if !committed || v != NoOp {
			t.Fatalf("node %d: slot 2 = %q committed=%v, want committed no-op", id, v, committed)
		}
	}

	// Heal: the next commit broadcast carries the full committed prefix and
	// safely overwrites the minority's dirty slot 2.
	net.Heal()
	if slot, ok := newLeader.Propose("D"); !ok || slot != 4 {
		t.Fatalf("post-heal propose failed: slot=%d ok=%v", slot, ok)
	}
	for _, n := range nodes {
		if n.CommitIndex() != 4 {
			t.Fatalf("node %d commitIndex=%d, want 4", n.ID(), n.CommitIndex())
		}
		v, committed, _ := n.Value(2)
		if !committed || v != NoOp {
			t.Fatalf("node %d: dirty slot not overwritten: %q committed=%v", n.ID(), v, committed)
		}
	}

	// The old leader has stepped down and cannot commit anymore.
	if oldLeader.IsLeader() {
		t.Fatal("old leader should have stepped down")
	}
	if _, ok := oldLeader.Propose("evil"); ok {
		t.Fatal("deposed leader must not commit")
	}
	assertClean(t, nodes)
}

// TestHoleRepairPreservesChosenValue: a value accepted by a minority that IS
// visible to the new leader's Phase 1 must be re-proposed (it may have been
// chosen), while genuinely unseen slots become no-ops.
func TestHoleRepair(t *testing.T) {
	net, nodes := newCluster(5, time.Second)
	leader := nodes[0]
	if !leader.Campaign() {
		t.Fatal("campaign failed")
	}
	if _, ok := leader.Propose("s1"); !ok {
		t.Fatal("slot 1 failed")
	}

	// Slot 2 accepted only by {1,2} (uncommitted); slot 3 then commits
	// cluster-wide, leaving a hole at slot 2 on nodes 3-5.
	net.Partition([]int{1, 2}, []int{3, 4, 5})
	if _, ok := leader.Propose("dirty-2"); ok {
		t.Fatal("minority write must not commit")
	}
	net.Heal()
	if slot, ok := leader.Propose("s3"); !ok || slot != 3 {
		t.Fatalf("slot 3 failed: slot=%d ok=%v", slot, ok)
	}
	if nodes[3].CommitIndex() != 1 {
		t.Fatalf("node 4 commitIndex=%d, want 1 (hole at 2)", nodes[3].CommitIndex())
	}

	// A new campaign repairs the hole. Phase 1 sees dirty-2 on nodes 1,2 so
	// the value is preserved and committed everywhere.
	if !nodes[2].Campaign() {
		t.Fatal("repair campaign failed")
	}
	for _, n := range nodes {
		v, committed, _ := n.Value(2)
		if !committed || v != "dirty-2" {
			t.Fatalf("node %d: slot 2 = %q committed=%v, want committed dirty-2", n.ID(), v, committed)
		}
		if n.CommitIndex() != 3 {
			t.Fatalf("node %d commitIndex=%d, want 3", n.ID(), n.CommitIndex())
		}
	}
	assertClean(t, nodes)
}

func TestCommittedEntriesAreImmutable(t *testing.T) {
	_, nodes := newCluster(3, time.Second)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	if _, ok := nodes[0].Propose("chosen"); !ok {
		t.Fatal("propose failed")
	}
	// A hostile/buggy accept with a different value for a committed slot
	// must be rejected even with a higher ballot.
	resp := nodes[1].handleAccept(AcceptReq{Entry: Entry{Slot: 1, Value: "forgery", Ballot: Ballot{N: 99, Node: 3}}})
	if resp.Accepted {
		t.Fatal("accept overwrote a committed slot")
	}
	v, committed, _ := nodes[1].Value(1)
	if v != "chosen" || !committed {
		t.Fatalf("committed entry mutated: %q committed=%v", v, committed)
	}
	assertClean(t, nodes)
}

func TestConcurrentProposeAndInspect(t *testing.T) {
	_, nodes := newCluster(5, 10*time.Second)
	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, ok := nodes[0].Propose(fmt.Sprintf("g%d-v%d", g, i)); !ok {
					t.Errorf("worker %d propose %d failed", g, i)
					return
				}
			}
		}(g)
	}
	// Concurrent offline inspection must be race-free.
	stop := make(chan struct{})
	var inspectorWg sync.WaitGroup
	inspectorWg.Add(1)
	go func() {
		defer inspectorWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				NewInspector().Inspect(snapshots(nodes))
			}
		}
	}()
	wg.Wait()
	close(stop)
	inspectorWg.Wait()

	want := workers * perWorker
	for _, n := range nodes {
		if n.CommitIndex() != want {
			t.Fatalf("node %d commitIndex=%d, want %d", n.ID(), n.CommitIndex(), want)
		}
	}
	assertClean(t, nodes)
}

func goroutineCount() int {
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	return runtime.NumGoroutine()
}

func TestNoGoroutineLeak(t *testing.T) {
	before := goroutineCount()
	func() {
		net, nodes := newCluster(5, time.Second)
		nodes[0].Campaign()
		for i := 0; i < 10; i++ {
			nodes[0].Propose(fmt.Sprintf("v%d", i))
		}
		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				NewInspector().Inspect(snapshots(nodes))
			}()
		}
		wg.Wait()
		net.Close()
	}()
	after := goroutineCount()
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
