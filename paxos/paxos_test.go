package paxos

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
	"time"
)

func newLeader(c *Cluster, id int, lease time.Duration) *Leader {
	return NewLeader(c.Node(id), c.Peers(id), c, (c.Size()/2)+1, lease)
}

// decidedView returns slot->value for all decided slots on a node.
func decidedView(n *Node) map[uint64]string {
	out := map[uint64]string{}
	for _, s := range n.DecidedSlots() {
		e, _ := n.Decided(s)
		if e.Noop {
			out[s] = "<noop>"
		} else {
			out[s] = string(e.Value)
		}
	}
	return out
}

func assertConverged(t *testing.T, c *Cluster, slots uint64) {
	t.Helper()
	views := make([]map[uint64]string, c.Size())
	for i := 0; i < c.Size(); i++ {
		views[i] = decidedView(c.Node(i))
	}
	for slot := uint64(1); slot <= slots; slot++ {
		for i := 0; i < c.Size(); i++ {
			v, ok := views[i][slot]
			if !ok {
				t.Fatalf("node %d missing decided slot %d (view=%v)", i, slot, views[i])
			}
			if v != views[0][slot] {
				t.Fatalf("divergent decided value at slot %d: node0=%q node%d=%q", slot, views[0][slot], i, v)
			}
		}
	}
}

func TestBasicCommitAndLeaseFastPath(t *testing.T) {
	c := NewCluster(5)
	l := newLeader(c, 0, time.Minute)

	// First proposal runs Phase 1 (cold lease).
	if slot, err := l.Propose([]byte("a")); err != nil || slot != 1 {
		t.Fatalf("first propose: slot=%d err=%v", slot, err)
	}
	before, _ := c.Stats()

	// Subsequent proposals under the lease skip Phase 1: exactly one Accept
	// round trip per peer (4) plus one Commit broadcast (4) = 8 messages.
	for i := 0; i < 3; i++ {
		if _, err := l.Propose([]byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("fast-path propose %d: %v", i, err)
		}
	}
	after, _ := c.Stats()
	if got, want := after-before, int64(3*8); got != want {
		t.Fatalf("fast path used %d messages, want %d (phase 1 not skipped?)", got, want)
	}
	assertConverged(t, c, 4)
	if got := decidedView(c.Node(3))[1]; got != "a" {
		t.Fatalf("slot 1 = %q, want %q", got, "a")
	}
}

func TestLeaseExpiryForcesPhase1(t *testing.T) {
	c := NewCluster(3)
	l := newLeader(c, 0, time.Second)
	now := time.Now()
	l.SetClock(func() time.Time { return now })

	if _, err := l.Propose([]byte("x")); err != nil {
		t.Fatal(err)
	}
	before, _ := c.Stats()
	now = now.Add(2 * time.Second) // lease expires
	if _, err := l.Propose([]byte("y")); err != nil {
		t.Fatal(err)
	}
	after, _ := c.Stats()
	// Phase 1 (2 prepares) + learned-decision broadcast (2) + Phase 2
	// (2 accepts) + commit (2) = 8 messages.
	if got := after - before; got != 8 {
		t.Fatalf("expired lease should rerun phase 1: %d messages, want 8", got)
	}
	assertConverged(t, c, 2)
}

func TestHoleRepairFillsGapsWithNoop(t *testing.T) {
	c := NewCluster(5)
	l1 := newLeader(c, 0, time.Minute)

	// Leader 0 commits slot 1, then manually plants an accepted-but-
	// uncommitted value at slot 3 on a majority, leaving slot 2 a hole.
	if _, err := l1.Propose([]byte("committed-1")); err != nil {
		t.Fatal(err)
	}
	dirty := LogEntry{Slot: 3, Ballot: Ballot{N: 1, ID: 0}, Value: []byte("dirty-3")}
	for _, id := range []int{0, 1, 2} {
		if r := c.Node(id).Handle(AcceptReq{Entry: dirty}).(Accepted); !r.OK {
			t.Fatalf("planting dirty entry on node %d rejected", id)
		}
	}

	// A new leader with a higher ballot must repair: slot 2 becomes a no-op,
	// slot 3 keeps the highest-ballot accepted value.
	l2 := newLeader(c, 4, time.Minute)
	l2.ballot.N = 1
	if _, err := l2.Propose([]byte("fresh")); err != nil {
		t.Fatal(err)
	}
	assertConverged(t, c, 4)
	view := decidedView(c.Node(3))
	if view[2] != "<noop>" {
		t.Fatalf("hole slot 2 = %q, want <noop>", view[2])
	}
	if view[3] != "dirty-3" {
		t.Fatalf("slot 3 = %q, want dirty-3 (highest-ballot value preserved)", view[3])
	}
	if view[4] != "fresh" {
		t.Fatalf("slot 4 = %q, want fresh", view[4])
	}
}

