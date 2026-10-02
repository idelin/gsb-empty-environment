package aries

import (
	"path/filepath"
	"testing"
)

// TestRecoverCrashScenario is the acceptance test: a WAL with 3
// uncommitted and 2 committed transactions crashes after a checkpoint;
// recovery must roll back the losers and keep the winners.
func TestRecoverCrashScenario(t *testing.T) {
	dir := t.TempDir()
	sc, err := BuildCrashScenario(filepath.Join(dir, "test.wal"))
	if err != nil {
		t.Fatalf("build scenario: %v", err)
	}
	sc.WAL.Close()

	// Reopen the WAL from disk, simulating a post-crash restart.
	wal, err := OpenWAL(sc.WAL.Path())
	if err != nil {
		t.Fatalf("reopen WAL: %v", err)
	}
	defer wal.Close()

	eng := NewEngine(wal, sc.Disk)
	res, err := eng.Recover()
	if err != nil {
		t.Fatalf("recover: %v", err)
	}

	// --- Analysis assertions ---
	// Table holds the 3 losers plus T2 (committed after the checkpoint,
	// not yet ENDed). T1 was committed before the checkpoint.
	if len(res.TxnTable) != 4 {
		t.Fatalf("analysis: want 4 txns in table, got %d: %v", len(res.TxnTable), res.TxnTable)
	}
	for _, id := range []uint64{2, 3, 4, 5} {
		if _, ok := res.TxnTable[id]; !ok {
			t.Errorf("analysis: txn %d missing from transaction table", id)
		}
	}
	if res.TxnTable[2].Status != TxnCommitted {
		t.Errorf("analysis: txn 2 status = %v, want COMMITTED", res.TxnTable[2].Status)
	}
	if res.TxnTable[5].Status != TxnAborting {
		t.Errorf("analysis: txn 5 status = %v, want ABORTING", res.TxnTable[5].Status)
	}
	if len(res.DirtyPage) != 4 {
		t.Errorf("analysis: dirty page table has %d pages, want 4", len(res.DirtyPage))
	}
	// recLSN of P1 must be LSN 1 (its first update after checkpoint start).
	if res.DirtyPage[1] != 1 {
		t.Errorf("analysis: recLSN[P1] = %d, want 1", res.DirtyPage[1])
	}

	// --- Redo assertions ---
	if res.RedoLSN != 1 {
		t.Errorf("redo: start LSN = %d, want 1 (min recLSN)", res.RedoLSN)
	}
	// LSNs 1 and 4 were already flushed (pageLSN >= lsn) and must be skipped.
	for _, skipped := range []uint64{1, 4} {
		for _, done := range res.Redone {
			if done == skipped {
				t.Errorf("redo: LSN %d was already on disk but got redone", skipped)
			}
		}
	}
	// LSNs 2,5,8,9,11 must be redone (history repeated, incl. losers).
	wantRedone := map[uint64]bool{2: true, 5: true, 8: true, 9: true, 11: true}
	if len(res.Redone) != len(wantRedone) {
		t.Fatalf("redo: redone LSNs = %v, want %v", res.Redone, wantRedone)
	}
	for _, lsn := range res.Redone {
		if !wantRedone[lsn] {
			t.Errorf("redo: unexpected redone LSN %d", lsn)
		}
	}

	// --- Undo assertions ---
	// 4 loser updates must be compensated: T3(2 updates), T4(2), T5(1)...
	// T3: LSN 8, 2 ; T4: LSN 11, 5 ; T5: LSN 9  => 5 CLRs.
	if len(res.CLRs) != 5 {
		t.Fatalf("undo: wrote %d CLRs, want 5", len(res.CLRs))
	}
	for _, clr := range res.CLRs {
		if clr.Type != LogCLR {
			t.Errorf("undo: compensation record has type %v", clr.Type)
		}
	}
	// Every loser txn must be closed with END.
	ended := map[uint64]bool{}
	for _, id := range res.EndedTxns {
		ended[id] = true
	}
	for _, id := range []uint64{3, 4, 5} {
		if !ended[id] {
			t.Errorf("undo: txn %d never got an END record", id)
		}
	}

	// --- Final durable state ---
	for pageID, want := range sc.Expected {
		p, ok := sc.Disk.ReadPage(pageID)
		if !ok {
			t.Fatalf("page %d missing after recovery", pageID)
		}
		if got := string(p.Data); got != want {
			t.Errorf("page %d = %q, want %q", pageID, got, want)
		}
	}

	// CLR undoNext chains must be consistent: re-running recovery on the
	// post-recovery WAL must be a no-op for data (idempotence).
	before := map[uint64]string{}
	for _, id := range sc.PageNames {
		p, _ := sc.Disk.ReadPage(id)
		before[id] = string(p.Data)
	}
	res2, err := NewEngine(wal, sc.Disk).Recover()
	if err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if len(res2.CLRs) != 0 {
		t.Errorf("second recover: wrote %d CLRs, want 0 (no losers left)", len(res2.CLRs))
	}
	for _, id := range sc.PageNames {
		p, _ := sc.Disk.ReadPage(id)
		if string(p.Data) != before[id] {
			t.Errorf("second recover: page %d changed to %q", id, p.Data)
		}
	}
}

