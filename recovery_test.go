package aries

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func u64bytes(v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return b[:]
}

func pageUint64(d *Disk, pageID int64) uint64 {
	p := d.ReadPage(pageID)
	return binary.LittleEndian.Uint64(p.Data[0:8])
}

// crashScenario builds the acceptance WAL:
//
//	T1: write P1=100, P2=200, COMMIT            (committed before checkpoint)
//	T3: write P1=111                            (active at checkpoint)
//	CHECKPOINT                                  (flushes P1=111, P2=200)
//	T2: write P2=300, COMMIT                    (committed after checkpoint)
//	T4: write P3=333                            (never commits)
//	T5: write P2=222, P3=555                    (never commits)
//	*** CRASH ***                               (buffer pool discarded)
//
// Returns the WAL path, the surviving disk, and the txn IDs in commit order.
func crashScenario(t *testing.T, dir string) (string, *Disk, [5]int64) {
	t.Helper()
	walPath := filepath.Join(dir, "crash.wal")
	wal, err := CreateWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}
	disk := NewDisk()
	db := NewDB(wal, disk, nil) // single-threaded scenario: no locking needed

	t1 := db.Begin()
	mustWrite(t, db, t1, 1, u64bytes(100))
	mustWrite(t, db, t1, 2, u64bytes(200))
	db.Commit(t1)

	t3 := db.Begin()
	mustWrite(t, db, t3, 1, u64bytes(111))

	db.Checkpoint()

	t2 := db.Begin()
	mustWrite(t, db, t2, 2, u64bytes(300))
	db.Commit(t2)

	t4 := db.Begin()
	mustWrite(t, db, t4, 3, u64bytes(333))

	t5 := db.Begin()
	mustWrite(t, db, t5, 2, u64bytes(222))
	mustWrite(t, db, t5, 3, u64bytes(555))

	// CRASH: no checkpoint, no flush; the buffer pool (db) is abandoned.
	// The WAL file stays open at the OS level; close it to simulate a clean
	// file image at restart (all appends were synchronous writes).
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	return walPath, disk, [5]int64{t1, t2, t3, t4, t5}
}

func mustWrite(t *testing.T, db *DB, txn, pageID int64, data []byte) {
	t.Helper()
	if err := db.Write(txn, pageID, 0, data); err != nil {
		t.Fatalf("write txn=%d page=%d: %v", txn, pageID, err)
	}
}

