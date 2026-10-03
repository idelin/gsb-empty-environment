package paxos

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// recorder is a test state machine capturing applied entries in order.
type recorder struct {
	mu      sync.Mutex
	entries []Entry
}

func (r *recorder) apply(_ uint64, e Entry) {
	r.mu.Lock()
	r.entries = append(r.entries, e)
	r.mu.Unlock()
}

func (r *recorder) snapshot() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

func newCluster(n int, lease time.Duration) (*Transport, []*Node, []*recorder) {
	tr := NewTransport(n)
	nodes := make([]*Node, n)
	recs := make([]*recorder, n)
	for i := 0; i < n; i++ {
		recs[i] = &recorder{}
		nodes[i] = NewNode(i, n, tr, recs[i].apply, lease)
	}
	return tr, nodes, recs
}

func closeAll(nodes []*Node) {
	for _, n := range nodes {
		n.Close()
	}
}

// proposeRetry proposes, retrying on transient backpressure from the
// bounded intake queue.
func proposeRetry(node *Node, v []byte) error {
	for {
		err := node.Propose(v)
		if errors.Is(err, ErrBackpressure) {
			runtime.Gosched()
			continue
		}
		return err
	}
}

func valuesOf(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		if e.Noop {
			out[i] = "<noop>"
		} else {
			out[i] = string(e.Value)
		}
	}
	return out
}

func assertSameValues(t *testing.T, recs []*recorder, want []string) {
	t.Helper()
	for i, r := range recs {
		got := valuesOf(r.snapshot())
		if len(got) != len(want) {
			t.Fatalf("node %d applied %d entries %v, want %d %v", i, len(got), got, len(want), want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("node %d entry %d = %q, want %q (full: %v)", i, j+1, got[j], want[j], got)
			}
		}
	}
}

func TestElectionAndBasicCommit(t *testing.T) {
	_, nodes, recs := newCluster(5, time.Minute)
	defer closeAll(nodes)

	if !nodes[0].Campaign() {
		t.Fatal("campaign failed on fully connected cluster")
	}
	if !nodes[0].IsLeader() {
		t.Fatal("node 0 should be leader")
	}
	want := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		v := fmt.Sprintf("cmd-%d", i)
		if err := proposeRetry(nodes[0], []byte(v)); err != nil {
			t.Fatalf("propose %q: %v", v, err)
		}
		want = append(want, v)
	}
	for i, n := range nodes {
		if n.CommitIndex() != 10 {
			t.Fatalf("node %d commitIndex = %d, want 10", i, n.CommitIndex())
		}
		if n.Applied() != 10 {
			t.Fatalf("node %d applied = %d, want 10", i, n.Applied())
		}
	}
	assertSameValues(t, recs, want)
}

