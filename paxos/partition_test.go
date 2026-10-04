package paxos

import (
	"testing"
	"time"
)

// TestPartitionLeaderReplacement is the acceptance scenario:
//
//  1. n1 leads a 5-node cluster and commits slots 1-2.
//  2. A partial cut leaves n1 able to write a dirty (uncommitted) entry
//     at slot 3 on the minority {n1, n2} only.
//  3. The partition hardens: {n1, n2} vs {n3, n4, n5}. The old leader
//     cannot commit anything; n3 wins Phase 1 in the majority, repairs
//     the log and commits new entries.
//  4. After healing, n3 re-runs Phase 1: the committed prefix is
//     preserved and the minority's dirty slot 3 is safely overwritten
//     everywhere.
func TestPartitionLeaderReplacement(t *testing.T) {
	net, nodes := newCluster(5, WithLeaseDuration(40*time.Millisecond))
	n1, n2, n3 := nodes[0], nodes[1], nodes[2]
	if _, err := n1.Propose("v1"); err != nil {
		t.Fatalf("Propose v1: %v", err)
	}
	if _, err := n1.Propose("v2"); err != nil {
		t.Fatalf("Propose v2: %v", err)
	}
	// Partial cut: n1 can only reach n2, so its next accept lands on a
	// minority and can never commit.
	net.Cut("n1", "n3")
	net.Cut("n1", "n4")
	net.Cut("n1", "n5")
	if _, err := n1.Propose("dirty"); err == nil {
		t.Fatal("minority write must fail")
	}
	for _, n := range []*Node{n1, n2} {
		e, ok := n.LogEntry(3)
		if !ok || e.Value != "dirty" || e.Committed {
			t.Fatalf("%s should hold uncommitted dirty entry, got %+v (ok=%v)", n.ID(), e, ok)
		}
	}
	// Harden the partition: {n1, n2} | {n3, n4, n5}.
	net.Heal()
	net.Isolate("n1")
	net.Isolate("n2")
	// The old leader is fenced by the majority: it cannot commit.
	if _, err := n1.Propose("stale"); err == nil {
		t.Fatal("isolated old leader must not commit")
	}
	// n3 becomes the new leader in the majority partition, repairs the
	// log and commits a new value at slot 3, superseding the dirty one.
	slot, err := n3.Propose("v3")
	if err != nil {
		t.Fatalf("new leader Propose: %v", err)
	}
	if slot != 3 {
		t.Fatalf("slot = %d, want 3", slot)
	}
	if got := n3.CommitIndex(); got != 3 {
		t.Fatalf("new leader commitIndex = %d, want 3", got)
	}
	for _, n := range nodes[2:] {
		e, _ := n.LogEntry(3)
		if e.Value != "v3" || !e.Committed {
			t.Fatalf("majority node %s slot 3 = %+v, want committed v3", n.ID(), e)
		}
	}
	// Heal and force a fresh Phase 1 so the minority replicas converge.
	net.Heal()
	time.Sleep(80 * time.Millisecond) // let any stale lease expire
	if _, err := n3.Propose("v4"); err != nil {
		t.Fatalf("post-heal Propose: %v", err)
	}
	// Final state on every node: contiguous committed prefix v1..v4; the
	// dirty minority value is gone; committed values were never lost.
	want := []string{"v1", "v2", "v3", "v4"}
	for _, n := range nodes {
		if got := n.CommitIndex(); got != 4 {
			t.Fatalf("%s commitIndex = %d, want 4", n.ID(), got)
		}
		for slot := 1; slot <= 4; slot++ {
			e, ok := n.LogEntry(slot)
			if !ok || !e.Committed || e.Value != want[slot-1] {
				t.Fatalf("%s slot %d = %+v (ok=%v), want committed %q",
					n.ID(), slot, e, ok, want[slot-1])
			}
		}
	}
}

// TestCommittedValueSurvivesLeaderChange checks the core safety property
// directly: once a value is committed, every later leader must re-propose
// exactly that value for the slot.
func TestCommittedValueSurvivesLeaderChange(t *testing.T) {
	net, nodes := newCluster(5, WithLeaseDuration(20*time.Millisecond))
	if _, err := nodes[0].Propose("committed"); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	// Rotate leadership through every node; each must preserve slot 1.
	for i := 1; i < len(nodes); i++ {
		net.Isolate(nodes[i-1].ID())
		time.Sleep(30 * time.Millisecond)
		if _, err := nodes[i].Propose("x"); err != nil {
			net.Heal()
			t.Fatalf("node %s Propose: %v", nodes[i].ID(), err)
		}
		net.Heal()
		e, _ := nodes[i].LogEntry(1)
		if e.Value != "committed" || !e.Committed {
			t.Fatalf("after leader %s: slot 1 = %+v, want committed %q",
				nodes[i].ID(), e, "committed")
		}
	}
}
