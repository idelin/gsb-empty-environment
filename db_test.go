package waldb

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// survive runs fn, converting an injected crash panic into crashed=true.
func survive(fn func()) (crashed bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(crashPanic); ok {
				crashed = true
				return
			}
			panic(r)
		}
	}()
	fn()
	return false
}

// freshDB creates a database with deterministic original page values.
func freshDB(t *testing.T) (*DB, []int64) {
	t.Helper()
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := make([]int64, NumPages)
	for i := range original {
		original[i] = int64(i * 100)
		if err := db.InitPage(uint32(i), original[i]); err != nil {
			t.Fatal(err)
		}
	}
	return db, original
}

// recoverUntilClean recovers, tolerating injected crashes during
// recovery itself, until one recovery run completes.
func recoverUntilClean(t *testing.T, db *DB) {
	t.Helper()
	for survive(func() { db.Recover() }) {
		db.Crash()
	}
}

// runWorkload executes a deterministic mix of committed, aborted and
// never-committed transactions. The independent checker derives the
// committed set from the durable log, so it stays correct no matter
// where a crash interrupts the workload.
func runWorkload(t *testing.T, db *DB) {
	t.Helper()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	t1 := db.Begin()
	must(db.Update(t1, 1, 10))
	must(db.Update(t1, 2, 20))
	must(db.Commit(t1))

	t2 := db.Begin()
	must(db.Update(t2, 2, 5))
	must(db.Update(t2, 4, 44))
	must(db.Abort(t2))

	db.Checkpoint()

	t3 := db.Begin()
	must(db.Update(t3, 3, 33))
	must(db.Update(t3, 3, 7))
	must(db.Commit(t3))

	t4 := db.Begin() // never commits; dirty page is stolen to disk
	must(db.Update(t4, 1, 1000))
	must(db.Update(t4, 5, 500))
	must(db.FlushPages())
}

func TestBasicRecovery(t *testing.T) {
	db, original := freshDB(t)
	runWorkload(t, db)
	db.Crash()
	recoverUntilClean(t, db)
	if err := CheckRecovered(db, original); err != nil {
		t.Fatal(err)
	}
	// Recovery must be idempotent: crash again, recover again.
	db.Crash()
	recoverUntilClean(t, db)
	if err := CheckRecovered(db, original); err != nil {
		t.Fatal("after repeated recovery:", err)
	}
}

