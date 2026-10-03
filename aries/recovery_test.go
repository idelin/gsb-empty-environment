package aries

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"ariesdb/wal"
)

func tempWAL(t *testing.T) (string, func()) {
	dir, err := ioutil.TempDir("", "aries-test")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "test.log"), func() { os.RemoveAll(dir) }
}

func appendUpdate(t *testing.T, w *wal.Writer, txnID, pageID uint64, prev wal.LSN, before, after string) wal.LSN {
	lsn, err := w.Append(&wal.Record{
		Type:    wal.TypeUpdate,
		TxnID:   txnID,
		PrevLSN: prev,
		PageID:  pageID,
		Before:  []byte(before),
		After:   []byte(after),
	})
	if err != nil {
		t.Fatal(err)
	}
	return lsn
}

func appendSimple(t *testing.T, w *wal.Writer, typ wal.RecordType, txnID uint64, prev wal.LSN) wal.LSN {
	lsn, err := w.Append(&wal.Record{Type: typ, TxnID: txnID, PrevLSN: prev})
	if err != nil {
		t.Fatal(err)
	}
	return lsn
}

// TestRecoverAfterCrash builds the acceptance scenario: five transactions
// (T1, T2 committed; T3, T4, T5 uncommitted), a checkpoint, then a crash.
// It verifies that committed data survives, uncommitted changes are rolled
// back, and the undo pass emits CLRs and END records.
func TestRecoverAfterCrash(t *testing.T) {
	path, cleanup := tempWAL(t)
	defer cleanup()

	w, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// T1 and T2 commit before the checkpoint.
	t1u := appendUpdate(t, w, 1, 1, 0, "p1-v0", "p1-t1")
	t2u := appendUpdate(t, w, 2, 2, 0, "p2-v0", "p2-t2")
	t1c := appendSimple(t, w, wal.TypeCommit, 1, t1u)
	t2c := appendSimple(t, w, wal.TypeCommit, 2, t2u)

	// Checkpoint capturing the transaction table and dirty page table.
	if _, err := w.Append(&wal.Record{
		Type: wal.TypeCheckpoint,
		Checkpoint: &wal.CheckpointData{
			Txns: map[uint64]wal.TxnSnapshot{
				1: {LastLSN: t1c, Status: wal.TxnCommitted},
				2: {LastLSN: t2c, Status: wal.TxnCommitted},
			},
			DirtyPages: map[uint64]wal.LSN{1: t1u, 2: t2u},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// T3, T4, T5 update pages after the checkpoint and never commit.
	t3u := appendUpdate(t, w, 3, 1, 0, "p1-t1", "p1-t3")
	t4u := appendUpdate(t, w, 4, 3, 0, "p3-v0", "p3-t4")
	t5u := appendUpdate(t, w, 5, 4, 0, "p4-v0", "p4-t5")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Crash-time disk state: T1's committed update and T3's/T4's
	// uncommitted updates were flushed; T2's committed update and T5's
	// uncommitted update were lost.
	store := NewStorage()
	store.Apply(1, t3u, []byte("p1-t3")) // uncommitted, flushed -> undo
	store.Apply(2, 0, []byte("p2-v0"))   // committed, lost     -> redo
	store.Apply(3, t4u, []byte("p3-t4")) // uncommitted, flushed -> undo
	store.Apply(4, 0, []byte("p4-v0"))   // uncommitted, lost     -> redo+undo

	rec := NewRecovery(path, store)

	// --- Analysis ---
	if err := rec.Analysis(); err != nil {
		t.Fatal(err)
	}
	if len(rec.DPT) != 4 {
		t.Fatalf("DPT should track 4 pages, got %d", len(rec.DPT))
	}
	if rec.DPT[1] != t1u || rec.DPT[2] != t2u || rec.DPT[3] != t4u || rec.DPT[4] != t5u {
		t.Fatalf("unexpected DPT recLSNs: %v", rec.DPT)
	}
	if len(rec.TxnTable) != 5 {
		t.Fatalf("transaction table should have 5 entries, got %d", len(rec.TxnTable))
	}
	for _, id := range []uint64{1, 2} {
		if rec.TxnTable[id].Status != wal.TxnCommitted {
			t.Fatalf("txn %d should be committed, got %s", id, rec.TxnTable[id].Status)
		}
	}
	for _, id := range []uint64{3, 4, 5} {
		if rec.TxnTable[id].Status != wal.TxnRunning {
			t.Fatalf("txn %d should be running, got %s", id, rec.TxnTable[id].Status)
		}
	}

	// --- Redo ---
	if err := rec.Redo(); err != nil {
		t.Fatal(err)
	}
	if got := store.Get(2); string(got.Data) != "p2-t2" || got.LSN != t2u {
		t.Fatalf("redo must restore committed page 2, got %q lsn=%d", got.Data, got.LSN)
	}
	if got := store.Get(4); string(got.Data) != "p4-t5" || got.LSN != t5u {
		t.Fatalf("redo must repeat uncommitted history on page 4, got %q lsn=%d", got.Data, got.LSN)
	}

	// --- Undo ---
	if err := rec.Undo(); err != nil {
		t.Fatal(err)
	}
	expect := map[uint64]string{1: "p1-t1", 2: "p2-t2", 3: "p3-v0", 4: "p4-v0"}
	for pageID, want := range expect {
		if got := store.Get(pageID); string(got.Data) != want {
			t.Fatalf("page %d: got %q, want %q", pageID, got.Data, want)
		}
	}
	if len(rec.TxnTable) != 0 {
		t.Fatalf("transaction table should be empty after undo, got %d entries", len(rec.TxnTable))
	}

	// Undo must have written one CLR and one END per loser transaction.
	var clrs, ends int
	for _, rec := range rec.Appended {
		switch rec.Type {
		case wal.TypeCLR:
			clrs++
		case wal.TypeEnd:
			ends++
		}
	}
	if clrs != 3 || ends != 3 {
		t.Fatalf("expected 3 CLRs and 3 ENDs, got %d and %d", clrs, ends)
	}

	// The WAL on disk must contain the compensation records, and undo
	// pages must be stamped with their CLR LSNs.
	persisted, err := wal.ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	var diskCLRs, diskENDs int
	for _, rec := range persisted {
		switch rec.Type {
		case wal.TypeCLR:
			diskCLRs++
			page := store.Get(rec.PageID)
			if page.LSN != rec.LSN {
				t.Fatalf("page %d LSN %d does not match CLR LSN %d", rec.PageID, page.LSN, rec.LSN)
			}
		case wal.TypeEnd:
			diskENDs++
		}
	}
	if diskCLRs != 3 || diskENDs != 3 {
		t.Fatalf("persisted WAL should have 3 CLRs and 3 ENDs, got %d and %d", diskCLRs, diskENDs)
	}
}

// TestRedoIdempotent verifies that running recovery twice does not change
// the final state: CLRs are redone, not re-undone.
func TestRedoIdempotent(t *testing.T) {
	path, cleanup := tempWAL(t)
	defer cleanup()

	w, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	appendUpdate(t, w, 1, 1, 0, "v0", "v1")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	store := NewStorage()
	store.Apply(1, 0, []byte("v0"))

	first := NewRecovery(path, store)
	if err := first.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := store.Get(1); string(got.Data) != "v0" {
		t.Fatalf("first recovery: got %q, want v0", got.Data)
	}

	second := NewRecovery(path, store)
	if err := second.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := store.Get(1); string(got.Data) != "v0" {
		t.Fatalf("second recovery: got %q, want v0", got.Data)
	}
	if len(second.TxnTable) != 0 {
		t.Fatalf("transaction table should stay empty, got %d entries", len(second.TxnTable))
	}
}

// TestAnalysisWithoutCheckpoint covers a log with no checkpoint at all.
func TestAnalysisWithoutCheckpoint(t *testing.T) {
	path, cleanup := tempWAL(t)
	defer cleanup()

	w, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	u := appendUpdate(t, w, 7, 9, 0, "a", "b")
	appendSimple(t, w, wal.TypeCommit, 7, u)
	appendUpdate(t, w, 8, 9, 0, "b", "c")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	store := NewStorage()
	rec := NewRecovery(path, store)
	if err := rec.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := store.Get(9); string(got.Data) != "b" {
		t.Fatalf("committed value should survive, got %q", got.Data)
	}
	if len(rec.TxnTable) != 0 {
		t.Fatalf("transaction table should be empty, got %d entries", len(rec.TxnTable))
	}
}
