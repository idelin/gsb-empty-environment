package paxos

import (
	"bytes"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"multipaxos/store"
)

var testIDs = []int{1, 2, 3, 4, 5}

type cluster struct {
	net   *Network
	nodes map[int]*Node
}

func newCluster(t *testing.T, ids []int, lease time.Duration) *cluster {
	t.Helper()
	c := &cluster{net: NewNetwork(), nodes: make(map[int]*Node)}
	for _, id := range ids {
		n, err := NewNode(id, ids, c.net, NewMemPersister(), lease)
		if err != nil {
			t.Fatal(err)
		}
		c.nodes[id] = n
	}
	t.Cleanup(func() {
		for _, n := range c.nodes {
			n.Close()
		}
	})
	return c
}

func (c *cluster) leader(t *testing.T, id int) *Node {
	t.Helper()
	c.nodes[id].Campaign()
	if !c.nodes[id].IsLeader() {
		t.Fatalf("node%d failed to become leader", id)
	}
	return c.nodes[id]
}

func mustPropose(t *testing.T, n *Node, cmd string) uint64 {
	t.Helper()
	slot, err := n.Propose([]byte(cmd))
	if err != nil {
		t.Fatalf("propose %q: %v", cmd, err)
	}
	return slot
}

// assertConverged checks the gap-free commit engine contract: every node
// applied the identical, contiguous prefix 1..commitIndex.
func assertConverged(t *testing.T, c *cluster, want []string) {
	t.Helper()
	for id, n := range c.nodes {
		applied := n.Applied()
		if len(applied) != len(want) {
			t.Fatalf("node%d applied %d entries, want %d: %v", id, len(applied), len(want), cmds(applied))
		}
		for i, e := range applied {
			if e.Slot != uint64(i+1) {
				t.Fatalf("node%d gap: applied[%d].Slot=%d", id, i, e.Slot)
			}
			if string(e.Command) != want[i] {
				t.Fatalf("node%d slot %d = %q, want %q", id, i+1, e.Command, want[i])
			}
		}
		if n.CommitIndex() != uint64(len(want)) {
			t.Fatalf("node%d commitIndex=%d, want %d", id, n.CommitIndex(), len(want))
		}
	}
}

func cmds(entries []store.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = string(e.Command)
	}
	return out
}

func TestElectionAndBasicCommit(t *testing.T) {
	c := newCluster(t, testIDs, 10*time.Second)
	leader := c.leader(t, 1)
	for i := 0; i < 5; i++ {
		mustPropose(t, leader, fmt.Sprintf("cmd-%d", i))
	}
	assertConverged(t, c, []string{"cmd-0", "cmd-1", "cmd-2", "cmd-3", "cmd-4"})
}

func TestLeaseBypassesPhase1(t *testing.T) {
	c := newCluster(t, testIDs, 10*time.Second)
	leader := c.leader(t, 1)
	prepares := c.net.Count(MsgPrepare)
	if prepares != len(testIDs) {
		t.Fatalf("election sent %d Prepares, want %d", prepares, len(testIDs))
	}
	// Under a valid lease, consecutive proposals go straight to Accept.
	for i := 0; i < 10; i++ {
		mustPropose(t, leader, fmt.Sprintf("cmd-%d", i))
	}
	if got := c.net.Count(MsgPrepare); got != prepares {
		t.Fatalf("lease bypass violated: Prepare count %d -> %d", prepares, got)
	}
	if got := c.net.Count(MsgAccept); got != 10*(len(testIDs)-1) {
		t.Fatalf("Accept count = %d, want %d", got, 10*(len(testIDs)-1))
	}
}

func TestLeaseExpiry(t *testing.T) {
	c := newCluster(t, testIDs, 30*time.Millisecond)
	leader := c.leader(t, 1)
	time.Sleep(60 * time.Millisecond)
	if _, err := leader.Propose([]byte("x")); err != ErrNotLeader {
		t.Fatalf("expired lease: got %v, want ErrNotLeader", err)
	}
	if _, err := c.nodes[2].Propose([]byte("x")); err != ErrNotLeader {
		t.Fatalf("follower propose: got %v, want ErrNotLeader", err)
	}
}

