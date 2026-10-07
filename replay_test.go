package statedb

import (
	"math/rand"
	"testing"
)

// TestRandomReplay drives the store with a random sequence of commits,
// idempotent re-applications, corruptions, gaps, forks and restores, and
// after every step checks the store against a plain map model.
func TestRandomReplay(t *testing.T) {
	seeds := []int64{1, 7, 42, 1337, 20261007}
	for _, seed := range seeds {
		t.Run("", func(t *testing.T) {
			runReplay(t, rand.New(rand.NewSource(seed)), 600)
		})
	}
}

type harness struct {
	t      *testing.T
	r      *rand.Rand
	store  *Store
	model  map[string]int64
	log    []Delta    // every delta committed to store, in order
	snaps  []Snapshot // snapshots taken along the way
	branch []Delta    // deltas of a fork, when one exists
	forkAt uint64     // seq where the fork diverged
}

func runReplay(t *testing.T, r *rand.Rand, steps int) {
	h := &harness{
		t:     t,
		r:     r,
		store: NewStore(),
		model: make(map[string]int64),
	}
	h.snaps = append(h.snaps, h.store.Snapshot())
	for i := 0; i < steps; i++ {
		switch h.r.Intn(8) {
		case 0, 1, 2:
			h.commit()
		case 3:
			h.reapply()
		case 4:
			h.corrupt()
		case 5:
			h.gap()
		case 6:
			h.fork()
		case 7:
			h.restore()
		}
		h.check()
	}
}

// check verifies the store matches the model after every action.
func (h *harness) check() {
	st := h.store.State()
	if st.Len() != len(h.model) {
		h.t.Fatalf("state size %d, model %d", st.Len(), len(h.model))
	}
	for k, want := range h.model {
		if got, ok := st.Get(k); !ok || got != want {
			h.t.Fatalf("key %q = %d,%v, model %d", k, got, ok, want)
		}
	}
	want := DigestOf(modelState(h.model))
	if h.store.Head() != want {
		h.t.Fatalf("head %s, model digest %s", h.store.Head(), want)
	}
	if h.store.Seq() != uint64(len(h.log)) {
		h.t.Fatalf("seq %d, log length %d", h.store.Seq(), len(h.log))
	}
}

func modelState(m map[string]int64) *State {
	s := NewState()
	for k, v := range m {
		s.kv[k] = v
	}
	return s
}

func (h *harness) randKey() string {
	return string('a' + byte(h.r.Intn(8)))
}

// commit appends a fresh valid delta.
func (h *harness) commit() {
	n := 1 + h.r.Intn(3)
	ops := make([]Op, n)
	for i := range ops {
		op := Op{Key: h.randKey(), Val: int64(h.r.Intn(100))}
		switch h.r.Intn(3) {
		case 0:
			op.Kind = OpSet
		case 1:
			op.Kind = OpAdd
		case 2:
			op.Kind = OpDel
		}
		ops[i] = op
	}
	d := Delta{Seq: h.store.Seq() + 1, BaseDigest: h.store.Head(), Ops: ops}
	next := h.store.State()
	for _, op := range ops {
		next.apply(op)
	}
	d.Digest = DigestOf(next)
	if err := h.store.Apply(d); err != nil {
		h.t.Fatalf("commit valid delta: %v", err)
	}
	h.log = append(h.log, d)
	for _, op := range ops {
		switch op.Kind {
		case OpSet:
			h.model[op.Key] = op.Val
		case OpAdd:
			h.model[op.Key] += op.Val
		case OpDel:
			delete(h.model, op.Key)
		}
	}
	if h.r.Intn(4) == 0 {
		h.snaps = append(h.snaps, h.store.Snapshot())
	}
}

// reapply feeds an already-applied delta back; it must be a no-op.
func (h *harness) reapply() {
	if len(h.log) == 0 {
		return
	}
	d := h.log[h.r.Intn(len(h.log))]
	head, seq := h.store.Head(), h.store.Seq()
	if err := h.store.Apply(d); err != nil {
		h.t.Fatalf("idempotent re-apply of seq %d: %v", d.Seq, err)
	}
	if h.store.Head() != head || h.store.Seq() != seq {
		h.t.Fatalf("re-apply of seq %d mutated the store", d.Seq)
	}
}

// corrupt flips part of the next delta and expects rejection with the
// right error kind, leaving the store untouched.
func (h *harness) corrupt() {
	d := Delta{
		Seq:        h.store.Seq() + 1,
		BaseDigest: h.store.Head(),
		Ops:        []Op{{Kind: OpSet, Key: h.randKey(), Val: int64(h.r.Intn(100))}},
	}
	next := h.store.State()
	next.apply(d.Ops[0])
	d.Digest = DigestOf(next)

	var want error
	switch h.r.Intn(3) {
	case 0: // corrupted pre-state digest
		d.BaseDigest[h.r.Intn(len(d.BaseDigest))] ^= 1 << uint(h.r.Intn(8))
		want = ErrBaseMismatch
	case 1: // corrupted post-state digest
		d.Digest[h.r.Intn(len(d.Digest))] ^= 1 << uint(h.r.Intn(8))
		want = ErrDigestMismatch
	case 2: // corrupted op payload, digest no longer reproducible
		d.Ops[0].Val++
		want = ErrDigestMismatch
	}
	head, seq := h.store.Head(), h.store.Seq()
	if err := h.store.Apply(d); KindOf(err) != want {
		h.t.Fatalf("corrupt delta: got %v, want %v", err, want)
	}
	if h.store.Head() != head || h.store.Seq() != seq {
		h.t.Fatal("rejected corrupt delta mutated the store")
	}
}

