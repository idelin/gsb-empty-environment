package recovery_test

import (
	"bytes"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"aries/db"
	"aries/recovery"
	"aries/storage"
	"aries/wal"
)

func pageVal(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, storage.PageSize)
}

func zeroPage() []byte { return make([]byte, storage.PageSize) }

func openDB(t *testing.T) (*db.DB, *storage.Disk, *wal.WAL) {
	t.Helper()
	w, err := wal.Open(filepath.Join(t.TempDir(), "test.wal"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	disk := storage.NewDisk()
	return db.New(w, disk), disk, w
}

// TestEndToEndCrashRecovery is the acceptance scenario: a WAL with
// 3 uncommitted and 2 committed transactions, a checkpoint, then a
// crash. Recovery must roll back the uncommitted changes and persist
// the committed ones.
func TestEndToEndCrashRecovery(t *testing.T) {
	database, disk, w := openDB(t)

	// Three transactions that will never commit.
	database.Update(1, 10, pageVal(0xA1))
	database.Update(1, 11, pageVal(0xA1))
	database.Update(2, 11, pageVal(0xB2))
	// Two transactions that commit before the checkpoint.
	database.Update(4, 12, pageVal(0xD4))
	database.Commit(4)
	database.Update(5, 13, pageVal(0xE5))
	database.Commit(5)

	ckptLSN := database.Checkpoint()

	// More uncommitted work after the checkpoint, including a page
	// also written by a committed transaction.
	database.Update(3, 10, pageVal(0xC3))
	database.Update(3, 13, pageVal(0xC3))

	// Steal: force dirty pages (incl. uncommitted data) to disk.
	// No-force: committed page 13 is deliberately NOT flushed.
	database.Flush(10)
	database.Flush(12)

	database.Crash()

	st := recovery.NewEngine(w, disk).Recover()

	if st.CheckpointLSN != ckptLSN {
		t.Fatalf("analysis picked checkpoint LSN %d, want %d", st.CheckpointLSN, ckptLSN)
	}
	wantLosers := []uint32{1, 2, 3}
	if len(st.Losers) != 3 || st.Losers[0] != wantLosers[0] || st.Losers[1] != wantLosers[1] || st.Losers[2] != wantLosers[2] {
		t.Fatalf("losers = %v, want %v", st.Losers, wantLosers)
	}
	if st.Undone != 5 { // txn1:2, txn2:1, txn3:2 updates
		t.Fatalf("undone = %d, want 5", st.Undone)
	}
	if st.CLRsWritten != 5 {
		t.Fatalf("CLRs = %d, want 5", st.CLRsWritten)
	}
	if st.Redone == 0 {
		t.Fatal("redo applied nothing; committed page 13 needed redo")
	}

	assertPage := func(page uint32, want []byte) {
		t.Helper()
		got, ok := disk.Read(page)
		if !ok {
			t.Fatalf("page %d missing from disk", page)
		}
		if !bytes.Equal(got.Data, want) {
			t.Fatalf("page %d = %x..., want %x...", page, got.Data[:4], want[:4])
		}
	}
	// Committed data persisted.
	assertPage(12, pageVal(0xD4))
	assertPage(13, pageVal(0xE5)) // txn3's later uncommitted write undone
	// Uncommitted data rolled back.
	assertPage(10, zeroPage())
	assertPage(11, zeroPage())

	// Every loser got CLR + END records appended to the WAL.
	var clrs, ends int
	for _, r := range w.Records() {
		switch r.Type {
		case wal.TypeCLR:
			clrs++
			if r.TxnID < 1 || r.TxnID > 3 {
				t.Fatalf("CLR for unexpected txn %d", r.TxnID)
			}
		case wal.TypeEnd:
			ends++
		}
	}
	if clrs != 5 {
		t.Fatalf("WAL has %d CLRs, want 5", clrs)
	}
	if ends != 5 { // 2 commit-ends + 3 undo-ends
		t.Fatalf("WAL has %d ENDs, want 5", ends)
	}

	// Recovery is idempotent: a second pass changes nothing.
	before := disk.Snapshot()
	st2 := recovery.NewEngine(w, disk).Recover()
	if st2.Undone != 0 || st2.Redone != 0 {
		t.Fatalf("second recovery not idempotent: %+v", st2)
	}
	for id, p := range disk.Snapshot() {
		if !bytes.Equal(p.Data, before[id].Data) {
			t.Fatalf("page %d changed by second recovery", id)
		}
	}
}

func TestEmptyWAL(t *testing.T) {
	_, disk, w := openDB(t)
	st := recovery.NewEngine(w, disk).Recover()
	if st.Redone != 0 || st.Undone != 0 || len(st.Losers) != 0 {
		t.Fatalf("empty WAL: %+v", st)
	}
}

func TestNoCheckpoint(t *testing.T) {
	database, disk, w := openDB(t)
	database.Update(1, 1, pageVal(1))
	database.Update(2, 2, pageVal(2))
	database.Commit(2)
	database.Flush(2)
	database.Crash()
	st := recovery.NewEngine(w, disk).Recover()
	if st.CheckpointLSN != 0 {
		t.Fatalf("unexpected checkpoint %d", st.CheckpointLSN)
	}
	if len(st.Losers) != 1 || st.Losers[0] != 1 {
		t.Fatalf("losers = %v", st.Losers)
	}
	p, _ := disk.Read(1)
	if !bytes.Equal(p.Data, zeroPage()) {
		t.Fatal("loser update not undone")
	}
	p, _ = disk.Read(2)
	if !bytes.Equal(p.Data, pageVal(2)) {
		t.Fatal("committed update lost")
	}
}

func TestAllCommittedNoUndo(t *testing.T) {
	database, disk, w := openDB(t)
	for txn := uint32(1); txn <= 5; txn++ {
		database.Update(txn, txn, pageVal(byte(txn)))
		database.Commit(txn)
	}
	database.Checkpoint()
	database.Crash()
	st := recovery.NewEngine(w, disk).Recover()
	if st.Undone != 0 || len(st.Losers) != 0 {
		t.Fatalf("no losers expected: %+v", st)
	}
	for txn := uint32(1); txn <= 5; txn++ {
		p, _ := disk.Read(txn)
		if !bytes.Equal(p.Data, pageVal(byte(txn))) {
			t.Fatalf("committed page %d lost", txn)
		}
	}
}

// TestCrashDuringUndo: the WAL already contains a partial undo (a
// CLR) from a previous recovery attempt. Undo must resume from
// undoNext and must never undo the CLR itself.
func TestCrashDuringUndo(t *testing.T) {
	database, disk, w := openDB(t)
	database.Update(7, 70, pageVal(0x77))
	database.Update(7, 71, pageVal(0x78))
	database.Crash()

	// Simulate a first recovery attempt that crashed mid-undo: a CLR
	// for the newest update exists, but the older update is not yet
	// compensated. Recovery must resume from undoNext and must never
	// undo the CLR itself.
	last := w.LastLSN()
	upd2 := w.Get(last)
	clr := &wal.Record{
		Type:        wal.TypeCLR,
		TxnID:       7,
		PageID:      71,
		PrevLSN:     last,
		UndoNextLSN: upd2.PrevLSN,
		Before:      upd2.After,
		After:       upd2.Before,
	}
	w.Append(clr)
	disk.Write(71, upd2.Before, w.LastLSN())

	st := recovery.NewEngine(w, disk).Recover()
	if st.Undone != 1 { // only the first update remains to undo
		t.Fatalf("undone = %d, want 1 (CLR must not be re-undone)", st.Undone)
	}
	for _, page := range []uint32{70, 71} {
		p, _ := disk.Read(page)
		if !bytes.Equal(p.Data, zeroPage()) {
			t.Fatalf("page %d not fully undone", page)
		}
	}
	// Exactly one new CLR (for the first update) was written.
	nCLR := 0
	for _, r := range w.Records() {
		if r.Type == wal.TypeCLR {
			nCLR++
		}
	}
	if nCLR != 2 {
		t.Fatalf("total CLRs = %d, want 2", nCLR)
	}
}

// TestRuntimeAbortThenCrash: a transaction aborted at runtime leaves
// CLRs in the WAL; recovery after a crash must not roll those back
// again and must leave other losers handled.
func TestRuntimeAbortThenCrash(t *testing.T) {
	database, disk, w := openDB(t)
	database.Update(1, 1, pageVal(1))
	database.Abort(1)                 // runtime abort: writes CLRs + END
	database.Update(2, 2, pageVal(2)) // loser
	database.Update(3, 3, pageVal(3))
	database.Commit(3)
	database.Crash()
	st := recovery.NewEngine(w, disk).Recover()
	if len(st.Losers) != 1 || st.Losers[0] != 2 {
		t.Fatalf("losers = %v, want [2]", st.Losers)
	}
	p, _ := disk.Read(2)
	if !bytes.Equal(p.Data, zeroPage()) {
		t.Fatal("loser not undone")
	}
	p, _ = disk.Read(3)
	if !bytes.Equal(p.Data, pageVal(3)) {
		t.Fatal("committed txn lost")
	}
}

// TestRedoRespectsPageLSN: a page already flushed with a newer LSN
// must not be overwritten by redo of an older record.
func TestRedoRespectsPageLSN(t *testing.T) {
	database, disk, w := openDB(t)
	database.Update(1, 5, pageVal(0x11))
	database.Update(1, 5, pageVal(0x22))
	database.Commit(1)
	database.Flush(5)                    // disk has the newest image
	database.Update(2, 6, pageVal(0x33)) // loser elsewhere
	database.Crash()
	st := recovery.NewEngine(w, disk).Recover()
	p, _ := disk.Read(5)
	if !bytes.Equal(p.Data, pageVal(0x22)) {
		t.Fatal("redo clobbered a newer page image")
	}
	if st.Redone != 1 {
		// Page 5 is up to date and skipped; only the loser's
		// never-flushed update on page 6 is redone (repeat history),
		// then rolled back by undo.
		t.Fatalf("redone = %d, want 1", st.Redone)
	}
	p, _ = disk.Read(6)
	if !bytes.Equal(p.Data, zeroPage()) {
		t.Fatal("loser update on page 6 not undone")
	}
}

// TestConcurrentTransactions: many goroutines run transactions
// against the DB concurrently; after a crash, recovery must restore
// exactly the committed state.
func TestConcurrentTransactions(t *testing.T) {
	database, disk, w := openDB(t)
	const workers = 8
	const txnsPerWorker = 25
	committed := make(map[uint32][]byte)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < txnsPerWorker; i++ {
				txn := uint32(g*txnsPerWorker + i + 1)
				page := 1000 + txn // unique page per txn: no conflicts
				val := pageVal(byte(txn))
				database.Update(txn, page, val)
				if i%5 == 4 {
					database.Abort(txn)
					continue
				}
				database.Commit(txn)
				mu.Lock()
				committed[page] = val
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	// Genuine losers: transactions still in flight at the crash.
	database.Update(9001, 9001, pageVal(0xEE))
	database.Update(9002, 9002, pageVal(0xEF))
	database.Checkpoint()
	database.Crash()
	st := recovery.NewEngine(w, disk).Recover()
	if len(st.Losers) != 2 {
		t.Fatalf("losers = %v, want [9001 9002]", st.Losers)
	}
	snap := disk.Snapshot()
	for page, want := range committed {
		got, ok := snap[page]
		if !ok || !bytes.Equal(got.Data, want) {
			t.Fatalf("committed page %d missing/wrong after recovery", page)
		}
	}
	for page, got := range snap {
		if _, ok := committed[page]; !ok {
			if !bytes.Equal(got.Data, zeroPage()) {
				t.Fatalf("aborted txn leaked to page %d", page)
			}
		}
	}
}

// TestNoGoroutineLeak: recovery must not leave goroutines behind.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	database, disk, w := openDB(t)
	for txn := uint32(1); txn <= 10; txn++ {
		database.Update(txn, txn, pageVal(byte(txn)))
		if txn%2 == 0 {
			database.Commit(txn)
		}
	}
	database.Crash()
	recovery.NewEngine(w, disk).Recover()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