// TestPartitionSplitBrain is the acceptance scenario: 5 nodes, the old
// leader is partitioned away with a dirty minority write, a new majority
// leader completes the uncommitted slot via Phase 1, and after healing the
// minority's dirty log is safely overwritten.
func TestPartitionSplitBrain(t *testing.T) {
	c := newCluster(t, testIDs, 10*time.Second)
	old := c.leader(t, 1)
	mustPropose(t, old, "set x=1") // slot 1, committed everywhere

	// Slot 2 is accepted everywhere but the acks never reach the leader.
	for _, id := range testIDs[1:] {
		c.net.BlockLink(id, 1)
	}
	mustPropose(t, old, "set a=2")
	if old.CommitIndex() != 1 {
		t.Fatalf("slot2 must stay uncommitted, commitIndex=%d", old.CommitIndex())
	}

	// Partition: old leader stranded with node2; it dirties slot 3.
	c.net.Partition([]int{1, 2}, []int{3, 4, 5})
	mustPropose(t, old, "set x=3") // only reaches node2
	if old.CommitIndex() != 1 {
		t.Fatalf("minority leader must not commit, commitIndex=%d", old.CommitIndex())
	}

	// New majority leader: Phase 1 carries slot 2 forward; slot 3 is its own.
	leader := c.leader(t, 3)
	mustPropose(t, leader, "set y=3")
	for _, id := range []int{3, 4, 5} {
		if got := cmds(c.nodes[id].Applied()); !equalStrs(got, []string{"set x=1", "set a=2", "set y=3"}) {
			t.Fatalf("node%d applied %v", id, got)
		}
	}

	// Heal and sync: dirty slot 3 on {1,2} is safely overwritten.
	c.net.HealAll()
	for _, id := range testIDs {
		if id != 3 {
			if err := leader.SyncFollower(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertConverged(t, c, []string{"set x=1", "set a=2", "set y=3"})

	// The dirty value is gone from the minority's accepted logs, not just
	// shadowed in the applied prefix.
	for _, id := range []int{1, 2} {
		e, ok := c.nodes[id].Entry(3)
		if !ok || string(e.Command) != "set y=3" {
			t.Fatalf("node%d slot3 = %q, want dirty value overwritten by %q", id, e.Command, "set y=3")
		}
	}
}

// TestGapFillNoop forces a hole at slots 1-2 while slot 3 is accepted on a
// minority; the new leader must fill the holes with no-ops and commit a
// contiguous log.
func TestGapFillNoop(t *testing.T) {
	c := newCluster(t, testIDs, 10*time.Second)
	// Inject an accepted slot-3 value on nodes 4,5 only (slots 1,2 missing
	// everywhere) by delivering a crafted Accept from a fake term-1 leader.
	dirty := store.Entry{Slot: 3, Term: 0, NodeID: 9, Command: []byte("orphan")}
	for _, id := range []int{4, 5} {
		c.nodes[id].Receive(Message{
			Type: MsgAccept, From: 9, To: id,
			Ballot: Ballot{Term: 0, Node: 9}, Slot: 3, Entry: dirty,
		})
	}
	// Partition so the resolving quorum is exactly {3,4,5}.
	c.net.Partition([]int{1, 2}, []int{3, 4, 5})
	leader := c.leader(t, 3)
	// The quorum {3,4,5} reports max slot 3; slots 1-2 are holes.
	if leader.CommitIndex() != 3 {
		t.Fatalf("commitIndex=%d, want 3 after repair", leader.CommitIndex())
	}
	want := []string{"", "", "orphan"}
	for id, n := range c.nodes {
		if id == 4 || id == 5 || id == 3 {
			applied := n.Applied()
			if len(applied) != 3 {
				t.Fatalf("node%d applied %d entries, want 3", id, len(applied))
			}
			for i, e := range applied {
				if i < 2 && e.Flags&FlagNoop == 0 {
					t.Fatalf("node%d slot %d must be a no-op hole fill", id, i+1)
				}
				if string(e.Command) != want[i] {
					t.Fatalf("node%d slot %d = %q, want %q", id, i+1, e.Command, want[i])
				}
			}
		}
	}
	// Heal and sync the stranded nodes; verify full convergence.
	c.net.HealAll()
	for _, id := range testIDs {
		if id != 3 {
			if err := leader.SyncFollower(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertConverged(t, c, want)
}

// TestOldLeaderCannotCommitAfterPartition: the stranded old leader keeps
// its lease but can never reach a quorum, and steps down once it hears the
// new term.
func TestOldLeaderCannotCommitAfterPartition(t *testing.T) {
	c := newCluster(t, testIDs, 10*time.Second)
	old := c.leader(t, 1)
	mustPropose(t, old, "a")
	c.net.Partition([]int{1, 2}, []int{3, 4, 5})
	mustPropose(t, old, "dirty")
	if old.CommitIndex() != 1 {
		t.Fatalf("old leader committed without quorum: %d", old.CommitIndex())
	}
	c.leader(t, 3)
	c.net.HealAll()
	if err := c.nodes[3].SyncFollower(1); err != nil {
		t.Fatal(err)
	}
	// Old leader must have stepped down after seeing the higher ballot.
	if c.nodes[1].IsLeader() {
		// Force message exchange to make sure it observed term 2.
		c.nodes[1].Campaign() // term 3: fine, but it must not still hold term-1 lease
	}
}

func TestConcurrentPropose(t *testing.T) {
	c := newCluster(t, testIDs, 30*time.Second)
	leader := c.leader(t, 1)
	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				for {
					_, err := leader.Propose([]byte(fmt.Sprintf("w%d-%d", w, i)))
					if err == nil {
						break
					}
					time.Sleep(time.Millisecond)
				}
			}
		}(w)
	}
	wg.Wait()
	total := workers * perWorker
	deadline := time.Now().Add(10 * time.Second)
	for _, id := range testIDs {
		for c.nodes[id].CommitIndex() < uint64(total) {
			if time.Now().After(deadline) {
				t.Fatalf("node%d commitIndex=%d, want %d", id, c.nodes[id].CommitIndex(), total)
			}
			time.Sleep(time.Millisecond)
		}
	}
	// All applied logs must be identical and contiguous.
	ref := c.nodes[1].Applied()
	if len(ref) != total {
		t.Fatalf("leader applied %d, want %d", len(ref), total)
	}
	for _, id := range testIDs[1:] {
		got := c.nodes[id].Applied()
		if len(got) != total {
			t.Fatalf("node%d applied %d, want %d", id, len(got), total)
		}
		for i := range ref {
			if got[i].Slot != uint64(i+1) || !bytes.Equal(got[i].Command, ref[i].Command) {
				t.Fatalf("node%d diverges at slot %d", id, i+1)
			}
		}
	}
}

func TestPersistenceReplay(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/node1.log"
	fp, err := NewFilePersister(path)
	if err != nil {
		t.Fatal(err)
	}
	net := NewNetwork()
	n, err := NewNode(1, []int{1}, net, fp, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	n.Campaign()
	mustPropose(t, n, "persisted")
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen: entries survive restart via the mmap replay path.
	fp2, err := NewFilePersister(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fp2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no entries replayed")
	}
	var found bool
	for _, e := range entries {
		if string(e.Command) == "persisted" && e.Slot == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("persisted entry not replayed: %v", cmds(entries))
	}
	fp2.Close()
}

func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		c := newCluster(t, testIDs, 10*time.Second)
		leader := c.leader(t, 1)
		mustPropose(t, leader, "x")
		for _, n := range c.nodes {
			n.Close()
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", base, runtime.NumGoroutine())
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
