package rootstore

import (
	"errors"
	"math"
	"strings"
	"testing"
)

// buildTree returns a build function writing a 3-page tree (2 leaves + root).
func buildTree(tag string) func(w *PageWriter) (uint64, error) {
	return func(w *PageWriter) (uint64, error) {
		l1, err := w.New(nil, []byte(tag+"-leaf-1"))
		if err != nil {
			return 0, err
		}
		l2, err := w.New(nil, []byte(tag+"-leaf-2"))
		if err != nil {
			return 0, err
		}
		return w.New([]uint64{l1, l2}, []byte(tag+"-root"))
	}
}

func mustOpen(t *testing.T, fs FS) *Store {
	t.Helper()
	st, err := Open(fs)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return st
}

func mustCommit(t *testing.T, st *Store, tag string) Slot {
	t.Helper()
	if err := st.Commit(buildTree(tag)); err != nil {
		t.Fatalf("commit %s: %v", tag, err)
	}
	slot, ok := st.Root()
	if !ok {
		t.Fatalf("commit %s: no root", tag)
	}
	return slot
}

// recordOps installs a hook recording every storage op until detached.
func recordOps(fs *SimFS) *[]Op {
	ops := &[]Op{}
	fs.Hook = func(op *Op) {
		c := *op
		c.Data = cloneBytes(op.Data)
		*ops = append(*ops, c)
	}
	return ops
}

