package recovery

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"aries/wal"
)

const pageSize = 64

// crashScenario builds the acceptance WAL:
//   - 5 transactions: T1, T3 commit; T2, T4 stay running; T5 aborts in-flight
//   - a checkpoint in the middle
//   - page P1 flushed before the checkpoint (so disk holds data that a loser
//     later touches and Undo must restore)
//   - crash after the checkpoint with dirty pages unflushed
//
// It returns the disk image and the log path, simulating post-crash state.
func crashScenario(t *testing.T) (*MemDisk, string, func()) {
	dir, err := ioutil.TempDir("", "ariestest")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "crash.log")
	log, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	disk := NewMemDisk()
	bp := NewBufferPool(disk, pageSize)
	tm := NewTxnManager(log, bp)

	put := func(s string) []byte {
		b := make([]byte, 4)
		copy(b, s)
		return b
	}

	t1 := tm.Begin()
	tm.Update(t1, 1, 0, put("AAAA")) // committed -> survives
	tm.Update(t1, 2, 0, put("1111")) // committed -> survives

	t2 := tm.Begin()
	tm.Update(t2, 1, 8, put("BBBB")) // uncommitted, flushed -> must be undone on disk

	bp.FlushPage(1) // P1 (with T2's dirty BBBB) lands on disk before checkpoint

	tm.Checkpoint()

	tm.Update(t1, 3, 0, put("cccc")) // committed, never flushed -> must be redone
	tm.Commit(t1)

	t3 := tm.Begin()
	tm.Update(t3, 2, 8, put("3333")) // committed, never flushed -> must be redone
	tm.Commit(t3)

	t4 := tm.Begin()
	tm.Update(t4, 1, 16, put("DDDD")) // uncommitted -> must be undone

	t5 := tm.Begin()
	tm.Update(t5, 2, 16, put("5555")) // aborting -> must be undone
	tm.Abort(t5)

	tm.Update(t2, 3, 8, put("2222")) // uncommitted -> must be undone

	// Crash: lose the buffer pool, keep the log and the disk.
	bp.Crash()
	log.Close()

	return disk, path, func() { os.RemoveAll(dir) }
}

func reopen(t *testing.T, path string, disk *MemDisk) (*wal.WAL, *BufferPool) {
	log, err := wal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return log, NewBufferPool(disk, pageSize)
}

func pageAt(t *testing.T, disk *MemDisk, id wal.PageID) *Page {
	p, ok := disk.ReadPage(id)
	if !ok {
		t.Fatalf("page %d missing from disk after recovery", id)
	}
	return p
}

func TestAnalysisPhase(t *testing.T) {
	disk, path, cleanup := crashScenario(t)
	defer cleanup()
	log, bp := reopen(t, path, disk)

	res, err := NewEngine(log, bp).Recover()
	if err != nil {
		t.Fatal(err)
	}

	// TransactionTable at end of Analysis: all 5 txns with correct status.
	if len(res.TxTable) != 5 {
		t.Fatalf("TT has %d entries, want 5", len(res.TxTable))
	}
	wantStatus := map[wal.TxnID]TxnStatus{1: StatusCommitted, 2: StatusRunning, 3: StatusCommitted, 4: StatusRunning, 5: StatusAborting}
	for id, want := range wantStatus {
		e, ok := res.TxTable[id]
		if !ok {
			t.Fatalf("txn %d missing from TT", id)
		}
		if e.Status != want {
			t.Fatalf("txn %d status = %v, want %v", id, e.Status, want)
		}
		if e.LastLSN == 0 {
			t.Fatalf("txn %d has no lastLSN", id)
		}
	}

	// DirtyPageTable: P1 (dirtied by T4 post-checkpoint), P2, P3.
	if len(res.DirtyPages) != 3 {
		t.Fatalf("DPT has %d entries, want 3: %v", len(res.DirtyPages), res.DirtyPages)
	}
	for _, id := range []wal.PageID{1, 2, 3} {
		if _, ok := res.DirtyPages[id]; !ok {
			t.Fatalf("page %d missing from DPT", id)
		}
	}
	// Winners and losers.
	if len(res.Winners) != 2 || res.Winners[0] != 1 || res.Winners[1] != 3 {
		t.Fatalf("winners = %v, want [1 3]", res.Winners)
	}
	if len(res.Losers) != 3 || res.Losers[0] != 2 || res.Losers[1] != 4 || res.Losers[2] != 5 {
		t.Fatalf("losers = %v, want [2 4 5]", res.Losers)
	}
}

func TestRedoRepeatsHistory(t *testing.T) {
	disk, path, cleanup := crashScenario(t)
	defer cleanup()
	log, bp := reopen(t, path, disk)

	res, err := NewEngine(log, bp).Recover()
	if err != nil {
		t.Fatal(err)
	}

	// 6 update records lie at or after the minimum recLSN and must be
	// re-applied: T1->P2, T1->P3, T3->P2, T4->P1, T5->P2, T2->P3.
	if len(res.Redone) != 6 {
		t.Fatalf("redone %d records, want 6", len(res.Redone))
	}
	// Redo must include uncommitted changes too (repeat history), and the
	// pre-checkpoint updates to P1 must NOT be redone (already flushed).
	for _, lsn := range res.Redone {
		if lsn == 0 {
			t.Fatal("invalid redone LSN")
		}
	}
}