// TestLeaseBypassesPhase1 verifies that under a valid leader lease,
// proposals commit with a single Phase 2 round and no Prepare traffic.
func TestLeaseBypassesPhase1(t *testing.T) {
	tr, nodes, _ := newCluster(5, time.Minute)
	defer closeAll(nodes)

	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	preparesAfterElection := tr.Prepares.Load()
	acceptsAfterElection := tr.Accepts.Load()

	const proposals = 20
	for i := 0; i < proposals; i++ {
		if err := proposeRetry(nodes[0], []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	if got := tr.Prepares.Load() - preparesAfterElection; got != 0 {
		t.Fatalf("lease fast path sent %d Prepare messages, want 0", got)
	}
	if got := tr.Accepts.Load() - acceptsAfterElection; got != proposals*4 {
		t.Fatalf("sent %d Accept messages, want %d (one Phase 2 round per proposal)", got, proposals*4)
	}
	if nodes[0].CommitIndex() != proposals {
		t.Fatalf("commitIndex = %d, want %d", nodes[0].CommitIndex(), proposals)
	}
}

// TestLeaseExpiryForcesPhase1 verifies that once the lease lapses the
// leader runs Phase 1 again before committing.
func TestLeaseExpiryForcesPhase1(t *testing.T) {
	tr, nodes, _ := newCluster(5, 30*time.Millisecond)
	defer closeAll(nodes)

	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	if err := proposeRetry(nodes[0], []byte("a")); err != nil {
		t.Fatal(err)
	}
	before := tr.Prepares.Load()
	time.Sleep(60 * time.Millisecond) // let the lease expire
	if err := proposeRetry(nodes[0], []byte("b")); err != nil {
		t.Fatal(err)
	}
	if got := tr.Prepares.Load() - before; got == 0 {
		t.Fatal("expected a new Phase 1 round after lease expiry")
	}
}

// TestPartitionLeaderReplacement is the acceptance scenario: with a 5-node
// cluster partitioned 2|3, the old leader cannot commit, the majority side
// elects a new leader that completes uncommitted slots via Phase 1, and
// after healing the minority's dirty log entries are safely overwritten.
func TestPartitionLeaderReplacement(t *testing.T) {
	tr, nodes, recs := newCluster(5, time.Minute)
	defer closeAll(nodes)

	if !nodes[0].Campaign() {
		t.Fatal("initial campaign failed")
	}
	if err := proposeRetry(nodes[0], []byte("v1")); err != nil {
		t.Fatalf("commit v1: %v", err)
	}

	// Partition: {0,1} | {2,3,4}. Node 0 is the isolated old leader.
	tr.SetPartition([]int{0, 1}, []int{2, 3, 4})

	// The old leader's proposal can only reach 2/5 nodes: no quorum, but
	// it leaves a dirty (accepted, uncommitted) entry on nodes 0 and 1.
	if err := proposeRetry(nodes[0], []byte("v2-dirty")); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("old leader propose = %v, want ErrNoQuorum", err)
	}
	if nodes[0].CommitIndex() != 1 {
		t.Fatalf("old leader committed under partition: commitIndex = %d, want 1", nodes[0].CommitIndex())
	}
	if e, ok := nodes[1].LogEntry(2); !ok || string(e.Value) != "v2-dirty" {
		t.Fatalf("expected dirty entry on minority node 1, got %+v ok=%v", e, ok)
	}
	// A fresh campaign on the minority side must also fail.
	if err := proposeRetry(nodes[0], []byte("x")); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old leader repropose = %v, want ErrNotLeader", err)
	}

	// Majority side elects a new leader; Phase 1 completes slot 1.
	if !nodes[2].Campaign() {
		t.Fatal("majority-side campaign failed")
	}
	if err := proposeRetry(nodes[2], []byte("v3")); err != nil {
		t.Fatalf("new leader propose: %v", err)
	}
	if nodes[2].CommitIndex() != 2 {
		t.Fatalf("new leader commitIndex = %d, want 2", nodes[2].CommitIndex())
	}
	// Minority still untouched.
	if nodes[0].CommitIndex() != 1 || nodes[1].CommitIndex() != 1 {
		t.Fatal("minority commit index moved while partitioned")
	}

	// Heal. The next commit broadcast brings the minority up to date and
	// overwrites the dirty slot 2 with the committed value.
	tr.Heal()
	if err := proposeRetry(nodes[2], []byte("v4")); err != nil {
		t.Fatalf("post-heal propose: %v", err)
	}

	want := []string{"v1", "v3", "v4"}
	assertSameValues(t, recs, want)
	for i, n := range nodes {
		if n.CommitIndex() != 3 || n.Applied() != 3 {
			t.Fatalf("node %d commit=%d applied=%d, want 3/3", i, n.CommitIndex(), n.Applied())
		}
		e, ok := n.LogEntry(2)
		if !ok || string(e.Value) != "v3" {
			t.Fatalf("node %d slot 2 = %+v ok=%v, want committed v3 (dirty overwritten)", i, e, ok)
		}
	}
	if nodes[0].IsLeader() {
		t.Fatal("old leader should have stepped down after heal")
	}
}