// replayOps re-applies recorded ops onto a fresh SimFS (no hook installed).
func replayOps(t *testing.T, fs *SimFS, ops []Op) {
	t.Helper()
	handles := map[string]File{}
	for _, op := range ops {
		f, ok := handles[op.File]
		if !ok {
			var err error
			f, err = fs.Open(op.File)
			if err != nil {
				t.Fatal(err)
			}
			handles[op.File] = f
		}
		var err error
		if op.Kind == OpWrite {
			_, err = f.WriteAt(op.Data, op.Off)
		} else {
			err = f.Sync()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// assertRecoveredRoot checks the core guarantee: recovery yields the old or
// the new complete root, and its reachable closure verifies independently.
func assertRecoveredRoot(t *testing.T, fs *SimFS, oldRoot, newRoot uint64) Slot {
	t.Helper()
	st, err := Open(fs)
	if err != nil {
		t.Fatalf("open after crash: %v", err)
	}
	defer st.Close()
	slot, ok := st.Root()
	if !ok {
		t.Fatalf("no root after crash")
	}
	if slot.RootID != oldRoot && slot.RootID != newRoot {
		t.Fatalf("recovered root %d is neither old %d nor new %d", slot.RootID, oldRoot, newRoot)
	}
	if err := VerifyClosure(st.Pages(), slot.RootID); err != nil {
		t.Fatalf("recovered root %d closure broken: %v", slot.RootID, err)
	}
	return slot
}

func TestCommitAndRecover(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	b := mustCommit(t, st, "B")
	st.Close()

	fs.Crash(PowerLoss)
	got := assertRecoveredRoot(t, fs, a.RootID, b.RootID)
	if got.RootID != b.RootID || got.Seq != 2 {
		t.Fatalf("want newest root %v, got %v", b, got)
	}
}

func TestHalfWrittenSlotKeepsOldRoot(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	// Tear the second commit's slot publish: only 40 of 128 bytes land.
	fs.Hook = func(op *Op) {
		if op.File == MetaFileName && op.Kind == OpWrite {
			op.Data = op.Data[:40]
		}
	}
	b := mustCommit(t, st, "B") // locally "succeeds"
	fs.Hook = nil
	fs.Crash(PowerLoss)
	got := assertRecoveredRoot(t, fs, a.RootID, b.RootID)
	if got.RootID != a.RootID {
		t.Fatalf("half-written slot must keep old root %d, got %d", a.RootID, got.RootID)
	}
}

func TestLostSlotWriteKeepsOldRoot(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	fs.Hook = func(op *Op) {
		if op.File == MetaFileName && op.Kind == OpWrite {
			op.Drop = true
		}
	}
	b := mustCommit(t, st, "B")
	fs.Hook = nil
	fs.Crash(PowerLoss)
	got := assertRecoveredRoot(t, fs, a.RootID, b.RootID)
	if got.RootID != a.RootID {
		t.Fatalf("lost slot write must keep old root %d, got %d", a.RootID, got.RootID)
	}
}

// TestProcessCrashVsPowerLoss draws the boundary between the two crash
// models when the meta sync is lost: a process crash keeps OS buffers (new
// root visible), a power loss does not (old root).
func TestProcessCrashVsPowerLoss(t *testing.T) {
	for _, mode := range []CrashMode{ProcessCrash, PowerLoss} {
		fs := NewSimFS()
		st := mustOpen(t, fs)
		a := mustCommit(t, st, "A")
		fs.Hook = func(op *Op) {
			if op.File == MetaFileName && op.Kind == OpSync {
				op.Drop = true
			}
		}
		b := mustCommit(t, st, "B")
		fs.Hook = nil
		fs.Crash(mode)
		got := assertRecoveredRoot(t, fs, a.RootID, b.RootID)
		want := a.RootID
		if mode == ProcessCrash {
			want = b.RootID
		}
		if got.RootID != want {
			t.Fatalf("mode=%v: want root %d, got %d", mode, want, got.RootID)
		}
	}
}

func TestBothSlotsCorruptRefuseOpen(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	mustCommit(t, st, "A")
	st.Close()

	meta, _ := fs.Open(MetaFileName)
	garbage := make([]byte, 2*SlotSize)
	for i := range garbage {
		garbage[i] = 0xAB
	}
	if _, err := meta.WriteAt(garbage, 0); err != nil {
		t.Fatal(err)
	}
	meta.Sync()
	if _, err := Open(fs); !errors.Is(err, ErrNoValidSlot) {
		t.Fatalf("both slots corrupt: want ErrNoValidSlot, got %v", err)
	}
}

func TestSameSeqConflict(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	st.Close()

	// Identical duplicate publish (same seq, same content) is fine.
	meta, _ := fs.Open(MetaFileName)
	dup := encodeSlot(a)
	if _, err := meta.WriteAt(dup, SlotSize); err != nil {
		t.Fatal(err)
	}
	meta.Sync()
	st2 := mustOpen(t, fs)
	if got, _ := st2.Root(); got != a {
		t.Fatalf("idempotent duplicate: want %v, got %v", a, got)
	}
	st2.Close()

	// Same sequence, different content -> diagnosed conflict.
	other := a
	other.Pages++
	if _, err := meta.WriteAt(encodeSlot(other), SlotSize); err != nil {
		t.Fatal(err)
	}
	meta.Sync()
	_, err := Open(fs)
	if !errors.Is(err, ErrSlotConflict) {
		t.Fatalf("same seq different content: want ErrSlotConflict, got %v", err)
	}
	if !strings.Contains(err.Error(), "slot0") || !strings.Contains(err.Error(), "slot1") {
		t.Fatalf("conflict error must diagnose both slots: %v", err)
	}
}

func TestSeqWrapReaderSelectsNewest(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	st.Close()

	// Craft a wrapped sequence pair: MaxUint64 vs 0. Modular comparison
	// must pick 0 as the newest.
	meta, _ := fs.Open(MetaFileName)
	meta.WriteAt(encodeSlot(Slot{Seq: math.MaxUint64, RootID: a.RootID, Pages: a.Pages}), 0)
	meta.WriteAt(encodeSlot(Slot{Seq: 0, RootID: a.RootID, Pages: a.Pages}), SlotSize)
	meta.Sync()
	st2 := mustOpen(t, fs)
	got, _ := st2.Root()
	if got.Seq != 0 {
		t.Fatalf("wrapped seq: want seq 0 as newest, got %v", got)
	}
	// Committing on top continues at seq 1.
	d := mustCommit(t, st2, "D")
	if d.Seq != 1 {
		t.Fatalf("commit after wrapped read: want seq 1, got %d", d.Seq)
	}
}

func TestSeqExhaustedPolicy(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	st.Close()

	// Drive the sequence to the top of the range.
	meta, _ := fs.Open(MetaFileName)
	meta.WriteAt(encodeSlot(Slot{Seq: math.MaxUint64, RootID: a.RootID, Pages: a.Pages}), 0)
	meta.Sync()
	st2 := mustOpen(t, fs)
	if got, _ := st2.Root(); got.Seq != math.MaxUint64 {
		t.Fatalf("want seq MaxUint64, got %v", got)
	}
	// Explicit overflow policy: refuse, never wrap.
	if err := st2.Commit(buildTree("B")); !errors.Is(err, ErrSeqExhausted) {
		t.Fatalf("want ErrSeqExhausted, got %v", err)
	}
	if w, _, _ := fs.LayerSizes(MetaFileName); w != SlotSize {
		t.Fatalf("refused commit must not publish a slot, meta size=%d", w)
	}
}

func TestOldSlotReplayIgnored(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	// Save the old slot-0 image (seq 1, root A).
	meta, _ := fs.Open(MetaFileName)
	oldImage := make([]byte, SlotSize)
	if _, err := meta.ReadAt(oldImage, 0); err != nil {
		t.Fatal(err)
	}
	b := mustCommit(t, st, "B")
	c := mustCommit(t, st, "C")
	st.Close()

	// Replay the stale image over slot 0 (erasing C). The stale seq must
	// be detected as older; recovery falls back to B, never to A.
	if _, err := meta.WriteAt(oldImage, 0); err != nil {
		t.Fatal(err)
	}
	meta.Sync()
	st2 := mustOpen(t, fs)
	got, _ := st2.Root()
	if got.RootID == a.RootID {
		t.Fatalf("replayed stale slot must not be selected: %v", got)
	}
	if got.RootID != b.RootID {
		t.Fatalf("want fallback to B (%d), got %v (C was %d)", b.RootID, got, c.RootID)
	}
}

func TestLostPageWriteAbortsCommit(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	// Lose one page write of the second commit; the pre-publish
	// verification must catch it and refuse to publish.
	dropped := false
	fs.Hook = func(op *Op) {
		if !dropped && op.File == PageFileName && op.Kind == OpWrite {
			op.Drop = true
			dropped = true
		}
	}
	err := st.Commit(buildTree("B"))
	fs.Hook = nil
	if err == nil || !strings.Contains(err.Error(), "pre-publish") {
		t.Fatalf("want pre-publish verification failure, got %v", err)
	}
	if w, _, _ := fs.LayerSizes(MetaFileName); w != SlotSize {
		t.Fatalf("aborted commit must not publish a slot, meta size=%d", w)
	}
	fs.Crash(PowerLoss)
	if got := assertRecoveredRoot(t, fs, a.RootID, a.RootID); got.RootID != a.RootID {
		t.Fatalf("want old root %d, got %d", a.RootID, got.RootID)
	}
}

func TestVerifierDetectsCorruptClosure(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	b := mustCommit(t, st, "B")
	st.Close()

	// Corrupt a page reachable from the new root after publication.
	pages, _ := fs.Open(PageFileName)
	if _, err := pages.WriteAt(make([]byte, PageSize), 4*PageSize); err != nil {
		t.Fatal(err)
	}
	pages.Sync()

	st2 := mustOpen(t, fs) // must fall back to the old complete root
	defer st2.Close()
	got, _ := st2.Root()
	if got.RootID != a.RootID {
		t.Fatalf("corrupt new closure must fall back to old root %d, got %d", a.RootID, got.RootID)
	}
	if err := VerifyClosure(st2.Pages(), b.RootID); err == nil {
		t.Fatalf("verifier must reject corrupt closure of root %d", b.RootID)
	}
	if err := VerifyClosure(st2.Pages(), a.RootID); err != nil {
		t.Fatalf("verifier must accept old root %d: %v", a.RootID, err)
	}
}

func TestDeferredReorderedWrites(t *testing.T) {
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	// Defer page writes and apply them in reverse order at the page sync:
	// reordering before a sync barrier is harmless.
	fs.Hook = func(op *Op) {
		switch {
		case op.File == PageFileName && op.Kind == OpWrite:
			op.Defer = true
		case op.File == PageFileName && op.Kind == OpSync:
			fs.ReleaseReversed()
		}
	}
	b := mustCommit(t, st, "B")
	fs.Hook = nil
	fs.Crash(PowerLoss)
	got := assertRecoveredRoot(t, fs, a.RootID, b.RootID)
	if got.RootID != b.RootID {
		t.Fatalf("reordered pre-sync writes must still commit: want %d, got %d", b.RootID, got.RootID)
	}
}