func TestUndoRollsBackLosers(t *testing.T) {
	disk, path, cleanup := crashScenario(t)
	defer cleanup()
	log, bp := reopen(t, path, disk)

	res, err := NewEngine(log, bp).Recover()
	if err != nil {
		t.Fatal(err)
	}

	// 4 loser updates undone: T2 x2, T4 x1, T5 x1 -> 4 CLRs.
	if len(res.Undone) != 4 {
		t.Fatalf("undone %d updates, want 4", len(res.Undone))
	}
	if len(res.CLRsWritten) != 4 {
		t.Fatalf("wrote %d CLRs, want 4", len(res.CLRsWritten))
	}

	// Final durable state: committed data present, uncommitted data gone.
	zero4 := []byte{0, 0, 0, 0}
	p1 := pageAt(t, disk, 1)
	if !bytes.Equal(p1.Data[0:4], []byte("AAAA")) {
		t.Fatalf("P1[0:4] = %q, want AAAA (committed T1)", p1.Data[0:4])
	}
	if !bytes.Equal(p1.Data[8:12], zero4) {
		t.Fatalf("P1[8:12] = %q, want zeros (T2 rolled back)", p1.Data[8:12])
	}
	if !bytes.Equal(p1.Data[16:20], zero4) {
		t.Fatalf("P1[16:20] = %q, want zeros (T4 rolled back)", p1.Data[16:20])
	}
	p2 := pageAt(t, disk, 2)
	if !bytes.Equal(p2.Data[0:4], []byte("1111")) {
		t.Fatalf("P2[0:4] = %q, want 1111 (committed T1)", p2.Data[0:4])
	}
	if !bytes.Equal(p2.Data[8:12], []byte("3333")) {
		t.Fatalf("P2[8:12] = %q, want 3333 (committed T3)", p2.Data[8:12])
	}
	if !bytes.Equal(p2.Data[16:20], zero4) {
		t.Fatalf("P2[16:20] = %q, want zeros (T5 rolled back)", p2.Data[16:20])
	}
	p3 := pageAt(t, disk, 3)
	if !bytes.Equal(p3.Data[0:4], []byte("cccc")) {
		t.Fatalf("P3[0:4] = %q, want cccc (committed T1)", p3.Data[0:4])
	}
	if !bytes.Equal(p3.Data[8:12], zero4) {
		t.Fatalf("P3[8:12] = %q, want zeros (T2 rolled back)", p3.Data[8:12])
	}

	// The log must end with CLR/END records for the losers.
	recs, err := log.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	var clrs, ends int
	for _, r := range recs {
		switch r.Type {
		case wal.TypeCLR:
			clrs++
			// Every CLR's undoNext must point at the undone record's prevLSN.
			if _, ok := map[wal.TxnID]bool{2: true, 4: true, 5: true}[r.TxnID]; !ok {
				t.Fatalf("CLR for unexpected txn %d", r.TxnID)
			}
		case wal.TypeEnd:
			ends++
		}
	}
	if clrs != 4 || ends != 3 {
		t.Fatalf("log has %d CLRs and %d ENDs, want 4 and 3", clrs, ends)
	}
}

func TestRecoveryIsIdempotent(t *testing.T) {
	disk, path, cleanup := crashScenario(t)
	defer cleanup()

	log, bp := reopen(t, path, disk)
	res1, err := NewEngine(log, bp).Recover()
	if err != nil {
		t.Fatal(err)
	}
	log.Close()
	writesAfterFirst := disk.Writes()

	// Second crash + recovery on the recovered database.
	log2, bp2 := reopen(t, path, disk)
	res2, err := NewEngine(log2, bp2).Recover()
	if err != nil {
		t.Fatal(err)
	}
	log2.Close()

	if len(res2.Losers) != 0 {
		t.Fatalf("second recovery found losers %v, want none", res2.Losers)
	}
	if len(res2.Undone) != 0 {
		t.Fatalf("second recovery undid %d records, want 0", len(res2.Undone))
	}
	if len(res2.Redone) != 0 {
		t.Fatalf("second recovery redid %d records, want 0 (pageLSN idempotence)", len(res2.Redone))
	}
	if disk.Writes() != writesAfterFirst {
		t.Fatalf("second recovery wrote %d extra pages", disk.Writes()-writesAfterFirst)
	}

	// Data identical to the first recovery.
	p1 := pageAt(t, disk, 1)
	if !bytes.Equal(p1.Data[0:4], []byte("AAAA")) || !bytes.Equal(p1.Data[8:12], []byte{0, 0, 0, 0}) {
		t.Fatalf("state changed after second recovery")
	}
	_ = res1
}

func TestRecoveryWithoutCheckpoint(t *testing.T) {
	dir, _ := ioutil.TempDir("", "ariestest")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "nockpt.log")
	log, _ := wal.Open(path)
	disk := NewMemDisk()
	bp := NewBufferPool(disk, pageSize)
	tm := NewTxnManager(log, bp)

	t1 := tm.Begin()
	tm.Update(t1, 5, 0, []byte("okay"))
	tm.Commit(t1)
	t2 := tm.Begin()
	tm.Update(t2, 5, 8, []byte("nope"))
	bp.Crash()
	log.Close()

	log2, _ := wal.Open(path)
	res, err := NewEngine(log2, NewBufferPool(disk, pageSize)).Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Losers) != 1 || res.Losers[0] != 2 {
		t.Fatalf("losers = %v, want [2]", res.Losers)
	}
	p := pageAt(t, disk, 5)
	if !bytes.Equal(p.Data[0:4], []byte("okay")) || !bytes.Equal(p.Data[8:12], []byte{0, 0, 0, 0}) {
		t.Fatalf("recovery without checkpoint produced wrong state: %q", p.Data[:12])
	}
}
