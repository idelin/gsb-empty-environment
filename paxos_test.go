package paxos

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

const testLease = 200 * time.Millisecond

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v: %s", timeout, msg)
}

// sameLog reports whether two applied logs are identical (value + noop flag).
func sameLog(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Value != b[i].Value || a[i].Noop != b[i].Noop {
			return false
		}
	}
	return true
}

func logsConverged(net *Network) bool {
	ref := net.Node(0).Applied()
	for i := 1; i < net.Size(); i++ {
		if !sameLog(ref, net.Node(i).Applied()) {
			return false
		}
	}
	return true
}

func expectLog(t *testing.T, net *Network, want []Entry) {
	t.Helper()
	for i := 0; i < net.Size(); i++ {
		got := net.Node(i).Applied()
		if !sameLog(got, want) {
			t.Fatalf("node %d log mismatch:\n got  %v\n want %v", i, got, want)
		}
	}
}

// --- Tests ---------------------------------------------------------------

// A single proposer commits a contiguous log; after the first campaign the
// lease fast path commits subsequent entries without re-running Phase 1.
func TestLeaseFastPath(t *testing.T) {
	net := NewNetwork(5, testLease)
	n0 := net.Node(0)

	for _, v := range []string{"a", "b", "c"} {
		if _, err := n0.Propose(v); err != nil {
			t.Fatalf("propose %q: %v", v, err)
		}
	}
	if got := n0.Campaigns(); got != 1 {
		t.Fatalf("expected exactly 1 Phase 1 campaign (lease bypass), got %d", got)
	}
	waitFor(t, 2*time.Second, func() bool { return logsConverged(net) }, "logs converge")
	expectLog(t, net, []Entry{
		{Index: 1, Value: "a"},
		{Index: 2, Value: "b"},
		{Index: 3, Value: "c"},
	})
}

