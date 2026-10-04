package paxos

import (
	"bytes"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

const testLease = 150 * time.Millisecond

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

func newTestCluster(t *testing.T, n int, withStores bool) *Cluster {
	t.Helper()
	return newTestClusterLease(t, n, withStores, testLease)
}

func newTestClusterLease(t *testing.T, n int, withStores bool, lease time.Duration) *Cluster {
	t.Helper()
	var paths []string
	if withStores {
		dir := t.TempDir()
		for i := 0; i < n; i++ {
			paths = append(paths, filepath.Join(dir, fmt.Sprintf("node%d.log", i)))
		}
	}
	c, err := NewCluster(n, lease, paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func elect(t *testing.T, c *Cluster, id int) {
	t.Helper()
	waitFor(t, 3*time.Second, func() bool {
		if c.Node(id).IsLeader() {
			return true
		}
		c.Node(id).Campaign()
		return false
	}, fmt.Sprintf("node %d to become leader", id))
}

func appliedCmds(n *Node) [][]byte {
	var out [][]byte
	for _, e := range n.Applied() {
		out = append(out, e.Cmd)
	}
	return out
}

func assertSameLog(t *testing.T, c *Cluster) [][]byte {
	t.Helper()
	want := appliedCmds(c.Node(0))
	for i := 1; i < c.Size(); i++ {
		got := appliedCmds(c.Node(i))
		if len(got) != len(want) {
			t.Fatalf("node %d applied %d entries, want %d", i, len(got), len(want))
		}
		for j := range want {
			if !bytes.Equal(got[j], want[j]) {
				t.Fatalf("node %d slot %d = %q, want %q", i, j+1, got[j], want[j])
			}
		}
	}
	return want
}

func TestBasicElectionAndCommit(t *testing.T) {
	c := newTestCluster(t, 5, false)
	elect(t, c, 0)
	for i := 0; i < 3; i++ {
		if _, err := c.Node(0).Propose([]byte(fmt.Sprintf("cmd-%d", i+1))); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 3*time.Second, func() bool { return c.Node(4).CommitIndex() == 3 },
		"all entries committed on node 4")
	log := assertSameLog(t, c)
	if len(log) != 3 || string(log[0]) != "cmd-1" || string(log[2]) != "cmd-3" {
		t.Fatalf("unexpected log: %q", log)
	}
}

func TestLeaseBypassesPhase1(t *testing.T) {
	c := newTestCluster(t, 5, false)
	elect(t, c, 0)
	c.ResetCounts()
	for i := 0; i < 5; i++ {
		if _, err := c.Node(0).Propose([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 3*time.Second, func() bool { return c.Node(0).CommitIndex() == 5 }, "commits")
	counts := c.Counts()
	if counts[MsgPrepare] != 0 || counts[MsgPromise] != 0 {
		t.Fatalf("Phase 1 messages under valid lease: %v", counts)
	}
	if counts[MsgAccept] == 0 {
		t.Fatalf("expected Phase 2 traffic, got %v", counts)
	}
}

// TestPartitionRecovery is the acceptance scenario:
//  1. node0 leads and commits slots 1-3 on all 5 nodes.
//  2. "kept-4" is accepted by a majority but the commit is lost (replies
//     blocked), so it stays uncommitted with its value on the majority side.
//  3. A partition isolates {0,1} from {2,3,4}; the old leader's "dirty-5"
//     only reaches the minority (dirty, uncommitted).
//  4. After the lease expires, node2 wins Phase 1 in the majority, completes
//     slot 4 with its original value and commits a fresh slot 5.
//  5. Healing lets the new leader overwrite the minority's dirty slot 5;
//     every node converges to the same gap-free log.
func TestPartitionRecovery(t *testing.T) {
	// Longer lease gives a comfortable window for the negative assertion
	// below even on slow (race-enabled) machines.
	c := newTestClusterLease(t, 5, true, 600*time.Millisecond)
	elect(t, c, 0)
	for i := 1; i <= 3; i++ {
		if _, err := c.Node(0).Propose([]byte(fmt.Sprintf("cmd-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 3*time.Second, func() bool { return c.Node(4).CommitIndex() == 3 },
		"slots 1-3 committed everywhere")

	// "kept-4": accepted by everyone, but the Accepted replies from {2,3,4}
	// never reach the leader, so it cannot commit.
	for _, f := range []int{2, 3, 4} {
		c.Block(f, 0)
	}
	if _, err := c.Node(0).Propose([]byte("kept-4")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		e, ok := c.Node(2).LogEntry(4)
		return ok && string(e.Cmd) == "kept-4"
	}, "kept-4 accepted on majority side")
	time.Sleep(50 * time.Millisecond)
	if c.Node(0).CommitIndex() != 3 {
		t.Fatalf("slot 4 must stay uncommitted, got %d", c.Node(0).CommitIndex())
	}

	// Partition: old leader + one follower vs the majority. The directed
	// blocks above are a subset of this partition.
	c.Partition([]int{0, 1}, []int{2, 3, 4})

	// Dirty write in the minority: accepted by 0 and 1, never committed.
	if _, err := c.Node(0).Propose([]byte("dirty-5")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		e, ok := c.Node(1).LogEntry(5)
		return ok && string(e.Cmd) == "dirty-5"
	}, "dirty slot 5 on minority side")
	time.Sleep(50 * time.Millisecond)
	if c.Node(0).CommitIndex() != 3 || c.Node(1).CommitIndex() != 3 {
		t.Fatalf("minority must not commit: %d %d",
			c.Node(0).CommitIndex(), c.Node(1).CommitIndex())
	}

	// The majority cannot elect a new leader while the old lease holds.
	c.Node(2).Campaign()
	time.Sleep(50 * time.Millisecond)
	if c.Node(2).IsLeader() {
		t.Fatal("node 2 became leader before the old lease expired")
	}

	// After lease expiry, Phase 1 succeeds in the majority.
	waitFor(t, 3*time.Second, func() bool {
		if c.Node(2).IsLeader() {
			return true
		}
		c.Node(2).Campaign()
		return false
	}, "node 2 to win election after lease expiry")

	// Phase 1 repair completes slot 4 with its original value.
	waitFor(t, 3*time.Second, func() bool { return c.Node(4).CommitIndex() == 4 },
		"majority commits kept-4")
	if e, _ := c.Node(3).LogEntry(4); string(e.Cmd) != "kept-4" {
		t.Fatalf("slot 4 = %q, want kept-4", e.Cmd)
	}

	// Fresh proposal takes slot 5 on the majority side.
	if _, err := c.Node(2).Propose([]byte("new-5")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { return c.Node(4).CommitIndex() == 5 },
		"majority commits new-5")

	// Heal and commit one more entry so the minority learns the new log via
	// the commit/fetch path; its dirty slot 5 must be overwritten.
	c.Heal()
	if _, err := c.Node(2).Propose([]byte("post-6")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { return c.Node(0).CommitIndex() == 6 },
		"old leader converges")
	waitFor(t, 3*time.Second, func() bool { return c.Node(1).CommitIndex() == 6 },
		"isolated follower converges")

	log := assertSameLog(t, c)
	want := [][]byte{
		[]byte("cmd-1"), []byte("cmd-2"), []byte("cmd-3"),
		[]byte("kept-4"), []byte("new-5"), []byte("post-6"),
	}
	if len(log) != len(want) {
		t.Fatalf("log = %q, want %q", log, want)
	}
	for i := range want {
		if !bytes.Equal(log[i], want[i]) {
			t.Fatalf("slot %d = %q, want %q (log %q)", i+1, log[i], want[i], log)
		}
	}
	// The dirty value must be gone from every node's log at slot 5.
	for i := 0; i < c.Size(); i++ {
		if e, _ := c.Node(i).LogEntry(5); string(e.Cmd) != "new-5" {
			t.Fatalf("node %d slot 5 = %q, want new-5", i, e.Cmd)
		}
	}
}

// TestNoHoleCommit constructs a genuine hole: slot 2 exists only on a
// minority while slot 3 is accepted (but uncommitted) on the majority side.
// The new leader's Phase 1 must fill slot 2 with a no-op and preserve
// slot 3, so the log commits without gaps.
func TestNoHoleCommit(t *testing.T) {
	// Long lease: the old leader must stay lease-valid across two proposals
	// but expire before the new election.
	c, err := NewCluster(5, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	elect(t, c, 0)
	if _, err := c.Node(0).Propose([]byte("one")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { return c.Node(4).CommitIndex() == 1 }, "slot 1")

	// Slot 2 lands only on the minority {0,1}.
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	if _, err := c.Node(0).Propose([]byte("two")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		_, ok := c.Node(1).LogEntry(2)
		return ok
	}, "slot 2 on minority")

	// Heal and immediately propose slot 3: accepted by everyone, but
	// uncommitted because slot 2 never reached a majority.
	c.Heal()
	if _, err := c.Node(0).Propose([]byte("three")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		e, ok := c.Node(4).LogEntry(3)
		return ok && string(e.Cmd) == "three"
	}, "slot 3 accepted on majority side")
	time.Sleep(50 * time.Millisecond)
	if c.Node(4).CommitIndex() != 1 {
		t.Fatalf("slot 3 must not commit past the hole, got %d", c.Node(4).CommitIndex())
	}

	// Re-partition and wait out the lease; node 2 wins Phase 1 in the
	// majority. Its promise set has slot 3 but not slot 2: slot 2 becomes a
	// no-op hole fill, slot 3 keeps its value.
	c.Partition([]int{0, 1}, []int{2, 3, 4})
	waitFor(t, 5*time.Second, func() bool {
		if c.Node(2).IsLeader() {
			return true
		}
		c.Node(2).Campaign()
		return false
	}, "node 2 leads")
	waitFor(t, 3*time.Second, func() bool { return c.Node(4).CommitIndex() == 3 },
		"majority commits through slot 3")
	if e, _ := c.Node(3).LogEntry(2); !e.IsNoop() {
		t.Fatalf("slot 2 = %q, want no-op hole fill", e.Cmd)
	}
	if e, _ := c.Node(3).LogEntry(3); string(e.Cmd) != "three" {
		t.Fatalf("slot 3 = %q, want three", e.Cmd)
	}

	// Heal and commit one more entry; the minority converges and its dirty
	// slot 2 is replaced by the no-op.
	c.Heal()
	if _, err := c.Node(2).Propose([]byte("four")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { return c.Node(0).CommitIndex() == 4 },
		"minority converges")
	log := assertSameLog(t, c)
	if len(log) != 4 {
		t.Fatalf("log = %q", log)
	}
	if string(log[0]) != "one" || !bytes.Equal(log[1], NoopCmd) ||
		string(log[2]) != "three" || string(log[3]) != "four" {
		t.Fatalf("gap-free invariant violated: %q", log)
	}
}

// TestConcurrentPropose hammers the leader from many goroutines and checks
// that every node applies the same gap-free log.
func TestConcurrentPropose(t *testing.T) {
	c := newTestCluster(t, 5, false)
	elect(t, c, 0)
	const proposers = 8
	const perProposer = 10
	var wg sync.WaitGroup
	for p := 0; p < proposers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProposer; i++ {
				for {
					_, err := c.Node(0).Propose([]byte(fmt.Sprintf("p%d-%d", p, i)))
					if err == nil {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
		}(p)
	}
	wg.Wait()
	total := uint64(proposers * perProposer)
	waitFor(t, 5*time.Second, func() bool { return c.Node(4).CommitIndex() == total },
		"all proposals committed")
	log := assertSameLog(t, c)
	if uint64(len(log)) != total {
		t.Fatalf("applied %d entries, want %d", len(log), total)
	}
	for i, e := range log {
		if len(e) == 0 {
			t.Fatalf("slot %d has empty command", i+1)
		}
	}
}

// TestNodeRestartRecovery verifies that a node rebuilt from its store
// recovers the accepted log, including highest-ballot resolution.
func TestNodeRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 5)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("node%d.log", i))
	}
	c, err := NewCluster(5, testLease, paths)
	if err != nil {
		t.Fatal(err)
	}
	elect(t, c, 0)
	for i := 1; i <= 3; i++ {
		if _, err := c.Node(0).Propose([]byte(fmt.Sprintf("cmd-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 3*time.Second, func() bool { return c.Node(3).CommitIndex() == 3 }, "commit")
	c.Close()

	// Rebuild node 3 from its store alone.
	st, err := OpenStore(paths[3], false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n, err := NewNode(3, []int{0, 1, 2, 3, 4}, st, testLease, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 3; i++ {
		e, ok := n.LogEntry(i)
		if !ok || string(e.Cmd) != fmt.Sprintf("cmd-%d", i) {
			t.Fatalf("recovered slot %d = %+v ok=%v", i, e, ok)
		}
	}
}

// TestNoGoroutineLeak asserts the cluster shuts down cleanly.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	c, err := NewCluster(5, testLease, nil)
	if err != nil {
		t.Fatal(err)
	}
	elect(t, c, 0)
	if _, err := c.Node(0).Propose([]byte("x")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { return c.Node(0).CommitIndex() == 1 }, "commit")
	c.Close()
	waitFor(t, 3*time.Second, func() bool { return runtime.NumGoroutine() <= before },
		fmt.Sprintf("goroutines to drain (before=%d now=%d)", before, runtime.NumGoroutine()))
}
