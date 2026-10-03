package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// --- helpers -----------------------------------------------------------

func runUntil(c *Cluster, maxTicks int, cond func() bool) bool {
	for i := 0; i < maxTicks; i++ {
		if cond() {
			return true
		}
		c.Tick()
	}
	return cond()
}

func assertGapFree(t *testing.T, n *Node) {
	t.Helper()
	log := n.Log()
	ci := n.CommitIndex()
	if ci > len(log) {
		t.Fatalf("node %d: commitIndex %d beyond log len %d", n.ID(), ci, len(log))
	}
	for i := 0; i < ci; i++ {
		if log[i].Ballot.IsZero() {
			t.Fatalf("node %d: hole at committed slot %d", n.ID(), i)
		}
	}
}

func committedValues(n *Node) []string {
	var out []string
	for _, s := range n.Committed() {
		if s.NoOp {
			out = append(out, "<noop>")
		} else {
			out = append(out, s.Value)
		}
	}
	return out
}

func assertSameCommittedPrefix(t *testing.T, c *Cluster) {
	t.Helper()
	ref := committedValues(c.Node(0))
	for i := 1; i < c.Size(); i++ {
		got := committedValues(c.Node(i))
		if len(got) != len(ref) {
			t.Fatalf("node %d committed %v, want %v", i, got, ref)
		}
		for j := range ref {
			if got[j] != ref[j] {
				t.Fatalf("node %d committed %v, want %v", i, got, ref)
			}
		}
	}
}

// newTestNode builds a node whose outbound messages are captured
// synchronously, for white-box state-machine tests.
func newTestNode(id, size int) (*Node, *[]Message) {
	out := &[]Message{}
	n := newNode(id, size, DefaultConfig(), func(to int, m Message) {
		m.To = to
		*out = append(*out, m)
	})
	return n, out
}

func seedNode(n *Node, promised Ballot, log []Slot) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.promised = promised
	n.log = append([]Slot(nil), log...)
}

// --- ballot ------------------------------------------------------------

func TestBallotOrdering(t *testing.T) {
	cases := []struct {
		a, b Ballot
		less bool
	}{
		{Ballot{0, 0}, Ballot{0, 1}, true},
		{Ballot{1, 0}, Ballot{0, 4}, false},
		{Ballot{2, 3}, Ballot{2, 4}, true},
		{Ballot{2, 4}, Ballot{2, 4}, false},
	}
	for _, tc := range cases {
		if got := tc.a.Less(tc.b); got != tc.less {
			t.Fatalf("%v.Less(%v) = %v, want %v", tc.a, tc.b, got, tc.less)
		}
	}
	if !(Ballot{}).IsZero() || (Ballot{1, 0}).IsZero() {
		t.Fatal("IsZero broken")
	}
}

// --- phase 1 boundary conditions ---------------------------------------

func TestPreparePromiseAndRejection(t *testing.T) {
	n, out := newTestNode(1, 5)

	// First prepare is promised and carries the (empty) log.
	n.Handle(Message{Type: MsgPrepare, From: 0, Ballot: Ballot{3, 0}})
	msgs := *out
	if len(msgs) != 1 || msgs[0].Type != MsgPromise || msgs[0].Ballot != (Ballot{3, 0}) {
		t.Fatalf("expected promise for (3,0), got %+v", msgs)
	}
	if n.Promised() != (Ballot{3, 0}) {
		t.Fatalf("promised = %v", n.Promised())
	}

	// A lower prepare is nacked with the current promised ballot.
	*out = nil
	n.Handle(Message{Type: MsgPrepare, From: 2, Ballot: Ballot{2, 4}})
	msgs = *out
	if len(msgs) != 1 || msgs[0].Type != MsgPromise || msgs[0].Ballot != (Ballot{3, 0}) {
		t.Fatalf("expected nack with (3,0), got %+v", msgs)
	}

	// A higher prepare is promised again.
	*out = nil
	n.Handle(Message{Type: MsgPrepare, From: 4, Ballot: Ballot{5, 4}})
	msgs = *out
	if len(msgs) != 1 || msgs[0].Ballot != (Ballot{5, 4}) {
		t.Fatalf("expected promise for (5,4), got %+v", msgs)
	}
	if n.Promised() != (Ballot{5, 4}) {
		t.Fatalf("promised = %v", n.Promised())
	}
}

func TestPromiseCarriesLogForRepair(t *testing.T) {
	n, out := newTestNode(2, 5)
	seedNode(n, Ballot{1, 0}, []Slot{
		{Ballot: Ballot{1, 0}, Value: "a"},
		{}, // hole
		{Ballot: Ballot{1, 0}, Value: "x"},
	})
	n.Handle(Message{Type: MsgPrepare, From: 4, Ballot: Ballot{2, 4}})
	msgs := *out
	if len(msgs) != 1 || len(msgs[0].Log) != 3 {
		t.Fatalf("promise must carry full log, got %+v", msgs)
	}
	if msgs[0].Log[0].Value != "a" || !msgs[0].Log[1].Ballot.IsZero() || msgs[0].Log[2].Value != "x" {
		t.Fatalf("promise log mismatch: %+v", msgs[0].Log)
	}
}