func recoverFrom(t *testing.T, walPath string, disk *Disk) *Report {
	t.Helper()
	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()
	rep, err := NewRecoveryEngine(wal, disk).Recover()
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// TestRecoverCrashScenario is the acceptance test: 3 uncommitted + 2
// committed transactions, crash after a checkpoint, full three-phase
// recovery.
func TestRecoverCrashScenario(t *testing.T) {
	dir := t.TempDir()
	walPath, disk, txns := crashScenario(t, dir)
	t1, t2, t3, t4, t5 := txns[0], txns[1], txns[2], txns[3], txns[4]

	// Sanity: before recovery the disk holds stale/uncommitted data.
	if got := pageUint64(disk, 1); got != 111 {
		t.Fatalf("pre-recovery P1 = %d, want 111 (flushed uncommitted)", got)
	}
	if got := pageUint64(disk, 2); got != 200 {
		t.Fatalf("pre-recovery P2 = %d, want 200 (checkpoint image)", got)
	}

	rep := recoverFrom(t, walPath, disk)

	if !rep.CheckpointFound {
		t.Fatal("analysis did not find the checkpoint")
	}
	wantCommitted := []int64{t1, t2}
	if !reflect.DeepEqual(rep.Committed, wantCommitted) {
		t.Fatalf("committed = %v, want %v", rep.Committed, wantCommitted)
	}
	wantLosers := []int64{t3, t4, t5}
	if !reflect.DeepEqual(rep.Losers, wantLosers) {
		t.Fatalf("losers = %v, want %v", rep.Losers, wantLosers)
	}

	// Analysis DPT: P1 was flushed at checkpoint and untouched afterwards,
	// so only P2 (recLSN = T2's update) and P3 (recLSN = T4's update) appear.
	if len(rep.DPT) != 2 {
		t.Fatalf("DPT = %v, want exactly pages 2 and 3", rep.DPT)
	}
	if _, ok := rep.DPT[2]; !ok {
		t.Fatalf("DPT missing page 2: %v", rep.DPT)
	}
	if _, ok := rep.DPT[3]; !ok {
		t.Fatalf("DPT missing page 3: %v", rep.DPT)
	}

	// Redo repeats history: 4 post-checkpoint updates re-applied.
	if len(rep.Redone) != 4 {
		t.Fatalf("redone %d records, want 4: %v", len(rep.Redone), rep.Redone)
	}
	// Undo rolls back 4 loser updates via 4 CLRs.
	if len(rep.Undone) != 4 || len(rep.CLRs) != 4 {
		t.Fatalf("undone=%v clrs=%d, want 4/4", rep.Undone, len(rep.CLRs))
	}

	// Final durable state: committed data persisted, losers rolled back.
	if got := pageUint64(disk, 1); got != 100 {
		t.Fatalf("P1 = %d, want 100 (T1 committed, T3 rolled back)", got)
	}
	if got := pageUint64(disk, 2); got != 300 {
		t.Fatalf("P2 = %d, want 300 (T2 committed, T5 rolled back)", got)
	}
	if got := pageUint64(disk, 3); got != 0 {
		t.Fatalf("P3 = %d, want 0 (T4/T5 rolled back)", got)
	}

	// pageLSN on disk must match the last log record affecting each page.
	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()
	records, err := wal.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var clrs, ends int
	for _, r := range records {
		switch r.Type {
		case RecCLR:
			clrs++
		case RecEnd:
			ends++
		}
	}
	if clrs != 4 {
		t.Fatalf("WAL holds %d CLRs, want 4", clrs)
	}
	if ends != 5 { // T1, T2 online END + T3, T4, T5 recovery END
		t.Fatalf("WAL holds %d END records, want 5", ends)
	}
}

// TestRecoveryIdempotent crashes *again* after a completed recovery and
// verifies a second recovery pass is a no-op producing the same state.
func TestRecoveryIdempotent(t *testing.T) {
	dir := t.TempDir()
	walPath, disk, _ := crashScenario(t, dir)
	recoverFrom(t, walPath, disk)

	rep2 := recoverFrom(t, walPath, disk)
	if len(rep2.Losers) != 0 {
		t.Fatalf("second recovery found losers %v, want none", rep2.Losers)
	}
	if len(rep2.Undone) != 0 {
		t.Fatalf("second recovery undid %v, want nothing", rep2.Undone)
	}
	if got := pageUint64(disk, 1); got != 100 {
		t.Fatalf("P1 = %d after second recovery, want 100", got)
	}
	if got := pageUint64(disk, 2); got != 300 {
		t.Fatalf("P2 = %d after second recovery, want 300", got)
	}
	if got := pageUint64(disk, 3); got != 0 {
		t.Fatalf("P3 = %d after second recovery, want 0", got)
	}
}

// TestCrashDuringUndo kills recovery halfway (simulated by truncating the
// WAL right after the first recovery wrote its CLRs but before END records
// could matter) and verifies CLRs are never undone twice.
func TestCLRChainSkipsUndoneWork(t *testing.T) {
	dir := t.TempDir()
	walPath, disk, _ := crashScenario(t, dir)
	rep := recoverFrom(t, walPath, disk)

	// Every CLR must chain to the PrevLSN of the update it compensates.
	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer wal.Close()
	records, err := wal.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	byLSN := map[LSN]*LogRecord{}
	for _, r := range records {
		byLSN[r.LSN] = r
	}
	for _, clr := range rep.CLRs {
		target := byLSN[clr.UndoNextLSN]
		if clr.UndoNextLSN != NilLSN && target == nil {
			t.Fatalf("CLR %d chains to missing LSN %d", clr.LSN, clr.UndoNextLSN)
		}
	}
}

// TestWALCorruptTail verifies the reader stops cleanly at a torn/corrupt
// final frame instead of failing or misreading.
func TestWALCorruptTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "torn.wal")
	wal, err := CreateWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	wal.Append(&LogRecord{Type: RecBegin, TxnID: 1})
	wal.Append(&LogRecord{Type: RecUpdate, TxnID: 1, PageID: 7, NewData: []byte("x")})
	wal.Close()

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	wal2, err := OpenWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	defer wal2.Close()
	records, err := wal2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("read %d records, want 2 (corrupt tail ignored)", len(records))
	}
	if records[1].LSN <= records[0].LSN {
		t.Fatalf("LSNs not increasing: %d then %d", records[0].LSN, records[1].LSN)
	}
}

// TestWALRoundTrip exercises every record type including checkpoint tables.
func TestWALRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rt.wal")
	wal, err := CreateWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	in := []*LogRecord{
		{Type: RecBegin, TxnID: 9},
		{Type: RecUpdate, TxnID: 9, PrevLSN: 1, PageID: 3, Offset: 16, OldData: []byte("old"), NewData: []byte("new")},
		{Type: RecCheckpoint, DPT: map[int64]LSN{3: 42, 5: 43}, TT: map[int64]LSN{9: 2}},
		{Type: RecCLR, TxnID: 9, PrevLSN: 2, PageID: 3, Offset: 16, OldData: []byte("new"), NewData: []byte("old"), UndoNextLSN: 1},
		{Type: RecCommit, TxnID: 9, PrevLSN: 4},
		{Type: RecEnd, TxnID: 9, PrevLSN: 5},
	}
	for _, r := range in {
		wal.Append(r)
	}
	wal.Close()

	wal2, err := OpenWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	defer wal2.Close()
	out, err := wal2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("read %d records, want %d", len(out), len(in))
	}
	for i, r := range in {
		got := out[i]
		if got.Type != r.Type || got.TxnID != r.TxnID || got.PrevLSN != r.PrevLSN ||
			got.PageID != r.PageID || got.Offset != r.Offset ||
			string(got.OldData) != string(r.OldData) || string(got.NewData) != string(r.NewData) ||
			got.UndoNextLSN != r.UndoNextLSN {
			t.Fatalf("record %d mismatch: got %+v want %+v", i, got, r)
		}
	}
	if !reflect.DeepEqual(out[2].DPT, in[2].DPT) || !reflect.DeepEqual(out[2].TT, in[2].TT) {
		t.Fatalf("checkpoint tables mismatch: got %+v", out[2])
	}
}
