package rootstore

import "testing"

// commitOps records the storage ops of the second commit (B) on top of a
// committed baseline (A), returning both roots and the op log.
func commitOps(t *testing.T) (oldRoot, newRoot uint64, ops []Op) {
	t.Helper()
	fs := NewSimFS()
	st := mustOpen(t, fs)
	a := mustCommit(t, st, "A")
	rec := recordOps(fs)
	b := mustCommit(t, st, "B")
	fs.Hook = nil
	return a.RootID, b.RootID, *rec
}

// freshBaseline replays commit A deterministically on a new SimFS.
func freshBaseline(t *testing.T) *SimFS {
	t.Helper()
	fs := NewSimFS()
	st := mustOpen(t, fs)
	mustCommit(t, st, "A")
	st.Close()
	return fs
}

// TestCrashAtEveryBoundary enumerates every write/sync boundary of a
// commit: replay a prefix of the op log, crash, and require that recovery
// yields only the old or the new complete root.
func TestCrashAtEveryBoundary(t *testing.T) {
	oldRoot, newRoot, ops := commitOps(t)
	for _, mode := range []CrashMode{ProcessCrash, PowerLoss} {
		for i := 0; i <= len(ops); i++ {
			fs := freshBaseline(t)
			replayOps(t, fs, ops[:i])
			fs.Crash(mode)
			got := assertRecoveredRoot(t, fs, oldRoot, newRoot)
			if i == len(ops) && got.RootID != newRoot {
				t.Fatalf("mode=%v full prefix: want new root %d, got %d", mode, newRoot, got.RootID)
			}
			if i == 0 && got.RootID != oldRoot {
				t.Fatalf("mode=%v empty prefix: want old root %d, got %d", mode, oldRoot, got.RootID)
			}
		}
	}
}

// TestTornSlotEveryOffset enumerates every tear point of the slot publish
// write: any short slot must recover the old root; only the complete slot
// publishes the new root.
func TestTornSlotEveryOffset(t *testing.T) {
	oldRoot, newRoot, ops := commitOps(t)
	slotIdx := -1
	for i, op := range ops {
		if op.File == MetaFileName && op.Kind == OpWrite {
			slotIdx = i
		}
	}
	if slotIdx < 0 {
		t.Fatal("no slot write recorded")
	}
	for torn := 0; torn <= SlotSize; torn++ {
		fs := freshBaseline(t)
		replayOps(t, fs, ops[:slotIdx])
		meta, _ := fs.Open(MetaFileName)
		op := ops[slotIdx]
		if _, err := meta.WriteAt(op.Data[:torn], op.Off); err != nil {
			t.Fatal(err)
		}
		replayOps(t, fs, ops[slotIdx+1:])
		fs.Crash(PowerLoss)
		got := assertRecoveredRoot(t, fs, oldRoot, newRoot)
		want := oldRoot
		if torn == SlotSize {
			want = newRoot
		}
		if got.RootID != want {
			t.Fatalf("torn=%d: want root %d, got %d", torn, want, got.RootID)
		}
	}
}

// TestLostWriteAtEveryBoundary drops each single write op of a commit and
// requires recovery to still yield a complete old or new root.
func TestLostWriteAtEveryBoundary(t *testing.T) {
	oldRoot, newRoot, ops := commitOps(t)
	for i, op := range ops {
		if op.Kind != OpWrite {
			continue
		}
		fs := freshBaseline(t)
		rest := append(append([]Op{}, ops[:i]...), ops[i+1:]...)
		replayOps(t, fs, rest)
		fs.Crash(PowerLoss)
		assertRecoveredRoot(t, fs, oldRoot, newRoot)
	}
}

// TestReorderedWritesBeforeSync replays all commit writes in reverse
// order (syncs kept in order): reordering ahead of a sync barrier must
// not affect the outcome.
func TestReorderedWritesBeforeSync(t *testing.T) {
	oldRoot, newRoot, ops := commitOps(t)
	var writes, syncs []Op
	for _, op := range ops {
		if op.Kind == OpWrite {
			writes = append(writes, op)
		} else {
			syncs = append(syncs, op)
		}
	}
	reversed := append([]Op{}, writes...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	fs := freshBaseline(t)
	replayOps(t, fs, append(reversed, syncs...))
	fs.Crash(PowerLoss)
	got := assertRecoveredRoot(t, fs, oldRoot, newRoot)
	if got.RootID != newRoot {
		t.Fatalf("reordered writes: want new root %d, got %d", newRoot, got.RootID)
	}
}

// TestSlotPublishIsLastWrite documents the protocol invariant directly:
// every page write and the page sync happen before the slot publish.
func TestSlotPublishIsLastWrite(t *testing.T) {
	_, _, ops := commitOps(t)
	slotIdx, pageSyncIdx := -1, -1
	for i, op := range ops {
		if op.File == MetaFileName && op.Kind == OpWrite {
			slotIdx = i
		}
		if op.File == PageFileName && op.Kind == OpSync {
			pageSyncIdx = i
		}
	}
	if slotIdx < 0 || pageSyncIdx < 0 {
		t.Fatalf("missing ops: slot=%d pageSync=%d", slotIdx, pageSyncIdx)
	}
	if pageSyncIdx > slotIdx {
		t.Fatalf("page sync (%d) must precede slot publish (%d)", pageSyncIdx, slotIdx)
	}
	for i, op := range ops {
		if op.File == PageFileName && op.Kind == OpWrite && i > slotIdx {
			t.Fatalf("page write after slot publish at op %d", i)
		}
	}
}
