package recovery

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"aries/storage"
	"aries/wal"
)

func setup(t *testing.T) (string, *wal.Injector) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ariestest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "wal.log"), wal.NewInjector()
}

func pageData(s *storage.PageStore, id uint32, n int) []byte {
	p := s.Fetch(id)
	return p.Data[:n]
}

func assertNoGoroutineLeak(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}

// TestRecoverCheckpointThenCrash is the acceptance scenario: 2 committed
// and 3 uncommitted transactions, a checkpoint in the middle, then a
// crash. Recovery must redo the committed work and undo the rest.
func TestRecoverCheckpointThenCrash(t *testing.T) {
	path, _ := setup(t)
	w, err := wal.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewPageStore()
	tm := NewTxnManager(w, store)

	// txn1 commits BEFORE the checkpoint; its page is flushed to disk.
	txn1 := tm.Begin()
	if err := tm.Update(txn1, 1, 0, []byte("AAAA")); err != nil {
		t.Fatal(err)
	}
	if err := tm.Commit(txn1); err != nil {
		t.Fatal(err)
	}
	cpLSN, err := tm.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}

	// txn2 commits AFTER the checkpoint; its dirty page is only in the
	// volatile buffer pool and will be lost in the crash.
	txn2 := tm.Begin()
	if err := tm.Update(txn2, 2, 0, []byte("BBBB")); err != nil {
		t.Fatal(err)
	}
	if err := tm.Commit(txn2); err != nil {
		t.Fatal(err)
	}

	// txn3, txn4, txn5 never commit.
	losers := make([]uint64, 0, 3)
	for i, page := range []uint32{3, 4, 5} {
		txn := tm.Begin()
		if err := tm.Update(txn, page, 0, []byte{byte('C' + i), byte('C' + i), byte('C' + i), byte('C' + i)}); err != nil {
			t.Fatal(err)
		}
		losers = append(losers, txn)
	}

	// Log is durable, dirty pages are not. Power goes out.
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	store.Crash()
	w.Close()

	// --- Recovery ---
	w2, err := wal.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	rm := &RecoveryManager{Wal: w2, Store: store}
	rep, err := rm.Recover()
	if err != nil {
		t.Fatal(err)
	}

	// Analysis: checkpoint found, exactly the 3 uncommitted txns are losers.
	if rep.Analysis.CheckpointLSN != cpLSN {
		t.Fatalf("checkpoint LSN=%d, want %d", rep.Analysis.CheckpointLSN, cpLSN)
	}
	if len(rep.Losers) != 3 {
		t.Fatalf("losers=%v, want 3 uncommitted txns", rep.Losers)
	}
	for i, want := range losers {
		if rep.Losers[i] != want {
			t.Fatalf("loser[%d]=%d, want %d", i, rep.Losers[i], want)
		}
	}
	for _, page := range []uint32{2, 3, 4, 5} {
		if _, ok := rep.Analysis.DPT[page]; !ok {
			t.Fatalf("page %d missing from dirty page table", page)
		}
	}

	// Redo repeated history for the pages lost from the buffer pool.
	if rep.RedoApplied != 4 { // txn2's update + 3 loser updates
		t.Fatalf("RedoApplied=%d, want 4", rep.RedoApplied)
	}
	// Undo compensated exactly the 3 loser updates.
	if rep.UndoCLRs != 3 {
		t.Fatalf("UndoCLRs=%d, want 3", rep.UndoCLRs)
	}

	// Committed data persisted, uncommitted data rolled back.
	if got := pageData(store, 1, 4); !bytes.Equal(got, []byte("AAAA")) {
		t.Fatalf("page1=%q, want AAAA (committed before checkpoint)", got)
	}
	if got := pageData(store, 2, 4); !bytes.Equal(got, []byte("BBBB")) {
		t.Fatalf("page2=%q, want BBBB (committed after checkpoint, redone)", got)
	}
	for _, page := range []uint32{3, 4, 5} {
		if got := pageData(store, page, 4); !bytes.Equal(got, []byte{0, 0, 0, 0}) {
			t.Fatalf("page%d=%v, want zeros (rolled back)", page, got)
		}
	}

	// Every transaction is finished: re-analysis finds an empty txn table.
	res2 := rm.Analyze(w2.Records())
	if len(res2.Txns) != 0 {
		t.Fatalf("transaction table not empty after recovery: %v", res2.Txns)
	}
	w2.Close()

	// Recovery is idempotent: a second run changes nothing.
	w3, err := wal.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	rm3 := &RecoveryManager{Wal: w3, Store: store}
	rep3, err := rm3.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if rep3.RedoApplied != 0 || rep3.UndoCLRs != 0 || len(rep3.Losers) != 0 {
		t.Fatalf("second recovery not a no-op: %+v", rep3)
	}
	if got := pageData(store, 2, 4); !bytes.Equal(got, []byte("BBBB")) {
		t.Fatalf("page2=%q after second recovery, want BBBB", got)
	}
	w3.Close()
}

