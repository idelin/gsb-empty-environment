package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

const testLease = time.Minute

func logValues(n *Node) []string {
	var out []string
	for _, e := range n.Log() {
		if e.Noop {
			out = append(out, "<noop>")
		} else {
			out = append(out, string(e.Value))
		}
	}
	return out
}

func requireLog(t *testing.T, n *Node, want ...string) {
	t.Helper()
	got := logValues(n)
	if len(got) != len(want) {
		t.Fatalf("node %d: log = %v, want %v", n.ID(), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("node %d: log = %v, want %v", n.ID(), got, want)
		}
	}
}

func requireAllLogs(t *testing.T, c *Cluster, want ...string) {
	t.Helper()
	for _, n := range c.Nodes {
		requireLog(t, n, want...)
	}
}

// injectAccepted writes acceptor state directly (simulates a dirty log left
// behind by a crashed/partitioned leader).
func injectAccepted(n *Node, e Entry) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.accepted[e.Index] = e
	if e.Ballot.Greater(n.promised) {
		n.promised = e.Ballot
	}
}

func TestElectAndCommitBasic(t *testing.T) {
	c := NewCluster(5, testLease)
	leader := c.Nodes[0]
	for i, v := range []string{"a", "b", "c"} {
		idx, err := leader.Propose([]byte(v))
		if err != nil {
			t.Fatalf("propose %q: %v", v, err)
		}
		if idx != uint64(i+1) {
			t.Fatalf("slot = %d, want %d", idx, i+1)
		}
	}
	requireAllLogs(t, c, "a", "b", "c")
	if ci := leader.CommitIndex(); ci != 3 {
		t.Fatalf("commitIndex = %d, want 3", ci)
	}
}

func TestLeaseBypassesPhase1(t *testing.T) {
	c := NewCluster(5, testLease)
	leader := c.Nodes[0]
	if _, err := leader.Propose([]byte("first")); err != nil {
		t.Fatal(err)
	}
	leader.BroadcastHeartbeat()

	c.Net.ResetStats()
	if _, err := leader.Propose([]byte("second")); err != nil {
		t.Fatal(err)
	}
	prepare, accept, _, _ := c.Net.Stats()
	if prepare != 0 {
		t.Fatalf("phase 1 not bypassed under lease: %d prepare calls", prepare)
	}
	if accept == 0 {
		t.Fatal("expected phase 2 accept calls")
	}
	requireAllLogs(t, c, "first", "second")
}

func TestLeaseRejectsForeignPrepare(t *testing.T) {
	c := NewCluster(5, testLease)
	leader := c.Nodes[0]
	if err := leader.Elect(); err != nil {
		t.Fatal(err)
	}
	leader.BroadcastHeartbeat() // followers grant lease to node 0

	// Another node cannot run phase 1 while the lease is held.
	if err := c.Nodes[2].Elect(); err != ErrNoQuorum {
		t.Fatalf("elect under foreign lease = %v, want ErrNoQuorum", err)
	}
	if c.Nodes[2].IsLeader() {
		t.Fatal("node 2 must not become leader during lease")
	}

	// After the lease expires, election succeeds.
	c.Advance(2 * testLease)
	if err := c.Nodes[2].Elect(); err != nil {
		t.Fatalf("elect after lease expiry: %v", err)
	}
	if !c.Nodes[2].IsLeader() {
		t.Fatal("node 2 should be leader")
	}
	if c.Nodes[0].IsLeader() {
		t.Fatal("node 0 should have stepped down")
	}
}