// TestAnalysisWithoutCheckpoint covers the no-checkpoint path:
// analysis must scan the whole log.
func TestAnalysisWithoutCheckpoint(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(filepath.Join(dir, "nockpt.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()
	if _, err := wal.Append(&LogRecord{Type: LogUpdate, TxnID: 7, PageID: 42,
		Before: []byte("a"), After: []byte("b")}); err != nil {
		t.Fatal(err)
	}
	disk := NewMapStorage()
	res, err := NewEngine(wal, disk).Recover()
	if err != nil {
		t.Fatal(err)
	}
	if res.DirtyPage[42] != 1 {
		t.Errorf("recLSN = %d, want 1", res.DirtyPage[42])
	}
	if len(res.CLRs) != 1 {
		t.Errorf("CLRs = %d, want 1", len(res.CLRs))
	}
	p, _ := disk.ReadPage(42)
	if string(p.Data) != "a" {
		t.Errorf("page 42 = %q, want rolled-back %q", p.Data, "a")
	}
}

// TestWALRoundTrip verifies binary encode/decode of every record type.
func TestWALRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rt.wal")
	wal, err := OpenWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	in := []*LogRecord{
		{Type: LogUpdate, TxnID: 1, PageID: 9, PrevLSN: 0, Before: []byte("x"), After: []byte("y")},
		{Type: LogCommit, TxnID: 1, PrevLSN: 1},
		{Type: LogCLR, TxnID: 2, PageID: 9, PrevLSN: 2, UndoNextLSN: 0, After: []byte("x")},
		{Type: LogAbort, TxnID: 2},
		{Type: LogEnd, TxnID: 2},
		{Type: LogCheckpoint, After: EncodeCheckpointPayload(&CheckpointPayload{
			Txns: map[uint64]uint64{1: 1}, DirtyPages: map[uint64]uint64{9: 1}})},
	}
	for _, r := range in {
		if _, err := wal.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	wal.Close()

	wal2, err := OpenWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	defer wal2.Close()
	out, err := wal2.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("scanned %d records, want %d", len(out), len(in))
	}
	for i, r := range out {
		if r.Type != in[i].Type || r.TxnID != in[i].TxnID || r.PageID != in[i].PageID {
			t.Errorf("record %d mismatch: got %v", i, r)
		}
		if r.LSN != uint64(i+1) {
			t.Errorf("record %d LSN = %d, want %d", i, r.LSN, i+1)
		}
	}
	// LSN allocator must resume after the highest persisted LSN.
	n, err := wal2.Append(&LogRecord{Type: LogEnd, TxnID: 99})
	if err != nil {
		t.Fatal(err)
	}
	if n != uint64(len(in))+1 {
		t.Errorf("resumed LSN = %d, want %d", n, len(in)+1)
	}
}