// TestCrashDuringUndo resumes an interrupted undo: the WAL already
// contains a CLR, and recovery must follow UndoNextLSN instead of
// undoing the same update twice.
func TestCrashDuringUndo(t *testing.T) {
	path, _ := setup(t)
	w, err := wal.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewPageStore()
	tm := NewTxnManager(w, store)

	txn := tm.Begin()
	tm.Update(txn, 1, 0, []byte("XX"))
	tm.Update(txn, 1, 8, []byte("YY"))

	// Emulate an abort that crashes halfway: ABORT + one CLR (undoing the
	// second update), then power loss before the first update is undone.
	tm.mu.Lock()
	prev := tm.prevLSN[txn]
	tm.mu.Unlock()
	abortLSN := w.Append(&wal.Record{Type: wal.TypeAbort, TxnID: txn, PrevLSN: prev})
	lastUpdate := w.Record(prev)
	w.Append(&wal.Record{
		Type: wal.TypeCLR, TxnID: txn, PrevLSN: abortLSN,
		PageID: lastUpdate.PageID, Offset: lastUpdate.Offset,
		OldData: lastUpdate.NewData, NewData: lastUpdate.OldData,
		UndoNextLSN: lastUpdate.PrevLSN,
	})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	store.Crash()
	w.Close()

	w2, err := wal.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	rm := &RecoveryManager{Wal: w2, Store: store}
	rep, err := rm.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if rep.UndoCLRs != 1 {
		t.Fatalf("UndoCLRs=%d, want 1 (only the not-yet-undone update)", rep.UndoCLRs)
	}
	if got := pageData(store, 1, 16); !bytes.Equal(got, make([]byte, 16)) {
		t.Fatalf("page1=%v, want fully rolled back", got)
	}
	// Redo must have replayed the CLR too (updates + CLR = 3 records).
	if rep.RedoApplied != 3 {
		t.Fatalf("RedoApplied=%d, want 3 (2 updates + 1 CLR)", rep.RedoApplied)
	}
	w2.Close()
}

