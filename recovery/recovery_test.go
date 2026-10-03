package recovery

import (
	"strings"
	"testing"

	"aries/db"
	"aries/storage"
	"aries/wal"
)

// buildAcceptanceScenario constructs the acceptance WAL:
//   - 2 committed transactions (t1, t2)
//   - 3 uncommitted transactions (t3, t4, t5)
//   - a checkpoint taken after t3 began
//   - two page flushes that persist *uncommitted* data to disk
//   - then a crash
//
// Returns the store/disk/pool in their post-crash state.
func buildAcceptanceScenario(t *testing.T) (*wal.MemStore, *storage.MemDisk, *storage.BufferPool) {
	t.Helper()
	store := wal.NewMemStore()
	disk := storage.NewMemDisk()
	pool := storage.NewBufferPool(disk)
	d := db.New(wal.New(store, 1), pool)

	t1 := d.Begin()              // LSN 1
	must(t, d.Update(t1, 1, 10)) // 2
	must(t, d.Update(t1, 2, 20)) // 3
	must(t, d.Commit(t1))        // 4 commit, 5 end

	t2 := d.Begin()              // 6
	must(t, d.Update(t2, 3, 30)) // 7
	must(t, d.Commit(t2))        // 8, 9

	t3 := d.Begin()               // 10
	must(t, d.Update(t3, 1, 100)) // 11

	ckptLSN := d.Checkpoint() // 12
	if ckptLSN != 12 {
		t.Fatalf("checkpoint LSN = %d, want 12", ckptLSN)
	}

	must(t, d.Update(t3, 4, 40))  // 13
	t4 := d.Begin()               // 14
	must(t, d.Update(t4, 2, 200)) // 15
	t5 := d.Begin()               // 16
	must(t, d.Update(t5, 3, 300)) // 17
	must(t, d.Update(t5, 5, 50))  // 18

	// Persist uncommitted data (steal buffer policy), flushing the log
	// first per the WAL rule.
	d.FlushPage(1) // disk: page1 = 100 (uncommitted!)
	d.FlushPage(2) // disk: page2 = 200 (uncommitted!)

	d.Crash()
	return store, disk, pool
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func recoverAndFlush(t *testing.T, store *wal.MemStore, pool *storage.BufferPool, disk *storage.MemDisk, opts *Options) (*Result, *wal.WAL) {
	t.Helper()
	res, w, err := Recover(store, pool, disk, opts)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	pool.FlushAll()
	return res, w
}

// TestAcceptanceScenario is the end-to-end acceptance test: three
// uncommitted and two committed transactions, checkpoint, crash, then
// the three ARIES phases.
func TestAcceptanceScenario(t *testing.T) {
	store, disk, pool := buildAcceptanceScenario(t)

	res, _ := recoverAndFlush(t, store, pool, disk, nil)
	st := res.Stats

	// --- Analysis ---
	if st.CheckpointLSN != 12 {
		t.Errorf("checkpoint LSN = %d, want 12", st.CheckpointLSN)
	}
	wantLosers := []uint64{3, 4, 5}
	if !eqUint64s(st.Losers, wantLosers) {
		t.Errorf("losers = %v, want %v", st.Losers, wantLosers)
	}
	wantWinners := []uint64{1, 2}
	if !eqUint64s(st.Winners, wantWinners) {
		t.Errorf("winners = %v, want %v", st.Winners, wantWinners)
	}
	wantDPT := map[uint64]uint64{1: 2, 2: 3, 3: 7, 4: 13, 5: 18}
	if !eqDPT(res.DirtyPage, wantDPT) {
		t.Errorf("DPT = %v, want %v", res.DirtyPage, wantDPT)
	}

	// --- Redo ---
	if st.Redone != 4 {
		t.Errorf("redone = %d, want 4 (LSNs 7,13,17,18)", st.Redone)
	}
	if st.RedoSkipped != 4 {
		t.Errorf("redo skipped = %d, want 4 (LSNs 2,3,11,15 already on disk)", st.RedoSkipped)
	}

	// --- Undo ---
	if st.CLRsWritten != 5 {
		t.Errorf("CLRs written = %d, want 5", st.CLRsWritten)
	}
	if len(res.TxnTable) != 0 {
		t.Errorf("transaction table not empty after recovery: %v", res.TxnTable)
	}

	// --- Final durable state: committed persisted, uncommitted rolled back ---
	snap := disk.Snapshot()
	wantVals := map[uint64]int64{1: 10, 2: 20, 3: 30, 4: 0, 5: 0}
	for page, want := range wantVals {
		if got := snap[page].Value; got != want {
			t.Errorf("page %d = %d, want %d", page, got, want)
		}
	}
	// PageLSNs point at the CLRs that restored each page.
	wantLSN := map[uint64]uint64{1: 25, 2: 22, 3: 20, 4: 24, 5: 19}
	for page, want := range wantLSN {
		if got := snap[page].PageLSN; got != want {
			t.Errorf("page %d PageLSN = %d, want %d", page, got, want)
		}
	}
	// Cross-check against the independent committed-state model.
	recs, err := wal.New(store, 1).ReadAll()
	must(t, err)
	model := ExpectedCommittedState(recs)
	assertModelMatchesDisk(t, model, snap)

	// --- No leaks ---
	if dirty := pool.DirtyPages(); len(dirty) != 0 {
		t.Errorf("dirty pages leaked after flush: %v", dirty)
	}
}

// TestRedoIdempotent runs recovery twice: the second pass must be a
// no-op (history is not repeated twice, no extra CLRs).
func TestRedoIdempotent(t *testing.T) {
	store, disk, pool := buildAcceptanceScenario(t)
	recoverAndFlush(t, store, pool, disk, nil)
	before := disk.Snapshot()

	res, _ := recoverAndFlush(t, store, pool, disk, nil)
	if res.Stats.CLRsWritten != 0 {
		t.Errorf("second recovery wrote %d CLRs, want 0", res.Stats.CLRsWritten)
	}
	if len(res.Stats.Losers) != 0 {
		t.Errorf("second recovery found losers %v, want none", res.Stats.Losers)
	}
	if res.Stats.Redone != 0 {
		t.Errorf("second recovery redid %d records, want 0 (PageLSN already current)", res.Stats.Redone)
	}
	after := disk.Snapshot()
	if !eqSnap(before, after) {
		t.Errorf("disk changed across idempotent recovery: %v -> %v", before, after)
	}
}

// TestCrashDuringUndo injects a crash in the middle of the undo phase
// (after 2 CLRs are forced to stable storage) and verifies that a
// second recovery resumes undo via CLR UndoNext pointers instead of
// rolling back twice.
func TestCrashDuringUndo(t *testing.T) {
	store, disk, pool := buildAcceptanceScenario(t)

	opts := &Options{OnCLR: func(w *wal.WAL, p *storage.BufferPool, n int) bool {
		if n == 2 {
			_ = w.Flush() // make the first two CLRs durable
			return true   // then "power failure"
		}
		return false
	}}
	_, _, err := Recover(store, pool, disk, opts)
	if err != ErrCrashInjected {
		t.Fatalf("first recovery err = %v, want ErrCrashInjected", err)
	}

	res, _ := recoverAndFlush(t, store, pool, disk, nil)
	if res.Stats.CLRsWritten != 3 {
		t.Errorf("second recovery wrote %d CLRs, want 3 (2 were already durable)", res.Stats.CLRsWritten)
	}
	snap := disk.Snapshot()
	wantVals := map[uint64]int64{1: 10, 2: 20, 3: 30, 4: 0, 5: 0}
	for page, want := range wantVals {
		if got := snap[page].Value; got != want {
			t.Errorf("page %d = %d, want %d (undo resumed incorrectly)", page, got, want)
		}
	}
	recs, err2 := wal.New(store, 1).ReadAll()
	must(t, err2)
	assertModelMatchesDisk(t, ExpectedCommittedState(recs), snap)
}

// TestNoCheckpoint verifies recovery works when the log has no
// checkpoint at all (analysis scans from the beginning).
func TestNoCheckpoint(t *testing.T) {
	store := wal.NewMemStore()
	disk := storage.NewMemDisk()
	pool := storage.NewBufferPool(disk)
	d := db.New(wal.New(store, 1), pool)

	t1 := d.Begin()
	must(t, d.Update(t1, 7, 70))
	must(t, d.Commit(t1))
	t2 := d.Begin()
	must(t, d.Update(t2, 7, 700))
	d.FlushLog() // make t2's records durable before the crash
	d.Crash()

	res, _ := recoverAndFlush(t, store, pool, disk, nil)
	if res.Stats.CheckpointLSN != 0 {
		t.Errorf("checkpoint LSN = %d, want 0", res.Stats.CheckpointLSN)
	}
	if got := disk.Snapshot()[7].Value; got != 70 {
		t.Errorf("page 7 = %d, want 70", got)
	}
	if !eqUint64s(res.Stats.Losers, []uint64{2}) {
		t.Errorf("losers = %v, want [2]", res.Stats.Losers)
	}
}

// TestEmptyLog verifies recovery over an empty log is a clean no-op.
func TestEmptyLog(t *testing.T) {
	store := wal.NewMemStore()
	disk := storage.NewMemDisk()
	pool := storage.NewBufferPool(disk)
	res, _ := recoverAndFlush(t, store, pool, disk, nil)
	if res.Stats.RecordsScanned != 0 || res.Stats.Redone != 0 || res.Stats.CLRsWritten != 0 {
		t.Errorf("unexpected stats for empty log: %+v", res.Stats)
	}
	if len(disk.Snapshot()) != 0 {
		t.Errorf("disk not empty: %v", disk.Snapshot())
	}
}

// TestCorruptedLogDetected verifies checksum-based detection of a
// corrupted stable log frame (fault injection).
func TestCorruptedLogDetected(t *testing.T) {
	store, disk, pool := buildAcceptanceScenario(t)
	store.FlipByte(3, 10) // corrupt payload of the 4th stable frame
	_, _, err := Recover(store, pool, disk, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
}

// TestCommittedButNoEndRecord covers a winner whose COMMIT is durable
// but whose END record was lost in the crash: it must not be undone.
func TestCommittedButNoEndRecord(t *testing.T) {
	store := wal.NewMemStore()
	disk := storage.NewMemDisk()
	pool := storage.NewBufferPool(disk)
	w := wal.New(store, 1)
	d := db.New(w, pool)

	t1 := d.Begin()
	must(t, d.Update(t1, 1, 42))
	// Hand-craft a commit without an END record.
	must(t, d.Update(t1, 2, 7))
	w.Append(&wal.Record{Type: wal.RecCommit, TxnID: t1, PrevLSN: 3})
	must(t, w.Flush())
	t2 := d.Begin()
	must(t, d.Update(t2, 1, 99))
	d.FlushLog()
	d.Crash()

	res, _ := recoverAndFlush(t, store, pool, disk, nil)
	if !eqUint64s(res.Stats.Losers, []uint64{2}) {
		t.Errorf("losers = %v, want [2] (committed txn must not be undone)", res.Stats.Losers)
	}
	snap := disk.Snapshot()
	if got := snap[1].Value; got != 42 {
		t.Errorf("page 1 = %d, want 42", got)
	}
	if got := snap[2].Value; got != 7 {
		t.Errorf("page 2 = %d, want 7", got)
	}
}

// TestRuntimeAbortThenCrash mixes a runtime-aborted transaction (CLRs
// already in the log) with a crash, ensuring recovery neither redoes
// the aborted txn nor undoes it twice.
func TestRuntimeAbortThenCrash(t *testing.T) {
	store := wal.NewMemStore()
	disk := storage.NewMemDisk()
	pool := storage.NewBufferPool(disk)
	d := db.New(wal.New(store, 1), pool)

	t1 := d.Begin()
	must(t, d.Update(t1, 1, 1))
	must(t, d.Update(t1, 2, 2))
	must(t, d.Abort(t1)) // CLRs written at runtime
	t2 := d.Begin()
	must(t, d.Update(t2, 1, 100))
	must(t, d.Commit(t2))
	t3 := d.Begin()
	must(t, d.Update(t3, 2, 200))
	d.FlushLog()
	d.Crash()

	res, _ := recoverAndFlush(t, store, pool, disk, nil)
	snap := disk.Snapshot()
	if got := snap[1].Value; got != 100 {
		t.Errorf("page 1 = %d, want 100", got)
	}
	if got := snap[2].Value; got != 0 {
		t.Errorf("page 2 = %d, want 0 (t1 aborted, t3 uncommitted)", got)
	}
	if !eqUint64s(res.Stats.Losers, []uint64{3}) {
		t.Errorf("losers = %v, want [3]", res.Stats.Losers)
	}
	recs, err := wal.New(store, 1).ReadAll()
	must(t, err)
	assertModelMatchesDisk(t, ExpectedCommittedState(recs), snap)
}

// ---------- helpers ----------

func eqUint64s(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eqDPT(a, b map[uint64]uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

func eqSnap(a, b map[uint64]storage.PageImage) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

// assertModelMatchesDisk compares the independent committed-state model
// against the durable disk state (missing pages count as zero).
func assertModelMatchesDisk(t *testing.T, model map[uint64]int64, snap map[uint64]storage.PageImage) {
	t.Helper()
	for page, want := range model {
		if got := snap[page].Value; got != want {
			t.Errorf("page %d = %d, model wants %d", page, got, want)
		}
	}
	for page, img := range snap {
		if _, ok := model[page]; !ok && img.Value != 0 {
			t.Errorf("page %d = %d on disk but absent from committed model", page, img.Value)
		}
	}
}
