package statestore

import "testing"

func mustApply(t *testing.T, s *Store, d Delta) {
	t.Helper()
	applied, err := s.Apply(d)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if !applied {
		t.Fatalf("Apply: expected applied=true")
	}
}

func buildChain(t *testing.T, s *Store, opsPerDelta [][]Op) []Delta {
	t.Helper()
	deltas := make([]Delta, 0, len(opsPerDelta))
	for _, ops := range opsPerDelta {
		d := s.NewDelta(ops)
		mustApply(t, s, d)
		deltas = append(deltas, d)
	}
	return deltas
}

func TestSnapshotPlusDeltaRecovery(t *testing.T) {
	src := NewStore()
	ops := [][]Op{
		{{Kind: OpSet, Key: "a", Value: 1}},
		{{Kind: OpAdd, Key: "a", Value: 2}, {Kind: OpSet, Key: "b", Value: 7}},
		{{Kind: OpDel, Key: "b"}},
	}
	deltas := buildChain(t, src, ops)

	// Snapshot after the first delta, recover with the remaining deltas.
	snap := Snapshot{Seq: 1, Digest: src.history[1], State: map[string]int64{"a": 1}}
	dst := NewStore()
	if err := dst.Restore(snap, deltas[1:]); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if dst.Seq() != src.Seq() || dst.Digest() != src.Digest() {
		t.Fatalf("recovered head mismatch: seq=%d digest=%x, want seq=%d digest=%x",
			dst.Seq(), dst.Digest(), src.Seq(), src.Digest())
	}
	if v, ok := dst.Get("a"); !ok || v != 3 {
		t.Fatalf("a=%d,%v want 3,true", v, ok)
	}
	if _, ok := dst.Get("b"); ok {
		t.Fatalf("b should be deleted")
	}
}

func TestReapplyIsIdempotent(t *testing.T) {
	s := NewStore()
	deltas := buildChain(t, s, [][]Op{
		{{Kind: OpSet, Key: "x", Value: 1}},
		{{Kind: OpAdd, Key: "x", Value: 10}},
	})
	head := s.Digest()
	for _, d := range deltas {
		applied, err := s.Apply(d)
		if err != nil {
			t.Fatalf("reapply seq=%d: unexpected error: %v", d.Seq, err)
		}
		if applied {
			t.Fatalf("reapply seq=%d: expected applied=false", d.Seq)
		}
	}
	if s.Digest() != head || s.Seq() != 2 {
		t.Fatalf("idempotent replay mutated the store")
	}
}

func TestBrokenChainRejected(t *testing.T) {
	s := NewStore()
	deltas := buildChain(t, s, [][]Op{
		{{Kind: OpSet, Key: "x", Value: 1}},
		{{Kind: OpSet, Key: "y", Value: 2}},
		{{Kind: OpSet, Key: "z", Value: 3}},
	})
	// Gap: delta 2 without delta... build a fresh store at seq 1 and
	// try to attach delta for seq 2.
	fresh := NewStore()
	mustApply(t, fresh, deltas[0])
	if _, err := fresh.Apply(deltas[2]); err != ErrChainBroken {
		t.Fatalf("gap: got %v, want ErrChainBroken", err)
	}
	// Wrong pre-digest.
	bad := fresh.NewDelta([]Op{{Kind: OpSet, Key: "w", Value: 9}})
	bad.PreDigest[0] ^= 0xff
	if _, err := fresh.Apply(bad); err != ErrChainBroken {
		t.Fatalf("bad pre-digest: got %v, want ErrChainBroken", err)
	}
	// Conflicting delta for an already committed position.
	conflict := deltas[0]
	conflict.PostDigest[0] ^= 0xff
	if _, err := s.Apply(conflict); err != ErrChainBroken {
		t.Fatalf("conflict: got %v, want ErrChainBroken", err)
	}
	if s.Seq() != 3 {
		t.Fatalf("rejections mutated the store: seq=%d", s.Seq())
	}
}

