package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func valuesOf(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Noop {
			out = append(out, "<noop>")
		} else {
			out = append(out, string(e.Value))
		}
	}
	return out
}

func assertAppliedEqual(t *testing.T, c *Cluster, want []string) {
	t.Helper()
	for _, n := range c.Nodes {
		got := valuesOf(n.Applied())
		if len(got) != len(want) {
			t.Fatalf("node %d applied=%v, want %v", n.ID(), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("node %d applied=%v, want %v", n.ID(), got, want)
			}
		}
	}
}

// TestElectionAndLeaseFastPath: a leader with a valid lease commits
// consecutive entries without re-running Phase 1.
func TestElectionAndLeaseFastPath(t *testing.T) {
	c := NewCluster(5)
	defer c.Shutdown()
	for _, n := range c.Nodes {
		n.SetLeaseDuration(10 * time.Second)
	}
	if err := c.Nodes[0].Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if got := c.Leader().ID(); got != 0 {
		t.Fatalf("leader=%d, want 0", got)
	}
	preparesBefore := c.Nodes[0].Metrics().Counter(CPrepareSent)
	for i := 0; i < 4; i++ {
		if _, err := c.Nodes[0].Propose([]byte(fmt.Sprintf("cmd-%d", i))); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	// Lease fast path: no additional Phase-1 traffic.
	if got := c.Nodes[0].Metrics().Counter(CPrepareSent); got != preparesBefore {
		t.Fatalf("phase 1 re-ran under valid lease: prepare.sent %d -> %d", preparesBefore, got)
	}
	if r := c.Nodes[0].Metrics().HitRate(RateLease); r != 1.0 {
		t.Fatalf("lease hit rate = %v, want 1.0", r)
	}
	assertAppliedEqual(t, c, []string{"cmd-0", "cmd-1", "cmd-2", "cmd-3"})
	for _, n := range c.Nodes {
		if n.Status().CommitIndex != 4 {
			t.Fatalf("node %d commitIndex=%d, want 4", n.ID(), n.Status().CommitIndex)
		}
	}
}

// TestLeaseExpiryReentersPhase1: an expired lease forces Phase 1 again.
func TestLeaseExpiryReentersPhase1(t *testing.T) {
	c := NewCluster(3)
	defer c.Shutdown()
	for _, n := range c.Nodes {
		n.SetLeaseDuration(30 * time.Millisecond)
	}
	if _, err := c.Nodes[0].Propose([]byte("a")); err != nil { // miss: campaigns
		t.Fatalf("propose a: %v", err)
	}
	if _, err := c.Nodes[0].Propose([]byte("a2")); err != nil { // hit: lease valid
		t.Fatalf("propose a2: %v", err)
	}
	if got := c.Nodes[0].Metrics().Counter(CCampaignOk); got != 1 {
		t.Fatalf("campaigns=%d, want 1", got)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := c.Nodes[0].Propose([]byte("b")); err != nil { // miss: lease expired
		t.Fatalf("propose b: %v", err)
	}
	if got := c.Nodes[0].Metrics().Counter(CCampaignOk); got != 2 {
		t.Fatalf("campaigns=%d, want 2 after lease expiry", got)
	}
	m := c.Nodes[0].Metrics()
	if r := m.HitRate(RateLease); r != 1.0/3.0 {
		t.Fatalf("lease hit rate=%v, want 1/3 (1 hit 2 misses)", r)
	}
	assertAppliedEqual(t, c, []string{"a", "a2", "b"})
}

// TestPartitionLeaderReplacement is the acceptance scenario: a 5-node
// cluster is partitioned; the old leader is cut off and replaced by a
// majority-elected new leader, which fills uncommitted slots via Phase 1
// (including a noop hole) and safely overwrites the minority's dirty log.
func TestPartitionLeaderReplacement(t *testing.T) {
	c := NewCluster(5)
	defer c.Shutdown()
	for _, n := range c.Nodes {
		n.SetLeaseDuration(10 * time.Second)
	}

	// Stable leader commits slots 1-3 on all nodes.
	if err := c.Nodes[0].Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := c.Nodes[0].Propose([]byte(fmt.Sprintf("cmd-%d", i))); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	assertAppliedEqual(t, c, []string{"cmd-0", "cmd-1", "cmd-2"})

	// Old leader fully isolated: its slot-4 accept reaches only itself
	// (dirty value invisible to the future quorum).
	c.Net.Isolate(0)
	if _, err := c.Nodes[0].Propose([]byte("dirty-4")); err == nil {
		t.Fatal("isolated leader must not commit")
	}

	// Old leader reachable only by node 3: slot 5 accepted on {0,3}.
	c.Net.Heal()
	c.Net.Partition([]int{0}, []int{1, 2, 4})
	if _, err := c.Nodes[0].Propose([]byte("v5")); err == nil {
		t.Fatal("minority accept must not commit")
	}

	// Old leader cut off for good; the majority replaces it.
	c.Net.Isolate(0)
	if err := c.Nodes[1].Campaign(); err != nil {
		t.Fatalf("new leader campaign: %v", err)
	}
	if c.Nodes[0].Status().IsLeader && !c.Nodes[1].Status().IsLeader {
		t.Fatal("expected leadership to move to majority")
	}
	// Phase-1 repair: slot 4 is a hole (noop), slot 5 adopts v5 from node 3.
	m1 := c.Nodes[1].Metrics()
	if got := m1.Counter(CRepairNoop); got != 1 {
		t.Fatalf("repair noop fills=%d, want 1 (hole at slot 4)", got)
	}
	if got := m1.Counter(CRepairAdopt); got != 1 {
		t.Fatalf("repair adopts=%d, want 1 (v5 at slot 5)", got)
	}
	if _, err := c.Nodes[1].Propose([]byte("cmd-6")); err != nil {
		t.Fatalf("new leader propose: %v", err)
	}

	// The isolated old leader still cannot commit anything.
	if _, err := c.Nodes[0].Propose([]byte("dirty-6")); err == nil {
		t.Fatal("old leader must not commit while isolated")
	}

	// Heal. The old leader's stale accepts are rejected by the majority and
	// it steps down; commit broadcasts + fetch converge its log.
	c.Net.Heal()
	if _, err := c.Nodes[0].Propose([]byte("late")); err == nil {
		t.Fatal("old leader propose after heal must fail against higher ballot")
	}
	if c.Nodes[0].Status().IsLeader {
		t.Fatal("old leader must step down after seeing higher ballot")
	}
	if _, err := c.Nodes[1].Propose([]byte("cmd-7")); err != nil {
		t.Fatalf("post-heal propose: %v", err)
	}

	want := []string{"cmd-0", "cmd-1", "cmd-2", "<noop>", "v5", "cmd-6", "cmd-7"}
	assertAppliedEqual(t, c, want)

	// Minority dirty logs (dirty-4 at slot 4, dirty-6 at slot 6) were
	// safely overwritten by committed values.
	if got := c.Nodes[0].Metrics().Counter(CDirtyOverwrite); got < 2 {
		t.Fatalf("node 0 dirty overwrites=%d, want >= 2", got)
	}
	// Safety: no committed value was ever rewritten anywhere.
	for _, n := range c.Nodes {
		if got := n.Metrics().Errors()[ErrConflictCommit]; got != 0 {
			t.Fatalf("node %d saw %d conflicting commits", n.ID(), got)
		}
		if got := n.Metrics().Errors()[ErrStaleCommit]; got != 0 {
			t.Fatalf("node %d saw %d stale-commit accepts", n.ID(), got)
		}
	}
	// Error codes were tallied on the failing paths.
	if got := c.Nodes[0].Metrics().Errors()[ErrNoQuorum]; got == 0 {
		t.Fatal("old leader should record NO_QUORUM errors")
	}
	if got := c.Nodes[0].Metrics().Counter(CStepDown); got == 0 {
		t.Fatal("old leader should record step-down")
	}
}

// TestCommittedValueSurvivesLeaderChange: a value committed by the old
// leader right before the partition must be preserved by the new leader.
func TestCommittedValueSurvivesLeaderChange(t *testing.T) {
	c := NewCluster(5)
	defer c.Shutdown()
	for _, n := range c.Nodes {
		n.SetLeaseDuration(10 * time.Second)
	}
	if err := c.Nodes[0].Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if _, err := c.Nodes[0].Propose([]byte("stable")); err != nil {
		t.Fatalf("propose: %v", err)
	}
	c.Net.Isolate(0)
	if err := c.Nodes[2].Campaign(); err != nil {
		t.Fatalf("new campaign: %v", err)
	}
	if _, err := c.Nodes[2].Propose([]byte("after")); err != nil {
		t.Fatalf("new leader propose: %v", err)
	}
	c.Net.Heal()
	if _, err := c.Nodes[2].Propose([]byte("after-2")); err != nil {
		t.Fatalf("post-heal propose: %v", err)
	}
	assertAppliedEqual(t, c, []string{"stable", "after", "after-2"})
}

// TestConcurrentPropose hammers the leader and readers concurrently; the
// committed log must stay consistent everywhere (run with -race).
func TestConcurrentPropose(t *testing.T) {
	c := NewCluster(5)
	defer c.Shutdown()
	for _, n := range c.Nodes {
		n.SetLeaseDuration(10 * time.Second)
	}
	if err := c.Nodes[0].Campaign(); err != nil {
		t.Fatalf("campaign: %v", err)
	}
	const workers = 8
	const perWorker = 10
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := c.Nodes[0].Propose([]byte(fmt.Sprintf("w%d-%d", w, i))); err != nil {
					t.Errorf("propose: %v", err)
					return
				}
			}
		}(w)
	}
	// Concurrent readers.
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for _, n := range c.Nodes {
		rwg.Add(1)
		go func(n *Node) {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = n.Status()
					_ = n.Applied()
					_ = n.Metrics().Snapshot(n.ID())
				}
			}
		}(n)
	}
	wg.Wait()
	close(stop)
	rwg.Wait()

	if got := c.Nodes[0].Status().CommitIndex; got != workers*perWorker {
		t.Fatalf("commitIndex=%d, want %d", got, workers*perWorker)
	}
	// All followers converge to the leader's log after the final broadcast.
	want := valuesOf(c.Nodes[0].Applied())
	assertAppliedEqual(t, c, want)
	for _, n := range c.Nodes {
		if got := n.Metrics().Errors()[ErrConflictCommit]; got != 0 {
			t.Fatalf("node %d saw conflicting commits", n.ID())
		}
	}
}

