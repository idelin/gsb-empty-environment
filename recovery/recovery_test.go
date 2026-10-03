package recovery

import (
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"aries/storage"
	"aries/wal"
)

type fixture struct {
	dir        string
	disk       *storage.DiskStore
	log        *wal.LogManager
	pool       *storage.BufferPool
	masterPath string
	logPath    string
	diskPath   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{
		dir:        dir,
		logPath:    filepath.Join(dir, "wal.log"),
		diskPath:   filepath.Join(dir, "pages.db"),
		masterPath: filepath.Join(dir, "master"),
	}
	var err error
	if f.disk, err = storage.OpenDiskStore(f.diskPath); err != nil {
		t.Fatal(err)
	}
	if f.log, err = wal.OpenLog(f.logPath); err != nil {
		t.Fatal(err)
	}
	f.pool = storage.NewBufferPool(f.disk)
	t.Cleanup(func() {
		f.log.Close()
		f.disk.Close()
	})
	return f
}

// crash simulates a power failure: the buffer pool vanishes, the log
// manager is reopened from disk.
func (f *fixture) crash(t *testing.T) {
	t.Helper()
	f.log.Close()
	var err error
	if f.log, err = wal.OpenLog(f.logPath); err != nil {
		t.Fatal(err)
	}
	f.pool = storage.NewBufferPool(f.disk)
}