// TestPartitionOldLeaderReplaced is the acceptance scenario: a 5-node
// cluster partitions 2|3, the old leader in the minority is replaced, the
// new leader completes uncommitted slots via Phase 1 and safely overwrites
// dirty minority logs after healing.
func TestPartitionOldLeaderReplaced(t *testing.T) {
	c := NewCluster(5)
	oldLeader := newLeader(c, 0, time.Minute)

	// Slots 1-3 committed cluster-wide by node 0.
	for i := 1; i <= 3; i++ {
		if _, err := oldLeader.Propose([]byte(fmt.Sprintf("stable-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	assertConverged(t, c, 3)

	// Network partition: {0,1} | {2,3,4}. Old leader sits in the minority.
	c.Partition([]int{0, 1}, []int{2, 3, 4})

	// Old leader writes slot 4 ("dirty") — accepted by the minority only,
	// so it can never commit.
	if _, err := oldLeader.Propose([]byte("dirty-4")); err != ErrNotLeader {
		t.Fatalf("minority leader propose err=%v, want ErrNotLeader", err)
	}
	for _, id := range []int{0, 1} {
		if e, ok := c.Node(id).AcceptedEntry(4); !ok || string(e.Value) != "dirty-4" {
			t.Fatalf("node %d should hold dirty accepted slot 4, got %+v ok=%v", id, e, ok)
		}
	}
	if _, ok := c.Node(0).Decided(4); ok {
		t.Fatal("minority must never commit slot 4")
	}

	// Majority side elects node 2 with a higher ballot. Phase 1 sees no
	// slot-4 value on its side and commits its own slot 4.
	newLeader := newLeader(c, 2, time.Minute)
	newLeader.ballot.N = oldLeader.Ballot().N
	if slot, err := newLeader.Propose([]byte("real-4")); err != nil || slot != 4 {
		t.Fatalf("new leader propose: slot=%d err=%v", slot, err)
	}
	if slot, err := newLeader.Propose([]byte("real-5")); err != nil || slot != 5 {
		t.Fatalf("new leader propose: slot=%d err=%v", slot, err)
	}
	for _, id := range []int{2, 3, 4} {
		if e, ok := c.Node(id).Decided(4); !ok || string(e.Value) != "real-4" {
			t.Fatalf("majority node %d slot 4 = %+v ok=%v, want real-4", id, e, ok)
		}
	}

	// Old leader is now stale: its Accepts are rejected even after healing
	// because the majority promised a higher ballot. Heal first, then let
	// the old leader retry — it must fail and step back.
	c.Heal()
	if _, err := oldLeader.Propose([]byte("zombie")); err != ErrNotLeader {
		t.Fatalf("stale leader propose err=%v, want ErrNotLeader", err)
	}

	// New leader reruns Phase 1 (lease refresh) reaching all 5 nodes. The
	// dirty slot-4 values on nodes 0,1 are overwritten by the decided
	// value, and slot 5 is replicated everywhere.
	newLeader.prepared = false
	if slot, err := newLeader.Propose([]byte("real-6")); err != nil || slot != 6 {
		t.Fatalf("post-heal propose: slot=%d err=%v", slot, err)
	}

	assertConverged(t, c, 6)
	view := decidedView(c.Node(0))
	for slot, want := range map[uint64]string{
		1: "stable-1", 2: "stable-2", 3: "stable-3",
		4: "real-4", 5: "real-5", 6: "real-6",
	} {
		if view[slot] != want {
			t.Fatalf("node 0 slot %d = %q, want %q (dirty value not safely overwritten)", slot, view[slot], want)
		}
	}
	// Dirty accepted entries must be gone from the minority nodes.
	for _, id := range []int{0, 1} {
		if _, ok := c.Node(id).AcceptedEntry(4); ok {
			t.Fatalf("node %d still holds dirty accepted slot 4", id)
		}
	}
}

func TestCommittedValueNeverOverwritten(t *testing.T) {
	c := NewCluster(5)
	l1 := newLeader(c, 0, time.Minute)
	if _, err := l1.Propose([]byte("safe")); err != nil {
		t.Fatal(err)
	}

	// A higher-ballot leader must preserve the committed value at slot 1
	// even though it re-runs Phase 1.
	l2 := newLeader(c, 3, time.Minute)
	l2.ballot.N = 5
	if _, err := l2.Propose([]byte("next")); err != nil {
		t.Fatal(err)
	}
	assertConverged(t, c, 2)
	if got := decidedView(c.Node(1))[1]; got != "safe" {
		t.Fatalf("committed slot 1 changed to %q — safety violation", got)
	}
}

func TestStaleAcceptorRejectsLowerBallot(t *testing.T) {
	c := NewCluster(3)
	n := c.Node(0)
	if p := n.Handle(PrepareReq{Ballot: Ballot{N: 5, ID: 1}}).(Promise); !p.OK {
		t.Fatal("first prepare should succeed")
	}
	if p := n.Handle(PrepareReq{Ballot: Ballot{N: 4, ID: 2}}).(Promise); p.OK {
		t.Fatal("lower ballot prepare must be rejected")
	}
	if a := n.Handle(AcceptReq{Entry: LogEntry{Slot: 1, Ballot: Ballot{N: 4, ID: 2}, Value: []byte("x")}}).(Accepted); a.OK {
		t.Fatal("accept with ballot below promise must be rejected")
	}
	if a := n.Handle(AcceptReq{Entry: LogEntry{Slot: 1, Ballot: Ballot{N: 5, ID: 1}, Value: []byte("x")}}).(Accepted); !a.OK {
		t.Fatal("accept at promised ballot must succeed")
	}
}

func TestConcurrentCommitsDeterministic(t *testing.T) {
	// Drive many sequential proposals and verify the log is hole-free and
	// identical on every node.
	c := NewCluster(5)
	l := newLeader(c, 0, time.Minute)
	const n = 200
	for i := 0; i < n; i++ {
		if _, err := l.Propose([]byte(fmt.Sprintf("entry-%d", i))); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	assertConverged(t, c, n)
	slots := c.Node(4).DecidedSlots()
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	if len(slots) != n || slots[0] != 1 || slots[n-1] != n {
		t.Fatalf("committed log not contiguous 1..%d: got %d slots", n, len(slots))
	}
	for i := 0; i < n; i++ {
		e, _ := c.Node(2).Decided(uint64(i + 1))
		if want := fmt.Sprintf("entry-%d", i); !bytes.Equal(e.Value, []byte(want)) {
			t.Fatalf("slot %d = %q, want %q", i+1, e.Value, want)
		}
	}
}
