package statedb

import "testing"

// chain builds a store and returns the deltas that produced it.
func chain(t *testing.T, batches [][]Op) (*Store, []Delta) {
	t.Helper()
	s := NewStore()
	var deltas []Delta
	for _, ops := range batches {
		d := Delta{
			Seq:        s.Seq() + 1,
			BaseDigest: s.Head(),
			Ops:        ops,
		}
		next := s.State()
		for _, op := range ops {
			next.apply(op)
		}
		d.Digest = DigestOf(next)
		if err := s.Apply(d); err != nil {
			t.Fatalf("apply seq %d: %v", d.Seq, err)
		}
		deltas = append(deltas, d)
	}
	return s, deltas
}

func TestApplyAndReapplyIdempotent(t *testing.T) {
	s, deltas := chain(t, [][]Op{
		{{Kind: OpSet, Key: "a", Val: 1}},
		{{Kind: OpAdd, Key: "a", Val: 2}},
	})
	head, seq := s.Head(), s.Seq()
	for i := 0; i < 3; i++ {
		if err := s.Apply(deltas[0]); err != nil {
			t.Fatalf("re-apply delta 1 must be idempotent: %v", err)
		}
		if err := s.Apply(deltas[1]); err != nil {
			t.Fatalf("re-apply delta 2 must be idempotent: %v", err)
		}
	}
	if s.Head() != head || s.Seq() != seq {
		t.Fatalf("idempotent replay changed state: head %s->%s seq %d->%d",
			head, s.Head(), seq, s.Seq())
	}
	if v, _ := s.State().Get("a"); v != 3 {
		t.Fatalf("a = %d, want 3", v)
	}
}

func TestGapRejected(t *testing.T) {
	_, deltas := chain(t, [][]Op{
		{{Kind: OpSet, Key: "a", Val: 1}},
		{{Kind: OpSet, Key: "b", Val: 2}},
	})
	s := NewStore()
	if err := s.Apply(deltas[1]); KindOf(err) != ErrGap {
		t.Fatalf("skipping delta 1: got %v, want ErrGap", err)
	}
	if s.Seq() != 0 {
		t.Fatalf("rejected delta mutated store, seq = %d", s.Seq())
	}
}

func TestCorruptPostDigestRejectedAtomically(t *testing.T) {
	_, deltas := chain(t, [][]Op{
		{{Kind: OpSet, Key: "a", Val: 1}},
		{{Kind: OpSet, Key: "b", Val: 2}},
	})
	s := NewStore()
	if err := s.Apply(deltas[0]); err != nil {
		t.Fatal(err)
	}
	bad := deltas[1]
	bad.Digest[0] ^= 0xff
	head := s.Head()
	if err := s.Apply(bad); KindOf(err) != ErrDigestMismatch {
		t.Fatalf("corrupt digest: got %v, want ErrDigestMismatch", err)
	}
	if s.Head() != head || s.Seq() != 1 {
		t.Fatalf("corrupt delta not atomic: head %s seq %d", s.Head(), s.Seq())
	}
	if _, ok := s.State().Get("b"); ok {
		t.Fatal("corrupt delta leaked ops into state")
	}
}

func TestCorruptBaseDigestRejected(t *testing.T) {
	_, deltas := chain(t, [][]Op{
		{{Kind: OpSet, Key: "a", Val: 1}},
		{{Kind: OpSet, Key: "b", Val: 2}},
	})
	s := NewStore()
	if err := s.Apply(deltas[0]); err != nil {
		t.Fatal(err)
	}
	bad := deltas[1]
	bad.BaseDigest[0] ^= 0xff
	if err := s.Apply(bad); KindOf(err) != ErrBaseMismatch {
		t.Fatalf("corrupt base: got %v, want ErrBaseMismatch", err)
	}
}