// TestHoleRepair verifies that a new leader fills gaps in the log with
// no-op entries so the committed prefix is always contiguous.
func TestHoleRepair(t *testing.T) {
	tr, nodes, recs := newCluster(5, time.Minute)
	defer closeAll(nodes)

	// Inject an acceptor state with a hole: slots 1 and 3 accepted on a
	// majority (nodes 0,1,2), slot 2 missing everywhere.
	ballot1 := makeBallot(1, 0)
	inject := func(idx uint64, val string) {
		msg := AcceptMsg{From: 0, Ballot: ballot1, Index: idx, Entry: Entry{Value: []byte(val)}}
		for peer := 0; peer < 3; peer++ {
			if am, ok := tr.Accept(0, peer, msg); !ok || !am.OK {
				t.Fatalf("inject idx %d to node %d failed", idx, peer)
			}
		}
	}
	inject(1, "a")
	inject(3, "c")

	if !nodes[2].Campaign() {
		t.Fatal("campaign failed")
	}
	// Repair must have committed slots 1..3 contiguously, slot 2 a no-op.
	for i, n := range nodes {
		if n.CommitIndex() != 3 || n.Applied() != 3 {
			t.Fatalf("node %d commit=%d applied=%d, want 3/3", i, n.CommitIndex(), n.Applied())
		}
		e, ok := n.LogEntry(2)
		if !ok || !e.Noop {
			t.Fatalf("node %d slot 2 = %+v ok=%v, want committed no-op filler", i, e, ok)
		}
	}
	assertSameValues(t, recs, []string{"a", "<noop>", "c"})

	// The leader continues seamlessly after the repaired prefix.
	if err := proposeRetry(nodes[2], []byte("d")); err != nil {
		t.Fatalf("propose after repair: %v", err)
	}
	assertSameValues(t, recs, []string{"a", "<noop>", "c", "d"})
}

// TestConcurrentProposals hammers the leader from many goroutines and
// verifies exactly-once, hole-free, identically-ordered commits on all
// replicas. Run with -race.
func TestConcurrentProposals(t *testing.T) {
	_, nodes, recs := newCluster(5, time.Minute)
	defer closeAll(nodes)

	if !nodes[0].Campaign() {
		t.Fatal("campaign failed")
	}
	const (
		workers   = 32
		perWorker = 25
		total     = workers * perWorker
	)
	var wg sync.WaitGroup
	errs := make(chan error, total)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				v := []byte(fmt.Sprintf("w%02d-%02d", w, i))
				if err := proposeRetry(nodes[0], v); err != nil {
					errs <- fmt.Errorf("worker %d: %w", w, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Every replica applied exactly `total` entries, in identical order.
	leaderSeq := valuesOf(recs[0].snapshot())
	if len(leaderSeq) != total {
		t.Fatalf("leader applied %d entries, want %d", len(leaderSeq), total)
	}
	seen := make(map[string]int, total)
	for _, v := range leaderSeq {
		seen[v]++
	}
	if len(seen) != total {
		t.Fatalf("duplicate or missing commits: %d unique values, want %d", len(seen), total)
	}
	for i := 1; i < len(nodes); i++ {
		if nodes[i].CommitIndex() != total || nodes[i].Applied() != total {
			t.Fatalf("node %d commit=%d applied=%d, want %d", i, nodes[i].CommitIndex(), nodes[i].Applied(), total)
		}
		seq := valuesOf(recs[i].snapshot())
		for j := range leaderSeq {
			if seq[j] != leaderSeq[j] {
				t.Fatalf("node %d diverges at slot %d: %q vs leader %q", i, j+1, seq[j], leaderSeq[j])
			}
		}
	}
}

// TestNoGoroutineLeak asserts that closing a cluster returns the goroutine
// count to baseline.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	func() {
		_, nodes, _ := newCluster(5, time.Minute)
		defer closeAll(nodes)
		if !nodes[0].Campaign() {
			t.Fatal("campaign failed")
		}
		for i := 0; i < 50; i++ {
			if err := proposeRetry(nodes[0], []byte(fmt.Sprintf("v%d", i))); err != nil {
				t.Fatal(err)
			}
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, got)
	}
}

// TestProposeAfterClose ensures a closed node fails fast instead of hanging.
func TestProposeAfterClose(t *testing.T) {
	_, nodes, _ := newCluster(3, time.Minute)
	closeAll(nodes)
	if err := nodes[0].Propose([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Propose on closed node = %v, want ErrClosed", err)
	}
}