func (f *fixture) restart(t *testing.T) *Report {
	t.Helper()
	chk, err := wal.ReadMaster(f.masterPath)
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{Log: f.log, Pool: f.pool, Disk: f.disk}
	rep, err := engine.Restart(chk)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// TestAcceptanceScenario is the acceptance boundary: a WAL with 3
// uncommitted and 2 committed transactions crashes after a
// checkpoint; restart recovery must roll back the losers and persist
// the winners.
func TestAcceptanceScenario(t *testing.T) {
	f := newFixture(t)
	sys := NewSystem(f.log, f.pool)
	exp, chkLSN, err := BuildCrashScenario(sys, f.disk)
	if err != nil {
		t.Fatal(err)
	}
	if err := wal.WriteMaster(f.masterPath, chkLSN); err != nil {
		t.Fatal(err)
	}

	// Sanity: at crash time only pre-checkpoint updates are durable.
	if got := f.disk.PageLSN(2); got != 7 {
		t.Fatalf("page 2 durable pageLSN = %d, want 7 (pre-checkpoint)", got)
	}

	f.crash(t)
	rep := f.restart(t)

	if !reflect.DeepEqual(rep.Losers, exp.Losers) {
		t.Fatalf("losers = %v, want %v", rep.Losers, exp.Losers)
	}
	if !reflect.DeepEqual(rep.Winners, exp.Winners) {
		t.Fatalf("winners = %v, want %v", rep.Winners, exp.Winners)
	}
	// 6 post-crash updates redone (LSN 9,10,13,16,17,18); the two
	// pre-checkpoint updates were already durable.
	if rep.Redone != 6 {
		t.Fatalf("redone = %d, want 6", rep.Redone)
	}
	// Losers made 2+1+1 = 4 updates; each must be compensated.
	if len(rep.CLRs) != 4 {
		t.Fatalf("CLRs = %v, want 4", rep.CLRs)
	}
	if len(rep.Ends) != 3 {
		t.Fatalf("Ends = %v, want 3", rep.Ends)
	}
	if err := VerifyExpectation(f.disk, exp); err != nil {
		t.Fatal(err)
	}
	// A fresh analysis after restart must find an empty ATT.
	post, err := Analyze(f.log, chkLSN)
	if err != nil {
		t.Fatal(err)
	}
	if len(post.ATT) != 0 {
		t.Fatalf("ATT not empty after restart: %+v", post.ATT)
	}
	// Log must be durable through the last undo record.
	if f.log.FlushedLSN() != f.log.LastLSN() {
		t.Fatalf("flushed %d < last %d", f.log.FlushedLSN(), f.log.LastLSN())
	}
}

// TestRecoveryIsIdempotent crashes again after a full recovery and
// verifies the second restart is a no-op.
func TestRecoveryIsIdempotent(t *testing.T) {
	f := newFixture(t)
	sys := NewSystem(f.log, f.pool)
	exp, chkLSN, err := BuildCrashScenario(sys, f.disk)
	if err != nil {
		t.Fatal(err)
	}
	wal.WriteMaster(f.masterPath, chkLSN)
	f.crash(t)
	f.restart(t)

	f.crash(t)
	rep2 := f.restart(t)
	if rep2.Redone != 0 {
		t.Fatalf("second restart redid %d records, want 0", rep2.Redone)
	}
	if len(rep2.CLRs) != 0 || len(rep2.Ends) != 0 {
		t.Fatalf("second restart undid work: CLRs=%v Ends=%v", rep2.CLRs, rep2.Ends)
	}
	if err := VerifyExpectation(f.disk, exp); err != nil {
		t.Fatal(err)
	}
}

// TestUndoResumesAfterCrashDuringAbort verifies that CLRs written
// before a crash are honored: undo must not re-compensate them.
func TestUndoResumesAfterCrashDuringAbort(t *testing.T) {
	f := newFixture(t)
	sys := NewSystem(f.log, f.pool)
	f.disk.WritePage(1, 0, []byte("v0"))
	f.disk.Sync()

	sys.Begin(1)
	up1, _ := sys.Update(1, 1, []byte("v1"))
	up2, _ := sys.Update(1, 1, []byte("v2"))
	sys.Abort(1)
	// Partial rollback before the crash: compensate only the second
	// update, pointing undoNext at the first.
	f.log.Append(&wal.Record{
		Type: wal.TypeCLR, TxnID: 1, PrevLSN: up2, PageID: 1,
		UndoNext: up1, Before: []byte("v2"), After: []byte("v1"),
	})
	f.log.Flush(f.log.LastLSN())

	f.crash(t)
	rep := f.restart(t)
	if len(rep.CLRs) != 1 {
		t.Fatalf("wrote %d CLRs, want exactly 1 (only the first update)", len(rep.CLRs))
	}
	got, err := PageContents(f.disk, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0" {
		t.Fatalf("page 1 = %q, want %q", got, "v0")
	}
}

// TestNoCheckpoint verifies recovery from a log without any
// checkpoint: analysis scans from the beginning.
func TestNoCheckpoint(t *testing.T) {
	f := newFixture(t)
	sys := NewSystem(f.log, f.pool)
	f.disk.WritePage(1, 0, []byte("a"))
	f.disk.Sync()
	sys.Begin(1)
	sys.Update(1, 1, []byte("b"))
	sys.Commit(1)
	sys.Begin(2)
	sys.Update(2, 1, []byte("c"))
	f.log.Flush(f.log.LastLSN())

	f.crash(t)
	rep := f.restart(t)
	if !reflect.DeepEqual(rep.Losers, []int{2}) {
		t.Fatalf("losers = %v", rep.Losers)
	}
	got, _ := PageContents(f.disk, 1)
	if got != "b" {
		t.Fatalf("page 1 = %q, want committed %q", got, "b")
	}
}

// TestEmptyLogRestart is a boundary case: no records at all.
func TestEmptyLogRestart(t *testing.T) {
	f := newFixture(t)
	rep := f.restart(t)
	if rep.Redone != 0 || len(rep.CLRs) != 0 || len(rep.Losers) != 0 {
		t.Fatalf("unexpected work on empty log: %+v", rep)
	}
}

// TestCheckpointOnlyLog: checkpoint with empty tables, nothing else.
func TestCheckpointOnlyLog(t *testing.T) {
	f := newFixture(t)
	sys := NewSystem(f.log, f.pool)
	chk, err := sys.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	wal.WriteMaster(f.masterPath, chk)
	f.log.Flush(chk)
	f.crash(t)
	rep := f.restart(t)
	if rep.Redone != 0 || len(rep.Losers) != 0 {
		t.Fatalf("unexpected work: %+v", rep)
	}
}

// TestAnalysisRejectsNonCheckpoint guards the analysis entry point.
func TestAnalysisRejectsNonCheckpoint(t *testing.T) {
	f := newFixture(t)
	sys := NewSystem(f.log, f.pool)
	lsn, _ := sys.Begin(1)
	if _, err := Analyze(f.log, lsn); err == nil {
		t.Fatal("expected error: LSN is not a checkpoint")
	}
}

// TestConcurrentTransactions drives many concurrent transactions
// through the System facade (run with -race).
func TestConcurrentTransactions(t *testing.T) {
	f := newFixture(t)
	sys := NewSystem(f.log, f.pool)
	for page := 0; page < 4; page++ {
		f.disk.WritePage(page, 0, []byte("init"))
	}
	f.disk.Sync()
	var wg sync.WaitGroup
	for txn := 1; txn <= 16; txn++ {
		wg.Add(1)
		go func(txn int) {
			defer wg.Done()
			if _, err := sys.Begin(txn); err != nil {
				t.Error(err)
				return
			}
			for i := 0; i < 10; i++ {
				page := (txn + i) % 4
				if _, err := sys.Update(txn, page, []byte{byte(txn), byte(i)}); err != nil {
					t.Error(err)
					return
				}
			}
			if _, err := sys.Commit(txn); err != nil {
				t.Error(err)
			}
		}(txn)
	}
	wg.Wait()
	f.log.Flush(f.log.LastLSN())
	f.crash(t)
	rep := f.restart(t)
	if len(rep.Losers) != 0 {
		t.Fatalf("all txns committed, losers = %v", rep.Losers)
	}
}

// TestNoGoroutineLeak asserts the engine leaves no goroutines behind.
func TestNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	f := newFixture(t)
	sys := NewSystem(f.log, f.pool)
	exp, chk, err := BuildCrashScenario(sys, f.disk)
	if err != nil {
		t.Fatal(err)
	}
	wal.WriteMaster(f.masterPath, chk)
	f.crash(t)
	f.restart(t)
	if err := VerifyExpectation(f.disk, exp); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, got)
	}
}

// TestLoserOrdering verifies undo processes the highest LSN first:
// with two losers interleaved on one page, the final image must be
// the original before-image.
func TestLoserOrdering(t *testing.T) {
	f := newFixture(t)
	sys := NewSystem(f.log, f.pool)
	f.disk.WritePage(1, 0, []byte("base"))
	f.disk.Sync()
	sys.Begin(1)
	sys.Begin(2)
	sys.Update(1, 1, []byte("from-t1"))
	sys.Update(2, 1, []byte("from-t2"))
	f.log.Flush(f.log.LastLSN())
	f.crash(t)
	rep := f.restart(t)
	losers := append([]int(nil), rep.Losers...)
	sort.Ints(losers)
	if !reflect.DeepEqual(losers, []int{1, 2}) {
		t.Fatalf("losers = %v", losers)
	}
	got, _ := PageContents(f.disk, 1)
	if got != "base" {
		t.Fatalf("page 1 = %q, want %q", got, "base")
	}
}