func TestBranchCannotAttachToWrongBaseline(t *testing.T) {
	base, _ := chain(t, [][]Op{
		{{Kind: OpSet, Key: "x", Val: 10}},
	})
	// Fork A and B from the same baseline.
	_, branchA := chainFrom(t, base, [][]Op{{{Kind: OpSet, Key: "a", Val: 1}}})
	_, branchB := chainFrom(t, base, [][]Op{{{Kind: OpSet, Key: "b", Val: 2}}})

	// B's delta at seq 2 replayed onto the A store: same seq, different
	// content -> conflict, not silent acceptance.
	if err := applyTo(t, base, branchA); err != nil {
		t.Fatal(err)
	}
	if err := base.Apply(branchB[0]); KindOf(err) != ErrConflict {
		t.Fatalf("branch delta on wrong baseline: got %v, want ErrConflict", err)
	}
	if v, ok := base.State().Get("a"); !ok || v != 1 {
		t.Fatalf("store corrupted by foreign branch delta: a=%d ok=%v", v, ok)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	src, deltas := chain(t, [][]Op{
		{{Kind: OpSet, Key: "a", Val: 1}},
		{{Kind: OpAdd, Key: "a", Val: 5}},
		{{Kind: OpDel, Key: "a"}},
		{{Kind: OpSet, Key: "z", Val: 9}},
	})
	// Snapshot after delta 2, restore with the remaining deltas.
	tmp := NewStore()
	tmp.Apply(deltas[0])
	tmp.Apply(deltas[1])
	snap := tmp.Snapshot()

	got, err := Restore(snap, deltas[2:])
	if err != nil {
		t.Fatal(err)
	}
	if got.Head() != src.Head() || got.Seq() != src.Seq() {
		t.Fatalf("restored head %s seq %d, want %s seq %d",
			got.Head(), got.Seq(), src.Head(), src.Seq())
	}
}

func TestRestoreRejectsCorruptSnapshot(t *testing.T) {
	_, deltas := chain(t, [][]Op{{{Kind: OpSet, Key: "a", Val: 1}}})
	snap := NewStore().Snapshot()
	snap.Digest[0] ^= 0xff
	if _, err := Restore(snap, deltas); KindOf(err) != ErrBadSnapshot {
		t.Fatalf("corrupt snapshot: got %v, want ErrBadSnapshot", err)
	}
}

func TestRestoreAtomicOnMidChainFailure(t *testing.T) {
	_, deltas := chain(t, [][]Op{
		{{Kind: OpSet, Key: "a", Val: 1}},
		{{Kind: OpSet, Key: "b", Val: 2}},
		{{Kind: OpSet, Key: "c", Val: 3}},
	})
	bad := deltas[1]
	bad.Digest[0] ^= 0xff
	s, err := Restore(NewStore().Snapshot(), []Delta{deltas[0], bad, deltas[2]})
	if KindOf(err) != ErrDigestMismatch {
		t.Fatalf("got %v, want ErrDigestMismatch", err)
	}
	if s != nil {
		t.Fatal("failed restore returned a partially-restored store")
	}
}

func chainFrom(t *testing.T, base *Store, batches [][]Op) (*Store, []Delta) {
	t.Helper()
	s := &Store{
		seq:     base.Seq(),
		head:    base.Head(),
		state:   base.State(),
		applied: make(map[uint64]Digest),
	}
	var deltas []Delta
	for _, ops := range batches {
		d := Delta{Seq: s.Seq() + 1, BaseDigest: s.Head(), Ops: ops}
		next := s.State()
		for _, op := range ops {
			next.apply(op)
		}
		d.Digest = DigestOf(next)
		if err := s.Apply(d); err != nil {
			t.Fatalf("apply seq %d: %v", d.Seq, err)
		}
		deltas = append(deltas, d)
	}
	return s, deltas
}

func applyTo(t *testing.T, s *Store, deltas []Delta) error {
	t.Helper()
	for _, d := range deltas {
		if err := s.Apply(d); err != nil {
			return err
		}
	}
	return nil
}
