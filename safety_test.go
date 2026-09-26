package syncsim

import (
	"fmt"
	"testing"
)

// SAFETY: the conflict rule is explicit, total and deterministic.
// Concurrent writes to the same key at the same logical clock are
// ordered by node ID, and every node resolves them identically.
func TestExplicitConflictRule(t *testing.T) {
	s := NewSim(1, nil)
	nodes := addNodes(s, "A", "B", "C")

	// A and B write the same key concurrently: both get clock=1.
	nodes[0].Store.Put("k", "from-A")
	nodes[1].Store.Put("k", "from-B")
	// C deletes a key it never saw: tombstone with clock=1.
	nodes[2].Store.Delete("k")

	// Explicit rule: higher (Clock, Node) wins -> all clock=1, so the
	// highest node ID ("C", a tombstone) must win everywhere.
	if _, ok := runUntilFixpoint(s, nodes, 10); !ok {
		t.Fatal("did not converge")
	}
	mustConverge(t, nodes)
	rec, ok := nodes[0].Store.Get("k")
	if !ok || !rec.Tombstone || rec.Ver.Node != "C" {
		t.Fatalf("explicit rule violated: got %+v", rec)
	}
}

// SAFETY: a delete propagates as a tombstone and an older value can
// never resurrect the key — not via sync, and not via a stale delayed
// message.
func TestDeletePropagatesAndNeverResurrects(t *testing.T) {
	s := NewSim(2, nil)
	nodes := addNodes(s, "A", "B")
	a, b := nodes[0], nodes[1]

	a.Store.Put("k", "v1")
	syncRound(s, nodes)
	if v, ok := b.Store.LiveValue("k"); !ok || v != "v1" {
		t.Fatalf("setup: B missing value: %q %v", v, ok)
	}

	// A deletes k; the tombstone must propagate.
	a.Store.Delete("k")
	syncRound(s, nodes)
	if _, ok := b.Store.LiveValue("k"); ok {
		t.Fatal("delete did not propagate to B")
	}
	if _, ok := a.Store.LiveValue("k"); ok {
		t.Fatal("delete lost on A")
	}

	// A stale copy of the old value arrives late (e.g. a delayed or
	// duplicated pre-delete message). It must lose to the tombstone.
	stale := Record{Key: "k", Value: "v1", Ver: Version{Clock: 1, Node: "A"}}
	if b.Store.Apply(stale) {
		t.Fatal("stale value resurrected the key on B")
	}
	if _, ok := b.Store.LiveValue("k"); ok {
		t.Fatal("key resurrected on B")
	}

	// Reverse-direction sync must not resurrect it on A either.
	syncRound(s, nodes)
	if _, ok := a.Store.LiveValue("k"); ok {
		t.Fatal("key resurrected on A via reverse sync")
	}
	mustConverge(t, nodes)
}

// SAFETY: identical subtrees are never transmitted in full. With 5000
// identical records and 1 differing key, only the differing leaf may
// cross the wire.
func TestIdenticalSubtreeNotTransmitted(t *testing.T) {
	s := NewSim(3, nil)
	nodes := addNodes(s, "A", "B")
	a, b := nodes[0], nodes[1]

	const shared = 5000
	for i := 0; i < shared; i++ {
		r := a.Store.Put(fmt.Sprintf("key-%05d", i), "shared")
		b.Store.Apply(r) // identical state, identical versions
	}
	a.Store.Put("fresh-key", "only-on-A")

	// Reset counters so we measure only this sync.
	for _, n := range nodes {
		n.Stats = Stats{}
	}
	s.Sent = 0

	syncRound(s, nodes)
	mustConverge(t, nodes)

	if got := totalRecordsSent(nodes); got > 4 {
		t.Fatalf("transmitted %d records for 1 differing key out of %d", got, shared+1)
	}
	t.Logf("records transmitted: %d (full state would be %d), messages: %d",
		totalRecordsSent(nodes), shared+1, s.Sent)
}

// SAFETY: a forced summary collision must not silently hide divergence —
// the audit review path detects and repairs it.
func TestForcedCollisionReviewPath(t *testing.T) {
	// Weak digest hash: subtree digest = byte length of the encoding.
	// Two different single-record leaves of equal encoding length
	// collide, and so do the roots built above them.
	weak := func(b []byte) uint64 { return uint64(len(b)) }

	build := func(auditRate float64) (*Sim, []*Node) {
		s := NewSim(4, weak)
		nodes := addNodes(s, "A", "B")
		// Same key, same encoding length, different content/version.
		nodes[0].Store.Put("k", "alpha") // ver (1,"A")
		nodes[1].Store.Put("k", "zzzzz") // ver (1,"B")
		nodes[1].Store.Put("k", "gamma") // ver (2,"B") wins by rule
		for _, n := range nodes {
			n.AuditRate = auditRate
		}
		return s, nodes
	}

	// Without the review path the collision hides the divergence:
	// roots match, nothing is transmitted, nodes stay diverged.
	s1, n1 := build(0)
	rootA := n1[0].summary().Digest(0, 0)
	rootB := n1[1].summary().Digest(0, 0)
	if rootA != rootB {
		t.Fatalf("test setup: expected forced collision, roots %x vs %x", rootA, rootB)
	}
	syncRound(s1, n1)
	if allConverged(n1) {
		t.Fatal("expected divergence to persist without the review path")
	}
	if totalRecordsSent(n1) != 0 {
		t.Fatal("collision should have suppressed all record traffic")
	}

	// With the review path every matched digest is re-checked against
	// the real leaves; the explicit rule then repairs the divergence.
	s2, n2 := build(1.0)
	syncRound(s2, n2)
	mustConverge(t, n2)
	if v, ok := n2[0].Store.LiveValue("k"); !ok || v != "gamma" {
		t.Fatalf("review path converged to wrong value: %q %v", v, ok)
	}
}

// SAFETY: tombstones participate in digests, so a node that only has a
// tombstone and a node that has nothing still detect their difference.
func TestTombstoneVisibleToSummary(t *testing.T) {
	s := NewSim(5, nil)
	nodes := addNodes(s, "A", "B")
	a := nodes[0]

	a.Store.Put("gone", "x")
	a.Store.Delete("gone") // A now holds only a tombstone; B is empty.

	syncRound(s, nodes)
	mustConverge(t, nodes)
	rec, ok := nodes[1].Store.Get("gone")
	if !ok || !rec.Tombstone {
		t.Fatalf("tombstone did not replicate to empty node: %+v ok=%v", rec, ok)
	}
}