// Five nodes propose concurrently; the arbiter must guarantee that exactly
// one value is chosen per slot and that all nodes converge to the same log.
func TestConcurrentProposers(t *testing.T) {
	net := NewNetwork(5, 150*time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < net.Size(); i++ {
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func(i, j int) {
				defer wg.Done()
				v := fmt.Sprintf("v-%d-%d", i, j)
				// Retry until committed: contention may cost a campaign.
				for k := 0; k < 250; k++ {
					if _, err := net.Node(i).Propose(v); err == nil {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Errorf("value %q never committed", v)
			}(i, j)
		}
	}
	wg.Wait()

	// Force one final convergence round after leases settle.
	time.Sleep(2 * testLease)
	if _, err := net.Node(4).Propose("final"); err != nil {
		t.Fatalf("final propose: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool { return logsConverged(net) }, "logs converge")

	log := net.Node(0).Applied()
	if len(log) != 21 { // 20 client values + "final"
		t.Fatalf("expected 21 committed entries, got %d", len(log))
	}
	seen := make(map[string]bool)
	for _, e := range log {
		if e.Noop {
			t.Fatalf("unexpected noop at slot %d in fully connected run", e.Index)
		}
		if seen[e.Value] {
			t.Fatalf("value %q committed twice", e.Value)
		}
		seen[e.Value] = true
	}
	for i := 0; i < net.Size(); i++ {
		for j := 0; j < 4; j++ {
			if !seen[fmt.Sprintf("v-%d-%d", i, j)] {
				t.Fatalf("value v-%d-%d lost", i, j)
			}
		}
	}
}

// Acceptance boundary: 5-node cluster, network partition isolates the old
// leader with a minority; a new leader elected by the majority completes
// Phase 1, fills uncommitted slots, and safely overwrites dirty minority
// logs after healing.
func TestPartitionOldLeaderReplaced(t *testing.T) {
	net := NewNetwork(5, testLease)
	n0 := net.Node(0)

	if _, err := n0.Propose("x1"); err != nil {
		t.Fatalf("propose x1: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return logsConverged(net) }, "x1 committed")

	// Plant a dirty, uncommitted entry on the future minority {0,1} only.
	dirty := Entry{Index: 2, Ballot: n0.LeaderBallot(), Value: "dirty"}
	net.Node(0).handleAccept(AcceptRequest{Ballot: dirty.Ballot, Entries: []Entry{dirty}})
	net.Node(1).handleAccept(AcceptRequest{Ballot: dirty.Ballot, Entries: []Entry{dirty}})

	// Partition: {0,1} | {2,3,4}.
	net.Partition([]int{0, 1}, []int{2, 3, 4})

	// The old leader cannot commit anything through the partition.
	if _, err := n0.Propose("x-old"); err == nil {
		t.Fatalf("old leader committed through a minority partition")
	}

	// After the lease expires the majority side elects a new leader.
	time.Sleep(2 * testLease)
	n2 := net.Node(2)
	if _, err := n2.Propose("x2"); err != nil {
		t.Fatalf("new leader propose x2: %v", err)
	}

	// Majority side never saw the dirty slot-2 entry, so x2 takes slot 2
	// directly; the minority's dirty slot 2 is overwritten after healing.
	want := []Entry{
		{Index: 1, Value: "x1"},
		{Index: 2, Value: "x2"},
	}
	for _, id := range []int{2, 3, 4} {
		got := net.Node(id).Applied()
		if !sameLog(got, want) {
			t.Fatalf("majority node %d log:\n got  %v\n want %v", id, got, want)
		}
	}
	// Minority side is stuck at slot 1 (dirty entry must not be committed).
	for _, id := range []int{0, 1} {
		if got := net.Node(id).Applied(); len(got) != 1 {
			t.Fatalf("minority node %d applied %d entries, want 1", id, len(got))
		}
	}

	// Heal: the old leader's stale state must be repaired, not win.
	net.HealAll()
	// The majority may still hold node2's lease briefly; retry until the
	// old leader can campaign again.
	committed := false
	for k := 0; k < 100; k++ {
		if _, err := n0.Propose("x3"); err == nil {
			committed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !committed {
		t.Fatalf("old leader never recommitted after heal")
	}
	waitFor(t, 3*time.Second, func() bool { return logsConverged(net) }, "logs converge after heal")
	expectLog(t, net, []Entry{
		{Index: 1, Value: "x1"},
		{Index: 2, Value: "x2"},
		{Index: 3, Value: "x3"},
	})
	for i := 0; i < net.Size(); i++ {
		for _, e := range net.Node(i).Applied() {
			if e.Value == "dirty" {
				t.Fatalf("node %d committed dirty minority value", i)
			}
		}
	}
}

// A new leader must fill holes (never-chosen slots) with no-ops and adopt
// the highest-ballot accepted value for chosen-but-uncommitted slots.
func TestHoleRepair(t *testing.T) {
	net := NewNetwork(5, testLease)
	n0 := net.Node(0)

	if _, err := n0.Propose("a"); err != nil { // slot 1, committed
		t.Fatalf("propose a: %v", err)
	}
	// Accept "c" at slot 3 on a majority, leaving slot 2 as a hole and
	// slot 3 uncommitted.
	b := n0.LeaderBallot()
	e3 := Entry{Index: 3, Ballot: b, Value: "c"}
	for i := 0; i < 3; i++ {
		net.Node(i).handleAccept(AcceptRequest{Ballot: b, Entries: []Entry{e3}})
	}

	time.Sleep(2 * testLease) // let n0's lease expire
	if _, err := net.Node(3).Propose("d"); err != nil {
		t.Fatalf("new leader propose d: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool { return logsConverged(net) }, "logs converge")
	expectLog(t, net, []Entry{
		{Index: 1, Value: "a"},
		{Index: 2, Noop: true},
		{Index: 3, Value: "c"},
		{Index: 4, Value: "d"},
	})
}

// A stale leader's Accept must be rejected once a majority has promised a
// higher ballot; committed entries are never overwritten.
func TestStaleBallotRejectedAndCommitImmutable(t *testing.T) {
	net := NewNetwork(5, testLease)
	n0 := net.Node(0)

	if _, err := n0.Propose("a"); err != nil {
		t.Fatalf("propose a: %v", err)
	}
	stale := n0.LeaderBallot()

	time.Sleep(2 * testLease)
	n1 := net.Node(1)
	if _, err := n1.Propose("b"); err != nil {
		t.Fatalf("propose b: %v", err)
	}

	// Stale Accept against a node that promised higher must fail.
	resp := net.Node(2).handleAccept(AcceptRequest{
		Ballot:  stale,
		Entries: []Entry{{Index: 3, Ballot: stale, Value: "stale"}},
	})
	if resp.OK {
		t.Fatalf("stale ballot accepted")
	}

	// Attempt to overwrite committed slot 1 with a fresh ballot: the
	// committed value must survive.
	fresh := n1.LeaderBallot()
	net.Node(2).handleAccept(AcceptRequest{
		Ballot:  fresh,
		Entries: []Entry{{Index: 1, Ballot: fresh, Value: "evil"}},
	})
	got := net.Node(2).Applied()
	if len(got) < 1 || got[0].Value != "a" {
		t.Fatalf("committed slot 1 overwritten: %v", got)
	}
}

// Two nodes campaign at the same time; exactly one value can be chosen per
// slot and both values must eventually appear in the committed log.
func TestDuelingLeaders(t *testing.T) {
	net := NewNetwork(5, 120*time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 30; k++ {
				if _, err := net.Node(i).Propose(fmt.Sprintf("w-%d", i)); err == nil {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Errorf("node %d never committed", i)
		}(i)
	}
	wg.Wait()

	time.Sleep(2 * testLease)
	if _, err := net.Node(4).Propose("seal"); err != nil {
		t.Fatalf("seal: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool { return logsConverged(net) }, "logs converge")

	// Per-slot uniqueness is implied by convergence; also verify all three
	// contenders' values are present exactly once.
	count := map[string]int{}
	for _, e := range net.Node(0).Applied() {
		count[e.Value]++
	}
	for _, v := range []string{"w-0", "w-1", "w-2", "seal"} {
		if count[v] != 1 {
			t.Fatalf("value %q committed %d times, want 1", v, count[v])
		}
	}
}

// The implementation spawns no background goroutines: after the cluster
// goes idle, the goroutine count must return to baseline.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	net := NewNetwork(5, 100*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < net.Size(); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				if _, err := net.Node(i).Propose(fmt.Sprintf("m-%d", i)); err == nil {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
	waitFor(t, 3*time.Second, func() bool { return logsConverged(net) }, "logs converge")

	// Allow any in-flight broadcast to finish, then compare.
	time.Sleep(200 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
