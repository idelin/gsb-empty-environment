package aries

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readCell(t *testing.T, store *PageStore, pageID uint64, off uint32) uint32 {
	t.Helper()
	p, err := store.ReadPage(pageID)
	if err != nil {
		t.Fatalf("read page %d: %v", pageID, err)
	}
	return p.Get(off)
}

func TestCrashRecoveryThreePhases(t *testing.T) {
	dir := t.TempDir()
	sc, err := BuildCrashScenario(dir)
	if err != nil {
		t.Fatalf("build scenario: %v", err)
	}

	report, err := Recover(sc.WALPath, sc.PagePath)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}

	// --- Analysis phase ---
	if report.CheckpointLSN != sc.CheckpointLSN {
		t.Fatalf("analysis checkpoint LSN = %d, want %d", report.CheckpointLSN, sc.CheckpointLSN)
	}
	for _, pid := range []uint64{1, 2, 3, 4} {
		if _, ok := report.DPT[pid]; !ok {
			t.Fatalf("DPT missing page %d: %v", pid, report.DPT)
		}
	}
	if recLSN := report.DPT[4]; recLSN <= sc.CheckpointLSN {
		t.Fatalf("page 4 recLSN = %d, must be a post-checkpoint LSN (> %d)", recLSN, sc.CheckpointLSN)
	}
	if !reflect.DeepEqual(report.Losers, sc.UncommittedTxns) {
		t.Fatalf("losers = %v, want %v", report.Losers, sc.UncommittedTxns)
	}

	// --- Redo phase: pages 3,4 were never flushed -> 4 updates re-applied ---
	if report.Redone != 4 {
		t.Fatalf("redone = %d, want 4", report.Redone)
	}

	// --- Undo phase: 5 loser updates rolled back via CLRs ---
	if report.Undone != 5 {
		t.Fatalf("undone = %d, want 5", report.Undone)
	}
	if report.CLRsWritten != 5 {
		t.Fatalf("CLRs written = %d, want 5", report.CLRsWritten)
	}

	// --- Final state: committed persisted, uncommitted rolled back ---
	store, err := OpenPageStore(sc.PagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, want := range ExpectedCells {
		if got := readCell(t, store, want.PageID, want.Off); got != want.Val {
			t.Errorf("page %d off %d = %d, want %d", want.PageID, want.Off, got, want.Val)
		}
	}

	// --- WAL now contains CLRs and END records for every loser ---
	wal, err := OpenWAL(sc.WALPath)
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()
	records, err := wal.Records()
	if err != nil {
		t.Fatal(err)
	}
	clrs, ends := 0, map[uint64]bool{}
	for _, r := range records {
		switch r.Type {
		case LogCLR:
			clrs++
			if r.UndoNext == 0 && r.PrevLSN == 0 {
				t.Errorf("CLR at LSN %d has no undo chain", r.LSN)
			}
		case LogEnd:
			ends[r.TxnID] = true
		}
	}
	if clrs != 5 {
		t.Errorf("WAL contains %d CLRs, want 5", clrs)
	}
	for _, txn := range sc.UncommittedTxns {
		if !ends[txn] {
			t.Errorf("no END record for loser txn %d", txn)
		}
	}
}

func TestRecoveryIdempotent(t *testing.T) {
	dir := t.TempDir()
	sc, err := BuildCrashScenario(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(sc.WALPath, sc.PagePath); err != nil {
		t.Fatalf("first recover: %v", err)
	}
	// Second run (e.g. crash during the first undo): nothing left to do.
	report, err := Recover(sc.WALPath, sc.PagePath)
	if err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if len(report.Losers) != 0 {
		t.Fatalf("second run losers = %v, want none", report.Losers)
	}
	if report.Undone != 0 || report.CLRsWritten != 0 {
		t.Fatalf("second run undid %d updates / wrote %d CLRs, want 0/0", report.Undone, report.CLRsWritten)
	}
	if report.Redone != 0 {
		t.Fatalf("second run redid %d records, want 0 (pageLSN gates redo)", report.Redone)
	}
	store, err := OpenPageStore(sc.PagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, want := range ExpectedCells {
		if got := readCell(t, store, want.PageID, want.Off); got != want.Val {
			t.Errorf("after re-recovery page %d off %d = %d, want %d", want.PageID, want.Off, got, want.Val)
		}
	}
}

func TestRecoveryWithoutCheckpoint(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")
	pagePath := filepath.Join(dir, "pages.db")
	db, err := OpenDB(walPath, pagePath)
	if err != nil {
		t.Fatal(err)
	}
	t1 := db.Begin()
	t2 := db.Begin()
	if _, err := db.Update(t1, 9, 3, 33); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update(t2, 9, 4, 44); err != nil {
		t.Fatal(err)
	}
	db.Commit(t1)
	db.Crash() // no checkpoint, no flush at all

	report, err := Recover(walPath, pagePath)
	if err != nil {
		t.Fatal(err)
	}
	if report.CheckpointLSN != 0 {
		t.Fatalf("checkpoint LSN = %d, want 0", report.CheckpointLSN)
	}
	if !reflect.DeepEqual(report.Losers, []uint64{t2}) {
		t.Fatalf("losers = %v, want [%d]", report.Losers, t2)
	}
	store, err := OpenPageStore(pagePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := readCell(t, store, 9, 3); got != 33 {
		t.Errorf("committed cell = %d, want 33", got)
	}
	if got := readCell(t, store, 9, 4); got != 0 {
		t.Errorf("uncommitted cell = %d, want 0", got)
	}
}

func TestWALConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenWAL(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	const workers, perWorker = 16, 100
	done := make(chan uint64, workers*perWorker)
	for g := 0; g < workers; g++ {
		go func(g int) {
			for i := 0; i < perWorker; i++ {
				lsn := wal.Append(&LogRecord{Type: LogUpdate, TxnID: uint64(g + 1), PageID: 1, Offset: uint32(i), NewValue: uint32(i)})
				done <- lsn
			}
		}(g)
	}
	seen := map[uint64]bool{}
	for i := 0; i < workers*perWorker; i++ {
		lsn := <-done
		if seen[lsn] {
			t.Fatalf("duplicate LSN %d", lsn)
		}
		seen[lsn] = true
	}
	wal.Close()

	wal2, err := OpenWAL(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer wal2.Close()
	records, err := wal2.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != workers*perWorker {
		t.Fatalf("read %d records, want %d", len(records), workers*perWorker)
	}
	for i := 1; i < len(records); i++ {
		if records[i].LSN <= records[i-1].LSN {
			t.Fatalf("LSNs not increasing at record %d", i)
		}
	}
}

func TestPageStoreCompressionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pages.db")
	store, err := OpenPageStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p := NewSparsePage()
	p.LSN = 42
	p.Set(100, 7)
	p.Set(900, 8)
	if err := store.WritePage(5, p); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// File must be far smaller than a raw 4 KiB page.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 64 {
		t.Fatalf("page file is %d bytes, want <= 64 (compression)", st.Size())
	}
	// Reopen: index rebuilt by scanning, data intact.
	store2, err := OpenPageStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	got, err := store2.ReadPage(5)
	if err != nil {
		t.Fatal(err)
	}
	if got.LSN != 42 || got.Get(100) != 7 || got.Get(900) != 8 || got.NonZero() != 2 {
		t.Fatalf("round trip mismatch: LSN=%d c100=%d c900=%d nz=%d", got.LSN, got.Get(100), got.Get(900), got.NonZero())
	}
}