// --- phase 2 boundary conditions ---------------------------------------

func TestAcceptRejectAndDirtyOverwrite(t *testing.T) {
	n, out := newTestNode(1, 5)
	n.Handle(Message{Type: MsgPrepare, From: 0, Ballot: Ballot{3, 0}})
	*out = nil

	// Accept below the promised ballot is rejected and not written.
	n.Handle(Message{Type: MsgAccept, From: 2, Ballot: Ballot{2, 2}, Slot: 0,
		Value: Slot{Ballot: Ballot{2, 2}, Value: "stale"}})
	if len(n.Log()) != 0 {
		t.Fatalf("stale accept must not be written, log=%v", n.Log())
	}
	msgs := *out
	if len(msgs) != 1 || msgs[0].Type != MsgAccepted || msgs[0].Ballot != (Ballot{3, 0}) {
		t.Fatalf("expected nack with promised ballot, got %+v", msgs)
	}

	// Accept at the promised ballot is written.
	*out = nil
	n.Handle(Message{Type: MsgAccept, From: 0, Ballot: Ballot{3, 0}, Slot: 0,
		Value: Slot{Ballot: Ballot{3, 0}, Value: "x"}})
	if got := n.Log()[0].Value; got != "x" {
		t.Fatalf("log[0] = %q", got)
	}

	// A higher-ballot accept overwrites the dirty (unchosen) entry.
	n.Handle(Message{Type: MsgAccept, From: 4, Ballot: Ballot{4, 4}, Slot: 0,
		Value: Slot{Ballot: Ballot{4, 4}, Value: "y"}})
	if got := n.Log()[0]; got.Value != "y" || got.Ballot != (Ballot{4, 4}) {
		t.Fatalf("dirty entry not overwritten: %+v", got)
	}
}

func TestFollowerCommitIsGapFree(t *testing.T) {
	n, _ := newTestNode(1, 5)
	b := Ballot{2, 0}
	mk := func(v string) Slot { return Slot{Ballot: b, Value: v} }

	// Accept for slot 2 creates holes at 0 and 1.
	n.Handle(Message{Type: MsgAccept, From: 0, Ballot: b, Slot: 2, Value: mk("c")})
	if ci := n.CommitIndex(); ci != 0 {
		t.Fatalf("commitIndex jumped over holes: %d", ci)
	}
	assertGapFree(t, n)

	// Heartbeat carrying the committed prefix fills the holes and
	// advances the commit index exactly over the contiguous prefix.
	n.Handle(Message{Type: MsgHeartbeat, From: 0, Ballot: b, Slot: -1,
		Committed: []Slot{mk("a"), mk("b"), mk("c")}})
	if ci := n.CommitIndex(); ci != 3 {
		t.Fatalf("commitIndex = %d, want 3", ci)
	}
	assertGapFree(t, n)
	if got := committedValues(n); fmt.Sprint(got) != "[a b c]" {
		t.Fatalf("committed = %v", got)
	}

	// A heartbeat from a stale ballot must not move anything.
	n.Handle(Message{Type: MsgHeartbeat, From: 3, Ballot: Ballot{1, 3}, Slot: -1,
		Committed: []Slot{mk("z"), mk("z"), mk("z"), mk("z")}})
	if ci := n.CommitIndex(); ci != 3 {
		t.Fatalf("stale heartbeat moved commitIndex to %d", ci)
	}
	if got := committedValues(n); fmt.Sprint(got) != "[a b c]" {
		t.Fatalf("stale heartbeat corrupted log: %v", got)
	}
}

// --- basic cluster behaviour -------------------------------------------

func TestElectionAndBasicCommit(t *testing.T) {
	c := NewCluster(5, DefaultConfig())
	if !runUntil(c, 100, func() bool { return c.Leader() == 0 }) {
		t.Fatal("node 0 did not become leader")
	}
	for i, v := range []string{"a", "b", "c"} {
		if slot, ok := c.Node(0).Propose(v); !ok || slot != i {
			t.Fatalf("propose %q -> (%d,%v)", v, slot, ok)
		}
	}
	ok := runUntil(c, 100, func() bool {
		for i := 0; i < c.Size(); i++ {
			if c.Node(i).CommitIndex() != 3 {
				return false
			}
		}
		return true
	})
	if !ok {
		t.Fatal("entries not committed everywhere")
	}
	for i := 0; i < c.Size(); i++ {
		assertGapFree(t, c.Node(i))
		if got := committedValues(c.Node(i)); fmt.Sprint(got) != "[a b c]" {
			t.Fatalf("node %d committed %v", i, got)
		}
	}
}