// TestDiskFullCommitFailsThenHeals verifies that a commit hitting a full
// disk fails cleanly (transaction aborted, old value intact) and that the
// engine keeps working once space is available again.
func TestDiskFullCommitFailsThenHeals(t *testing.T) {
	path, inj := setup(t)
	w, err := wal.Open(path, inj)
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewPageStore()
	tm := NewTxnManager(w, store)

	txn1 := tm.Begin()
	tm.Update(txn1, 1, 0, []byte("AA"))
	inj.Arm(wal.FaultDiskFull)
	if err := tm.Commit(txn1); err != wal.ErrDiskFull {
		t.Fatalf("commit on full disk: want ErrDiskFull, got %v", err)
	}
	inj.Clear() // operator frees space
	if err := tm.Abort(txn1); err != nil {
		t.Fatalf("abort after failed commit: %v", err)
	}

	// The engine heals itself: a new transaction commits fine.
	txn2 := tm.Begin()
	tm.Update(txn2, 1, 0, []byte("BB"))
	if err := tm.Commit(txn2); err != nil {
		t.Fatalf("commit after healing: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	store.Crash()
	w.Close()

	w2, err := wal.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	rm := &RecoveryManager{Wal: w2, Store: store}
	if _, err := rm.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := pageData(store, 1, 2); !bytes.Equal(got, []byte("BB")) {
		t.Fatalf("page1=%q, want BB (aborted txn left no trace)", got)
	}
	w2.Close()
}

// TestPowerLossDropsUncommittedTail verifies that records lost in a power
// failure (including a commit that was acknowledged but never reached the
// disk) leave no trace after recovery.
func TestPowerLossDropsUncommittedTail(t *testing.T) {
	path, inj := setup(t)
	w, err := wal.Open(path, inj)
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewPageStore()
	tm := NewTxnManager(w, store)

	txn1 := tm.Begin()
	tm.Update(txn1, 1, 0, []byte("PP"))
	if err := tm.Commit(txn1); err != nil {
		t.Fatal(err)
	}

	inj.Arm(wal.FaultPowerLoss)
	txn2 := tm.Begin()
	tm.Update(txn2, 2, 0, []byte("QQ"))
	// The commit "succeeds" from the client's point of view, but the OS
	// page cache is vaporized by the power loss before it hits the disk.
	if err := tm.Commit(txn2); err != nil {
		t.Fatal(err)
	}
	inj.Clear()
	store.Crash()
	w.Close()

	w2, err := wal.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	rm := &RecoveryManager{Wal: w2, Store: store}
	rep, err := rm.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Losers) != 0 {
		t.Fatalf("txn2 vanished with the power loss; losers=%v", rep.Losers)
	}
	if got := pageData(store, 1, 2); !bytes.Equal(got, []byte("PP")) {
		t.Fatalf("page1=%q, want PP (durable commit survived)", got)
	}
	if got := pageData(store, 2, 2); !bytes.Equal(got, []byte{0, 0}) {
		t.Fatalf("page2=%q, want zeros (lost commit left no trace)", got)
	}
	w2.Close()
}

// TestLogShippingReplicaRecovery ships the log over a lossy network link
// and verifies the replica recovers to the exact same state.
func TestLogShippingReplicaRecovery(t *testing.T) {
	dir, err := os.MkdirTemp("", "ariesship")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	primary, err := wal.Open(filepath.Join(dir, "primary.log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	pStore := storage.NewPageStore()
	tm := NewTxnManager(primary, pStore)
	for i := uint32(1); i <= 3; i++ {
		txn := tm.Begin()
		tm.Update(txn, i, 0, []byte{byte('0' + i), byte('0' + i)})
		if err := tm.Commit(txn); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tm.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	txn4 := tm.Begin() // uncommitted at crash time
	tm.Update(txn4, 4, 0, []byte("ZZ"))
	if err := primary.Flush(); err != nil {
		t.Fatal(err)
	}

	replica, err := wal.Open(filepath.Join(dir, "replica.log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	recv := wal.NewReceiver(replica)
	link := wal.NewLossyLink(40)
	ship := &wal.Shipper{Source: primary, Link: link, MaxRetries: 200}
	if err := ship.Sync(recv); err != nil {
		t.Fatalf("log shipping over lossy link: %v", err)
	}
	if link.Dropped == 0 {
		t.Fatal("test is meaningless unless packets were actually dropped")
	}
	if err := replica.Flush(); err != nil {
		t.Fatal(err)
	}

	// Both sides crash, both recover.
	pStore.Crash()
	rStore := storage.NewPageStore()
	primary.Close()
	replica.Close()

	pWal, _ := wal.Open(filepath.Join(dir, "primary.log"), nil)
	rWal, _ := wal.Open(filepath.Join(dir, "replica.log"), nil)
	if _, err := (&RecoveryManager{Wal: pWal, Store: pStore}).Recover(); err != nil {
		t.Fatal(err)
	}
	if _, err := (&RecoveryManager{Wal: rWal, Store: rStore}).Recover(); err != nil {
		t.Fatal(err)
	}
	for page := uint32(1); page <= 4; page++ {
		want := pageData(pStore, page, 16)
		got := pageData(rStore, page, 16)
		if !bytes.Equal(got, want) {
			t.Fatalf("page %d diverged: replica=%v primary=%v", page, got, want)
		}
	}
	pWal.Close()
	rWal.Close()
}