// TestDuelingCampaigns: concurrent campaigns never produce conflicting
// committed values.
func TestDuelingCampaigns(t *testing.T) {
	c := NewCluster(5)
	defer c.Shutdown()
	for _, n := range c.Nodes {
		n.SetLeaseDuration(10 * time.Second)
	}
	var wg sync.WaitGroup
	for _, n := range c.Nodes {
		wg.Add(1)
		go func(n *Node) {
			defer wg.Done()
			_ = n.Campaign()
		}(n)
	}
	wg.Wait()
	leaders := 0
	for _, n := range c.Nodes {
		if n.Status().IsLeader {
			leaders++
		}
	}
	if leaders != 1 {
		t.Fatalf("leaders=%d, want exactly 1", leaders)
	}
	l := c.Leader()
	if _, err := l.Propose([]byte("x")); err != nil {
		t.Fatalf("propose: %v", err)
	}
	assertAppliedEqual(t, c, []string{"x"})
}

// TestNoGoroutineLeak: the engine spawns no background goroutines, so
// shutdown leaves the goroutine count unchanged.
func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	for round := 0; round < 3; round++ {
		c := NewCluster(5)
		if err := c.Nodes[0].Campaign(); err != nil {
			t.Fatalf("campaign: %v", err)
		}
		for i := 0; i < 5; i++ {
			if _, err := c.Nodes[0].Propose([]byte("v")); err != nil {
				t.Fatalf("propose: %v", err)
			}
		}
		c.Net.Isolate(0)
		_ = c.Nodes[1].Campaign()
		c.Net.Heal()
		_, _ = c.Nodes[1].Propose([]byte("w"))
		c.Shutdown()
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: base=%d now=%d", base, runtime.NumGoroutine())
}