func TestLeaseBypassesPhase1(t *testing.T) {
	c := NewCluster(5, DefaultConfig())
	if !runUntil(c, 100, func() bool { return c.Leader() == 0 }) {
		t.Fatal("no leader")
	}
	leader := c.Node(0)
	before := leader.PreparesSent()
	if before != 1 {
		t.Fatalf("prepares before = %d, want 1", before)
	}
	for i := 0; i < 5; i++ {
		if _, ok := leader.Propose(fmt.Sprintf("v%d", i)); !ok {
			t.Fatalf("propose %d rejected under valid lease", i)
		}
	}
	if !runUntil(c, 100, func() bool { return leader.CommitIndex() == 5 }) {
		t.Fatal("entries not committed")
	}
	if got := leader.PreparesSent(); got != before {
		t.Fatalf("lease fast path ran phase 1: prepares %d -> %d", before, got)
	}
}

func TestLeaseExpiryStepsDown(t *testing.T) {
	c := NewCluster(5, DefaultConfig())
	if !runUntil(c, 100, func() bool { return c.Leader() == 0 }) {
		t.Fatal("no leader")
	}
	c.Isolate(0)
	// The isolated leader must relinquish its lease once it can no
	// longer reach a majority.
	if !runUntil(c, 200, func() bool { return c.Node(0).Role() != Leader }) {
		t.Fatal("isolated leader kept its lease")
	}
	if _, ok := c.Node(0).Propose("x"); ok {
		t.Fatal("propose accepted without a lease")
	}
	// The remaining majority must elect a new leader among nodes 1..4.
	if !runUntil(c, 300, func() bool { return c.Leader() > 0 }) {
		t.Fatal("majority side did not elect a new leader")
	}
}

// --- hole repair and value selection -----------------------------------

// Seeds a divergent state white-box, then lets a real election repair it:
// the highest-ballot value must win and genuine holes must be no-op
// filled so the committed prefix stays gap-free.
func TestHoleRepairAndHighestBallotWins(t *testing.T) {
	c := NewCluster(5, DefaultConfig())
	b := func(n uint64, id int) Ballot { return Ballot{n, id} }

	// Any majority the next leader collects intersects the seeded
	// acceptors, so the repair must see (and keep) these values.
	seedNode(c.Node(0), b(1, 0), []Slot{{Ballot: b(1, 0), Value: "a"}})
	seedNode(c.Node(1), b(2, 0), []Slot{
		{Ballot: b(2, 0), Value: "z"}, // highest ballot at slot 0
		{},                            // hole at slot 1
		{Ballot: b(1, 0), Value: "x"},
	})
	seedNode(c.Node(2), b(2, 0), []Slot{{Ballot: b(2, 0), Value: "z"}})
	// Give node 4 a head start so its election ballot dominates.
	seedNode(c.Node(4), b(5, 4), nil)

	if !runUntil(c, 300, func() bool { return c.Leader() == 4 }) {
		t.Fatal("node 4 did not become leader")
	}
	ok := runUntil(c, 300, func() bool {
		for i := 0; i < c.Size(); i++ {
			if c.Node(i).CommitIndex() != 3 {
				return false
			}
		}
		return true
	})
	if !ok {
		t.Fatal("repaired log not committed everywhere")
	}
	for i := 0; i < c.Size(); i++ {
		assertGapFree(t, c.Node(i))
		got := committedValues(c.Node(i))
		want := []string{"z", "<noop>", "x"}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("node %d committed %v, want %v", i, got, want)
		}
	}
}

// --- concurrency and leak safety ---------------------------------------

func TestConcurrentProposeAndTick(t *testing.T) {
	c := NewCluster(5, DefaultConfig())
	if !runUntil(c, 100, func() bool { return c.Leader() == 0 }) {
		t.Fatal("no leader")
	}
	leader := c.Node(0)

	before := runtime.NumGoroutine()
	stop := make(chan struct{})
	var tickerWg sync.WaitGroup

	tickerWg.Add(1)
	go func() { // clock driver
		defer tickerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				c.Tick()
				time.Sleep(time.Millisecond)
			}
		}
	}()

	var propWg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for g := 0; g < 8; g++ {
		propWg.Add(1)
		go func(g int) {
			defer propWg.Done()
			for i := 0; i < 25; i++ {
				if _, ok := leader.Propose(fmt.Sprintf("g%d-%d", g, i)); ok {
					mu.Lock()
					succeeded++
					mu.Unlock()
				}
			}
		}(g)
	}
	propWg.Wait()
	close(stop)
	tickerWg.Wait()

	if succeeded == 0 {
		t.Fatal("no proposal succeeded")
	}
	if !runUntil(c, 500, func() bool {
		for i := 0; i < c.Size(); i++ {
			if c.Node(i).CommitIndex() != succeeded {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("committed prefix did not converge to %d entries", succeeded)
	}
	for i := 0; i < c.Size(); i++ {
		assertGapFree(t, c.Node(i))
	}
	assertSameCommittedPrefix(t, c)
	_ = before
}

func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	c := NewCluster(5, DefaultConfig())
	runUntil(c, 100, func() bool { return c.Leader() == 0 })
	c.Node(0).Propose("x")
	c.Isolate(0)
	runUntil(c, 300, func() bool { return c.Leader() > 0 })
	c.Heal()
	c.TickN(50)

	// The library itself must not spawn goroutines.
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