// gap skips a sequence number and expects ErrGap.
func (h *harness) gap() {
	d := Delta{
		Seq:        h.store.Seq() + 2,
		BaseDigest: h.store.Head(),
		Ops:        []Op{{Kind: OpSet, Key: h.randKey(), Val: 1}},
	}
	next := h.store.State()
	next.apply(d.Ops[0])
	d.Digest = DigestOf(next)
	if err := h.store.Apply(d); KindOf(err) != ErrGap {
		h.t.Fatalf("gap delta: got %v, want ErrGap", err)
	}
}

// fork builds a divergent branch from a past snapshot and verifies that
// its deltas cannot attach to the main chain's baseline.
func (h *harness) fork() {
	if len(h.snaps) == 0 {
		return
	}
	snap := h.snaps[h.r.Intn(len(h.snaps))]
	if snap.Seq >= h.store.Seq() {
		return // need a strictly past baseline to diverge from
	}
	// A branch delta valid on its own baseline but foreign to main.
	bd := Delta{
		Seq:        snap.Seq + 1,
		BaseDigest: snap.Digest,
		Ops:        []Op{{Kind: OpSet, Key: "fork", Val: int64(1 + h.r.Intn(9))}},
	}
	next := snap.State.Clone()
	next.apply(bd.Ops[0])
	bd.Digest = DigestOf(next)

	head, seq := h.store.Head(), h.store.Seq()
	err := h.store.Apply(bd)
	// bd.Seq <= main seq: the slot is taken by a different delta.
	if KindOf(err) != ErrConflict {
		h.t.Fatalf("branch delta on main chain: got %v, want ErrConflict", err)
	}
	if h.store.Head() != head || h.store.Seq() != seq {
		h.t.Fatal("foreign branch delta mutated the main store")
	}

	// Grow the branch on its own store, then try to attach main's next
	// delta onto the branch head: baselines differ, must be rejected.
	branch, rerr := Restore(snap, []Delta{bd})
	if rerr != nil {
		h.t.Fatalf("branch delta rejected on its own baseline: %v", rerr)
	}
	d := Delta{Seq: branch.Seq() + 1, BaseDigest: h.store.Head(),
		Ops: []Op{{Kind: OpSet, Key: "main", Val: 1}}}
	mn := h.store.State()
	mn.apply(d.Ops[0])
	d.Digest = DigestOf(mn)
	if err := branch.Apply(d); KindOf(err) != ErrBaseMismatch {
		h.t.Fatalf("main delta on branch baseline: got %v, want ErrBaseMismatch", err)
	}
}

// restore rebuilds from a random snapshot plus the exact delta suffix and
// requires bit-identical recovery; a corrupted suffix must fail atomically.
func (h *harness) restore() {
	if len(h.snaps) == 0 {
		return
	}
	snap := h.snaps[h.r.Intn(len(h.snaps))]
	suffix := h.log[snap.Seq:]

	got, err := Restore(snap, suffix)
	if err != nil {
		h.t.Fatalf("restore from snap seq %d: %v", snap.Seq, err)
	}
	if got.Head() != h.store.Head() || got.Seq() != h.store.Seq() {
		h.t.Fatalf("restored head %s seq %d, want %s seq %d",
			got.Head(), got.Seq(), h.store.Head(), h.store.Seq())
	}
	// Re-applying the last delta on the restored store is a no-op.
	if len(suffix) > 0 {
		if err := got.Apply(suffix[len(suffix)-1]); err != nil {
			h.t.Fatalf("re-apply on restored store: %v", err)
		}
	}

	// Corrupt one delta inside the suffix: recovery must fail and must
	// not hand back a partially-restored store.
	if len(suffix) > 0 {
		bad := make([]Delta, len(suffix))
		copy(bad, suffix)
		i := h.r.Intn(len(bad))
		bad[i].Digest[0] ^= 0xff
		s, rerr := Restore(snap, bad)
		if rerr == nil {
			h.t.Fatal("restore with corrupted suffix succeeded")
		}
		if s != nil {
			h.t.Fatal("failed restore returned a partial store")
		}
	}

	// A snapshot with a tampered digest must be rejected up front.
	badSnap := snap
	badSnap.Digest[0] ^= 0xff
	if _, rerr := Restore(badSnap, suffix); KindOf(rerr) != ErrBadSnapshot {
		h.t.Fatalf("tampered snapshot: got %v, want ErrBadSnapshot", rerr)
	}
}