// TestPartitionMajorityTakeover is the acceptance scenario:
//   - 5-node cluster, node 0 is leader and commits "v1".
//   - Network partitions {0,1} | {2,3,4}.
//   - The old leader's write reaches only the minority (dirty log).
//   - Node 2 is elected by the majority, runs phase 1, and commits "real".
//   - After healing, the minority's dirty log is safely overwritten.
func TestPartitionMajorityTakeover(t *testing.T) {
	c := NewCluster(5, testLease)
	old := c.Nodes[0]

	if _, err := old.Propose([]byte("v1")); err != nil {
		t.Fatal(err)
	}
	old.BroadcastHeartbeat()
	requireAllLogs(t, c, "v1")

	c.Partition([]int{0, 1}, []int{2, 3, 4})

	// Old leader still holds its lease but cannot reach a quorum.
	if _, err := old.Propose([]byte("dirty")); err != ErrNoQuorum {
		t.Fatalf("minority propose = %v, want ErrNoQuorum", err)
	}
	if _, ok := c.Nodes[0].AcceptedAt(2); !ok {
		t.Fatal("expected dirty entry on node 0")
	}
	if _, ok := c.Nodes[1].AcceptedAt(2); !ok {
		t.Fatal("expected dirty entry on node 1")
	}
	for _, id := range []int{2, 3, 4} {
		if _, ok := c.Nodes[id].AcceptedAt(2); ok {
			t.Fatalf("dirty entry leaked to majority node %d", id)
		}
	}

	// Leases expire; the majority side elects a new leader.
	c.Advance(2 * testLease)
	newLeader := c.Nodes[2]
	idx, err := newLeader.Propose([]byte("real"))
	if err != nil {
		t.Fatalf("majority propose: %v", err)
	}
	if idx != 2 {
		t.Fatalf("slot = %d, want 2", idx)
	}
	for _, id := range []int{2, 3, 4} {
		requireLog(t, c.Nodes[id], "v1", "real")
	}
	// Minority side is still stale.
	requireLog(t, c.Nodes[0], "v1")
	requireLog(t, c.Nodes[1], "v1")

	// Heal: the new leader's heartbeat repairs the minority.
	c.Heal()
	newLeader.BroadcastHeartbeat()

	requireAllLogs(t, c, "v1", "real")
	for _, n := range c.Nodes {
		if _, ok := n.AcceptedAt(2); ok {
			t.Fatalf("node %d: dirty accepted entry survived repair", n.ID())
		}
	}
	if old.IsLeader() {
		t.Fatal("old leader must step down after heal")
	}

	// While the new leader's lease is valid the old leader cannot grab
	// leadership back.
	if _, err := old.Propose([]byte("zombie")); err != ErrNoQuorum {
		t.Fatalf("propose during foreign lease = %v, want ErrNoQuorum", err)
	}
	// After the lease expires it may win a fresh election, but the
	// committed prefix [v1, real] must be preserved.
	c.Advance(2 * testLease)
	idx3, err := old.Propose([]byte("zombie"))
	if err != nil {
		t.Fatalf("old leader re-election: %v", err)
	}
	if idx3 != 3 {
		t.Fatalf("slot = %d, want 3", idx3)
	}
	requireAllLogs(t, c, "v1", "real", "zombie")
}

func TestGapRepairFillsHoles(t *testing.T) {
	c := NewCluster(5, testLease)
	// Simulate a crashed leader that left slot 3 accepted on two nodes
	// while slots 1 and 2 were never written (holes).
	dirty := Entry{Index: 3, Ballot: Ballot{N: 1, ID: 0}, Value: []byte("x")}
	injectAccepted(c.Nodes[1], dirty)
	injectAccepted(c.Nodes[2], dirty)

	idx, err := c.Nodes[4].Propose([]byte("y"))
	if err != nil {
		t.Fatal(err)
	}
	if idx != 4 {
		t.Fatalf("slot = %d, want 4", idx)
	}
	// Slots 1,2 are no-op filled, slot 3 carries the repaired value.
	requireAllLogs(t, c, "<noop>", "<noop>", "x", "y")
	log := c.Nodes[4].Log()
	if !log[0].Noop || !log[1].Noop {
		t.Fatal("holes must be filled with no-op entries")
	}
	if log[2].Noop || string(log[2].Value) != "x" {
		t.Fatal("slot 3 must adopt the previously accepted value")
	}
}