func TestCorruptedPostDigestRejected(t *testing.T) {
	s := NewStore()
	mustApply(t, s, s.NewDelta([]Op{{Kind: OpSet, Key: "x", Value: 1}}))
	head := s.Digest()
	d := s.NewDelta([]Op{{Kind: OpSet, Key: "x", Value: 2}})
	d.PostDigest[3] ^= 0x01
	if _, err := s.Apply(d); err != ErrDigestMismatch {
		t.Fatalf("got %v, want ErrDigestMismatch", err)
	}
	if s.Digest() != head || s.Seq() != 1 {
		t.Fatalf("corrupt delta mutated the store")
	}
	// Tampered ops must also fail the post-digest check.
	d2 := s.NewDelta([]Op{{Kind: OpSet, Key: "x", Value: 2}})
	d2.Ops[0].Value = 3
	if _, err := s.Apply(d2); err != ErrDigestMismatch {
		t.Fatalf("tampered ops: got %v, want ErrDigestMismatch", err)
	}
}

func TestBranchCannotAttachToWrongBaseline(t *testing.T) {
	base := NewStore()
	d0 := base.NewDelta([]Op{{Kind: OpSet, Key: "root", Value: 1}})
	mustApply(t, base, d0)

	// Two branches fork from the same baseline.
	branchA := NewStore()
	mustApply(t, branchA, d0)
	branchB := NewStore()
	mustApply(t, branchB, d0)

	da := branchA.NewDelta([]Op{{Kind: OpSet, Key: "a", Value: 100}})
	db := branchB.NewDelta([]Op{{Kind: OpSet, Key: "b", Value: 200}})
	mustApply(t, branchA, da)
	mustApply(t, branchB, db)

	// Branch B's next delta must not attach to branch A's head.
	db2 := branchB.NewDelta([]Op{{Kind: OpAdd, Key: "b", Value: 1}})
	if _, err := branchA.Apply(db2); err != ErrChainBroken {
		t.Fatalf("cross-branch attach: got %v, want ErrChainBroken", err)
	}
	if branchA.Seq() != 2 {
		t.Fatalf("branch A mutated by foreign delta")
	}
	if v, _ := branchA.Get("b"); v != 0 {
		t.Fatalf("branch A leaked branch B state")
	}
}

func TestRestoreIsAtomic(t *testing.T) {
	src := NewStore()
	deltas := buildChain(t, src, [][]Op{
		{{Kind: OpSet, Key: "a", Value: 1}},
		{{Kind: OpSet, Key: "b", Value: 2}},
		{{Kind: OpSet, Key: "c", Value: 3}},
	})
	snap := Snapshot{Seq: 0, Digest: src.history[0], State: map[string]int64{}}

	// Corrupt the middle delta.
	bad := make([]Delta, len(deltas))
	copy(bad, deltas)
	bad[1].PostDigest[0] ^= 0xff

	victim := NewStore()
	mustApply(t, victim, victim.NewDelta([]Op{{Kind: OpSet, Key: "keep", Value: 42}}))
	before := victim.Snapshot()

	if err := victim.Restore(snap, bad); err != ErrDigestMismatch {
		t.Fatalf("got %v, want ErrDigestMismatch", err)
	}
	after := victim.Snapshot()
	if after.Seq != before.Seq || after.Digest != before.Digest {
		t.Fatalf("failed Restore mutated the store")
	}
	if v, ok := victim.Get("keep"); !ok || v != 42 {
		t.Fatalf("store content lost after failed Restore")
	}

	// Broken chain inside Restore is also rejected atomically.
	if err := victim.Restore(snap, deltas[1:]); err != ErrChainBroken {
		t.Fatalf("gap restore: got %v, want ErrChainBroken", err)
	}
	if victim.Snapshot().Digest != before.Digest {
		t.Fatalf("failed Restore mutated the store")
	}
}

func TestBadSnapshotRejected(t *testing.T) {
	snap := Snapshot{Seq: 0, State: map[string]int64{"a": 1}}
	snap.Digest = digestOf(0, map[string]int64{"a": 2}) // digest of a different state
	s := NewStore()
	if err := s.Restore(snap, nil); err != ErrBadSnapshot {
		t.Fatalf("got %v, want ErrBadSnapshot", err)
	}
	if s.Seq() != 0 {
		t.Fatalf("bad snapshot mutated the store")
	}
}