func TestExplicitAbort(t *testing.T) {
	db, original := freshDB(t)
	txn := db.Begin()
	for p := uint32(0); p < 8; p++ {
		if err := db.Update(txn, p, 999); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.FlushPages(); err != nil { // steal uncommitted pages
		t.Fatal(err)
	}
	if err := db.Abort(txn); err != nil {
		t.Fatal(err)
	}
	if err := db.FlushPages(); err != nil {
		t.Fatal(err)
	}
	db.Crash()
	recoverUntilClean(t, db)
	if err := CheckRecovered(db, original); err != nil {
		t.Fatal(err)
	}
}

// TestCrashAtEveryExecPoint crashes the workload once at every
// enumerated normal-execution exit point, then recovers and verifies.
func TestCrashAtEveryExecPoint(t *testing.T) {
	for _, pt := range ExecCrashPoints {
		t.Run(pt.String(), func(t *testing.T) {
			db, original := freshDB(t)
			db.inj.arm(pt)
			crashed := survive(func() { runWorkload(t, db) })
			if crashed {
				db.Crash()
			} else {
				db.Crash() // clean run; still verify recovery path
			}
			db.inj.reset()
			recoverUntilClean(t, db)
			if err := CheckRecovered(db, original); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestCrashAtEveryRecoveryPoint snapshots a crashed database, then
// for every recovery exit point restores the snapshot, crashes
// recovery at that point, recovers again and verifies.
func TestCrashAtEveryRecoveryPoint(t *testing.T) {
	db, original := freshDB(t)
	runWorkload(t, db)
	db.Crash()
	walSnap, err := os.ReadFile(filepath.Join(db.dir, "db.wal"))
	if err != nil {
		t.Fatal(err)
	}
	dataSnap, err := os.ReadFile(filepath.Join(db.dir, "db.data"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pt := range RecoveryCrashPoints {
		t.Run(pt.String(), func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(db.dir, "db.wal"), walSnap, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(db.dir, "db.data"), dataSnap, 0o644); err != nil {
				t.Fatal(err)
			}
			db.Crash()
			db.inj.reset()
			db.inj.arm(pt)
			survive(func() { db.Recover() }) // may crash mid-recovery
			db.Crash()
			db.inj.reset()
			recoverUntilClean(t, db)
			if err := CheckRecovered(db, original); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRepeatedRecovery recovers several times in a row; each run must
// be a no-op after the first.
func TestRepeatedRecovery(t *testing.T) {
	db, original := freshDB(t)
	runWorkload(t, db)
	db.Crash()
	for i := 0; i < 3; i++ {
		recoverUntilClean(t, db)
		if err := CheckRecovered(db, original); err != nil {
			t.Fatalf("recovery run %d: %v", i, err)
		}
		db.Crash()
	}
}

// TestCLRFlushedPageNotModified: the CLR is durable but the crash
// happens before the undo touches the page (whose uncommitted image
// was already stolen to disk). Redo of the CLR must restore it.
func TestCLRFlushedPageNotModified(t *testing.T) {
	db, original := freshDB(t)
	txn := db.Begin()
	if err := db.Update(txn, 4, 50); err != nil {
		t.Fatal(err)
	}
	if err := db.FlushPages(); err != nil { // uncommitted image durable
		t.Fatal(err)
	}
	db.inj.Hook = func(pt CrashPoint) {
		if pt == PtAbortUndo {
			db.FlushLog() // CLR durable...
		}
	}
	db.inj.arm(PtAbortUndo) // ...crash before the page is fixed
	if !survive(func() { db.Abort(txn) }) {
		t.Fatal("expected injected crash")
	}
	db.Crash()
	db.inj.reset()
	recoverUntilClean(t, db)
	if err := CheckRecovered(db, original); err != nil {
		t.Fatal(err)
	}
	v, _, err := db.ReadPage(4)
	if err != nil {
		t.Fatal(err)
	}
	if v != original[4] {
		t.Fatalf("page 4 = %d, want original %d", v, original[4])
	}
}

// TestPageModifiedEndNotWritten: commit record and the dirty page are
// both durable, but the crash happens before the end record is
// written. The transaction must still count as committed.
func TestPageModifiedEndNotWritten(t *testing.T) {
	db, original := freshDB(t)
	txn := db.Begin()
	if err := db.Update(txn, 7, 9); err != nil {
		t.Fatal(err)
	}
	db.inj.Hook = func(pt CrashPoint) {
		if pt == PtCommitFlushed {
			db.FlushPages() // page durable right after the commit record
		}
	}
	db.inj.arm(PtEndLogged) // crash with end only in the volatile tail
	if !survive(func() { db.Commit(txn) }) {
		t.Fatal("expected injected crash")
	}
	db.Crash()
	db.inj.reset()
	recoverUntilClean(t, db)
	if err := CheckRecovered(db, original); err != nil {
		t.Fatal(err)
	}
	v, _, err := db.ReadPage(7)
	if err != nil {
		t.Fatal(err)
	}
	if v != original[7]+9 {
		t.Fatalf("page 7 = %d, want %d (committed effect must survive)", v, original[7]+9)
	}
}

// TestIncompleteTailRecord: a torn record at the end of the log is
// ignored, and a torn commit means the transaction never happened.
func TestIncompleteTailRecord(t *testing.T) {
	db, original := freshDB(t)
	txn := db.Begin()
	if err := db.Update(txn, 9, 42); err != nil {
		t.Fatal(err)
	}
	db.FlushLog() // update durable
	// Torn commit record: only a prefix becomes durable.
	size, err := db.wal.Size()
	if err != nil {
		t.Fatal(err)
	}
	rec := &LogRecord{Type: RecCommit, TxnID: txn, PrevLSN: 2, LSN: 3}
	enc := encodeRecord(rec)
	if err := db.wal.WriteAt(enc[:len(enc)-3], size); err != nil {
		t.Fatal(err)
	}
	if err := db.wal.Sync(); err != nil {
		t.Fatal(err)
	}
	db.Crash()
	recoverUntilClean(t, db)
	// The commit never persisted, so the update must be undone.
	if err := CheckRecovered(db, original); err != nil {
		t.Fatal(err)
	}
	v, _, err := db.ReadPage(9)
	if err != nil {
		t.Fatal(err)
	}
	if v != original[9] {
		t.Fatalf("page 9 = %d, want original %d (torn commit must not count)", v, original[9])
	}
	// Log must be clean after recovery truncated the torn tail.
	if err := CheckLog(db); err != nil {
		t.Fatal(err)
	}
}

// TestPartialCheckpoint: a checkpoint torn in half by a crash during
// its flush is ignored; recovery falls back to the older checkpoint.
func TestPartialCheckpoint(t *testing.T) {
	db, original := freshDB(t)
	t1 := db.Begin()
	if err := db.Update(t1, 2, 20); err != nil {
		t.Fatal(err)
	}
	if err := db.Commit(t1); err != nil {
		t.Fatal(err)
	}
	db.Checkpoint() // good checkpoint

	t2 := db.Begin()
	if err := db.Update(t2, 3, 30); err != nil {
		t.Fatal(err)
	}
	if err := db.Commit(t2); err != nil {
		t.Fatal(err)
	}
	// This checkpoint is only half written to durable storage.
	db.wal.Truncator = func(region string, data []byte) []byte {
		if region == "wal" {
			return data[:len(data)/2]
		}
		return data
	}
	db.Checkpoint()
	db.wal.Truncator = nil

	// t3's update stays in the volatile log tail and is lost with the
	// torn checkpoint remainder; recovery must fall back to the older
	// checkpoint and never see t3.
	t3 := db.Begin()
	if err := db.Update(t3, 2, 2000); err != nil {
		t.Fatal(err)
	}
	db.Crash()
	recoverUntilClean(t, db)
	if err := CheckRecovered(db, original); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentTxns hammers the database with concurrent strict-2PL
// transactions (pages locked in sorted order, so no deadlock), then
// crashes and recovers. Every transaction committed before the crash,
// so all effects must survive exactly once.
func TestConcurrentTxns(t *testing.T) {
	db, original := freshDB(t)
	const workers = 4
	const txnsPerWorker = 10
	pagesOf := func(g, i int) []uint32 {
		set := map[uint32]bool{}
		var ps []uint32
		for k := 0; k < 3; k++ {
			p := uint32((g*7 + i*3 + k*5) % NumPages)
			if !set[p] {
				set[p] = true
				ps = append(ps, p)
			}
		}
		sort.Slice(ps, func(a, b int) bool { return ps[a] < ps[b] })
		return ps
	}
	expected := append([]int64(nil), original...)
	var wg sync.WaitGroup
	errs := make(chan error, workers*txnsPerWorker*4)
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < txnsPerWorker; i++ {
				txn := db.Begin()
				for _, p := range pagesOf(g, i) {
					if err := db.Update(txn, p, int64(g+i+1)); err != nil {
						errs <- err
						return
					}
				}
				if err := db.Commit(txn); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for g := 0; g < workers; g++ {
		for i := 0; i < txnsPerWorker; i++ {
			for _, p := range pagesOf(g, i) {
				expected[p] += int64(g + i + 1)
			}
		}
	}
	if err := db.FlushPages(); err != nil {
		t.Fatal(err)
	}
	db.Crash()
	recoverUntilClean(t, db)
	if err := CheckPages(db, expected); err != nil {
		t.Fatal(err)
	}
	if err := CheckRecovered(db, original); err != nil {
		t.Fatal(err)
	}
}

// TestCrashStorm crashes and recovers repeatedly at random-ish points
// to stress idempotence of the whole pipeline.
func TestCrashStorm(t *testing.T) {
	db, original := freshDB(t)
	points := append(append([]CrashPoint{}, ExecCrashPoints...), RecoveryCrashPoints...)
	for round := 0; round < 20; round++ {
		db.inj.reset()
		db.inj.arm(points[round%len(points)])
		crashed := survive(func() {
			txn := db.Begin()
			if err := db.Update(txn, uint32(round%NumPages), int64(round+1)); err != nil {
				t.Fatal(err)
			}
			if err := db.Commit(txn); err != nil {
				t.Fatal(err)
			}
			if round%3 == 0 {
				db.Checkpoint()
			}
			if round%4 == 0 {
				if err := db.FlushPages(); err != nil {
					t.Fatal(err)
				}
			}
			if round%5 == 0 {
				db.Recover()
			}
		})
		if crashed {
			db.Crash()
			db.inj.reset()
			recoverUntilClean(t, db) // a crashed DB must be recovered before reuse
		}
	}
	db.inj.reset()
	db.Crash()
	recoverUntilClean(t, db)
	if err := CheckRecovered(db, original); err != nil {
		t.Fatal(err)
	}
	fmt.Println("crash storm: final state verified")
}