func TestPhase1PreservesPossiblyChosenValue(t *testing.T) {
	c := NewCluster(5, testLease)
	// "d" was accepted by a minority under an old ballot; a new leader
	// whose quorum intersects those nodes must adopt, not overwrite, it.
	dirty := Entry{Index: 1, Ballot: Ballot{N: 1, ID: 0}, Value: []byte("d")}
	injectAccepted(c.Nodes[1], dirty)
	injectAccepted(c.Nodes[2], dirty)

	if _, err := c.Nodes[3].Propose([]byte("mine")); err != nil {
		t.Fatal(err)
	}
	requireAllLogs(t, c, "d", "mine")
}

func TestCommittedEntriesNeverOverwritten(t *testing.T) {
	c := NewCluster(5, testLease)
	n := c.Nodes[0]
	if _, err := n.Propose([]byte("a")); err != nil {
		t.Fatal(err)
	}
	// A stale/conflicting accept for an already committed slot must not
	// change the committed log.
	n.handleAccept(Entry{Index: 1, Ballot: Ballot{N: 99, ID: 4}, Value: []byte("evil")})
	requireLog(t, n, "a")
}

func TestConcurrentProposeSingleLeader(t *testing.T) {
	c := NewCluster(5, testLease)
	leader := c.Nodes[0]
	if err := leader.Elect(); err != nil {
		t.Fatal(err)
	}

	const writers = 4
	const perWriter = 25
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < perWriter; k++ {
				v := fmt.Sprintf("g%d-%d", g, k)
				if _, err := leader.Propose([]byte(v)); err != nil {
					errs <- fmt.Errorf("propose %s: %v", v, err)
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	log := leader.Log()
	if len(log) != writers*perWriter {
		t.Fatalf("committed %d entries, want %d", len(log), writers*perWriter)
	}
	// Gap-free, each value exactly once, identical on every node.
	seen := make(map[string]int)
	for i, e := range log {
		if e.Index != uint64(i+1) {
			t.Fatalf("gap: slot %d has index %d", i+1, e.Index)
		}
		seen[string(e.Value)]++
	}
	if len(seen) != writers*perWriter {
		t.Fatalf("duplicate or lost values: %d unique", len(seen))
	}
	for v, cnt := range seen {
		if cnt != 1 {
			t.Fatalf("value %q committed %d times", v, cnt)
		}
	}
	for _, n := range c.Nodes {
		got := logValues(n)
		if len(got) != len(log) {
			t.Fatalf("node %d: %d entries, want %d", n.ID(), len(got), len(log))
		}
	}
}

func TestConcurrentElectionContention(t *testing.T) {
	c := NewCluster(5, testLease)
	var wg sync.WaitGroup
	results := make(chan error, 3)
	for _, id := range []int{0, 1, 2} {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := c.Nodes[id].Propose([]byte(fmt.Sprintf("n%d", id)))
			results <- err
		}(id)
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if err != ErrNoQuorum && err != ErrNotLeader {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// At least one contender won; exactly one leader survives.
	if succeeded == 0 {
		t.Fatal("no contender managed to commit")
	}
	leaders := 0
	for _, n := range c.Nodes {
		if n.IsLeader() {
			leaders++
		}
	}
	if leaders != 1 {
		t.Fatalf("%d leaders, want 1", leaders)
	}
	// Every node agrees on slot 1 (only one value was chosen).
	want := logValues(c.Nodes[0])
	if len(want) != 1 {
		t.Fatalf("log = %v, want exactly 1 entry", want)
	}
	requireAllLogs(t, c, want[0])
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	func() {
		c := NewCluster(5, testLease)
		for i := 0; i < 10; i++ {
			if _, err := c.Nodes[0].Propose([]byte(fmt.Sprintf("v%d", i))); err != nil {
				t.Fatal(err)
			}
		}
		c.Nodes[0].BroadcastHeartbeat()
	}()
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
