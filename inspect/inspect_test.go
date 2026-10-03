package inspect

import (
	"path/filepath"
	"sync"
	"testing"

	"aries/recovery"
	"aries/storage"
	"aries/wal"
)

type fixture struct {
	dir  string
	log  *wal.LogManager
	disk *storage.DiskStore
	pool *storage.BufferPool
	insp *Inspector
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	disk, err := storage.OpenDiskStore(filepath.Join(dir, "pages.db"))
	if err != nil {
		t.Fatal(err)
	}
	log, err := wal.OpenLog(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	pool := storage.NewBufferPool(disk)
	t.Cleanup(func() {
		log.Close()
		disk.Close()
	})
	return &fixture{dir: dir, log: log, disk: disk, pool: pool, insp: NewInspector(log, pool, disk)}
}

func hasKind(violations []Violation, kind Kind) bool {
	for _, v := range violations {
		if v.Kind == kind {
			return true
		}
	}
	return false
}

// recoveredFixture builds the full acceptance scenario, crashes and
// recovers, returning an inspector over the recovered state.
func recoveredFixture(t *testing.T) (*fixture, *recovery.Analysis) {
	t.Helper()
	f := newFixture(t)
	sys := recovery.NewSystem(f.log, f.pool)
	_, chk, err := recovery.BuildCrashScenario(sys, f.disk)
	if err != nil {
		t.Fatal(err)
	}
	f.log.Close()
	log, err := wal.OpenLog(filepath.Join(f.dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	f.log = log
	f.pool = storage.NewBufferPool(f.disk)
	f.insp = NewInspector(f.log, f.pool, f.disk)
	engine := &recovery.Engine{Log: f.log, Pool: f.pool, Disk: f.disk}
	rep, err := engine.Restart(chk)
	if err != nil {
		t.Fatal(err)
	}
	return f, rep.Analysis
}

func TestCleanSystemPassesAllChecks(t *testing.T) {
	f, analysis := recoveredFixture(t)
	violations, err := f.insp.CheckAll(analysis)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("clean system reported violations: %v", violations)
	}
	// The assertion instrument must not panic.
	f.insp.AssertClean(analysis)
}

func TestDanglingPrevLSN(t *testing.T) {
	f := newFixture(t)
	f.log.Append(&wal.Record{Type: wal.TypeBegin, TxnID: 1})
	f.log.Append(&wal.Record{Type: wal.TypeUpdate, TxnID: 1, PrevLSN: 999, PageID: 1})
	violations, err := f.insp.CheckChains()
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(violations, KindDanglingRef) {
		t.Fatalf("expected dangling-reference, got %v", violations)
	}
}

func TestCrossTxnPrevLSN(t *testing.T) {
	f := newFixture(t)
	f.log.Append(&wal.Record{Type: wal.TypeBegin, TxnID: 1})
	f.log.Append(&wal.Record{Type: wal.TypeUpdate, TxnID: 2, PrevLSN: 1, PageID: 1})
	violations, _ := f.insp.CheckChains()
	if !hasKind(violations, KindDanglingRef) {
		t.Fatalf("expected cross-transaction dangling reference, got %v", violations)
	}
}

func TestOrphanLoopDetected(t *testing.T) {
	f := newFixture(t)
	// LSN 2 points forward at LSN 3, and LSN 3 points back at 2:
	// a cycle the undo scan would never escape.
	f.log.Append(&wal.Record{Type: wal.TypeBegin, TxnID: 1})
	f.log.Append(&wal.Record{Type: wal.TypeUpdate, TxnID: 1, PrevLSN: 3, PageID: 1})
	f.log.Append(&wal.Record{Type: wal.TypeUpdate, TxnID: 1, PrevLSN: 2, PageID: 1})
	violations, err := f.insp.CheckAcyclic()
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(violations, KindOrphanLoop) {
		t.Fatalf("expected orphan-loop, got %v", violations)
	}
}

func TestAcyclicCleanLog(t *testing.T) {
	f, _ := recoveredFixture(t)
	violations, err := f.insp.CheckAcyclic()
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("unexpected violations: %v", violations)
	}
}

func TestCLRLinkViolations(t *testing.T) {
	f := newFixture(t)
	f.log.Append(&wal.Record{Type: wal.TypeBegin, TxnID: 1})
	f.log.Append(&wal.Record{Type: wal.TypeBegin, TxnID: 2})
	// undoNext points at another transaction's record.
	f.log.Append(&wal.Record{Type: wal.TypeCLR, TxnID: 1, PrevLSN: 1, PageID: 1, UndoNext: 2})
	// undoNext points nowhere.
	f.log.Append(&wal.Record{Type: wal.TypeCLR, TxnID: 1, PrevLSN: 3, PageID: 1, UndoNext: 777})
	violations, err := f.insp.CheckCLRLinks()
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 2 || !hasKind(violations, KindCLRLink) {
		t.Fatalf("expected 2 clr-link violations, got %v", violations)
	}
}

func TestDPTMismatchDetected(t *testing.T) {
	f, analysis := recoveredFixture(t)
	// Tamper: phantom page and wrong recLSN.
	analysis.DPT[99] = 1
	analysis.DPT[1] = analysis.DPT[1] + 1
	violations, err := f.insp.CheckDPT(analysis)
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(violations, KindDPTMismatch) {
		t.Fatalf("expected dpt-inconsistency, got %v", violations)
	}
}

func TestDPTMissingEntry(t *testing.T) {
	f, analysis := recoveredFixture(t)
	delete(analysis.DPT, 2)
	violations, err := f.insp.CheckDPT(analysis)
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(violations, KindDPTMismatch) {
		t.Fatalf("expected dpt-inconsistency for missing page, got %v", violations)
	}
}

func TestCommittedDataLost(t *testing.T) {
	f := newFixture(t)
	sys := recovery.NewSystem(f.log, f.pool)
	f.disk.WritePage(1, 0, []byte("v0"))
	f.disk.Sync()
	sys.Begin(1)
	sys.Update(1, 1, []byte("v1"))
	sys.Commit(1)
	// Crash before any flush: the committed update is not durable.
	violations, err := f.insp.CheckPersistence()
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(violations, KindCommittedLost) {
		t.Fatalf("expected committed-data-lost, got %v", violations)
	}
	// After flushing the pool the violation must clear.
	if err := f.pool.FlushAll(); err != nil {
		t.Fatal(err)
	}
	violations, _ = f.insp.CheckPersistence()
	if hasKind(violations, KindCommittedLost) {
		t.Fatalf("violation persisted after flush: %v", violations)
	}
}

func TestUncompensatedUpdate(t *testing.T) {
	f := newFixture(t)
	sys := recovery.NewSystem(f.log, f.pool)
	f.disk.WritePage(1, 0, []byte("v0"))
	f.disk.Sync()
	sys.Begin(1)
	sys.Update(1, 1, []byte("v1"))
	sys.Abort(1)
	// Txn ends without any CLR: the update is uncompensated.
	f.log.Append(&wal.Record{Type: wal.TypeEnd, TxnID: 1, PrevLSN: 3})
	violations, err := f.insp.CheckPersistence()
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(violations, KindUncompensated) {
		t.Fatalf("expected uncompensated-update, got %v", violations)
	}
}

func TestSkewLostUpdate(t *testing.T) {
	f := newFixture(t)
	f.pool.Apply(1, []byte("v1"), 10)
	f.pool.Flush(1)
	// Disk advances past the buffered image (e.g. torn write-back).
	f.disk.WritePage(1, 20, []byte("v2"))
	violations, err := f.insp.CheckSkew()
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(violations, KindSkew) {
		t.Fatalf("expected buffer-disk-skew, got %v", violations)
	}
}

func TestSkewUntrackedDirt(t *testing.T) {
	f := newFixture(t)
	f.pool.Apply(1, []byte("v1"), 10)
	f.pool.Flush(1)
	// Disk changes underneath a clean frame.
	f.disk.WritePage(1, 11, []byte("v2"))
	violations, err := f.insp.CheckSkew()
	if err != nil {
		t.Fatal(err)
	}
	if !hasKind(violations, KindSkew) {
		t.Fatalf("expected buffer-disk-skew, got %v", violations)
	}
}

func TestSkewCleanAfterFlush(t *testing.T) {
	f := newFixture(t)
	f.pool.Apply(1, []byte("v1"), 10)
	f.pool.Flush(1)
	violations, err := f.insp.CheckSkew()
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("unexpected skew: %v", violations)
	}
}

func TestCheckpointDanglingReferences(t *testing.T) {
	f := newFixture(t)
	f.log.Append(&wal.Record{
		Type: wal.TypeCheckpoint,
		ATT:  map[int]uint64{1: 555}, // lastLSN not in log
		DPT:  map[int]uint64{2: 1},   // recLSN points at the checkpoint itself
	})
	violations, err := f.insp.CheckChains()
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 2 || !hasKind(violations, KindDanglingRef) {
		t.Fatalf("expected 2 dangling-reference violations, got %v", violations)
	}
}

// TestConcurrentInspection runs inspectors concurrently with an
// active transaction workload (run with -race).
func TestConcurrentInspection(t *testing.T) {
	f := newFixture(t)
	sys := recovery.NewSystem(f.log, f.pool)
	for page := 0; page < 4; page++ {
		f.disk.WritePage(page, 0, []byte("init"))
	}
	f.disk.Sync()
	var wg sync.WaitGroup
	for txn := 1; txn <= 8; txn++ {
		wg.Add(1)
		go func(txn int) {
			defer wg.Done()
			sys.Begin(txn)
			for i := 0; i < 10; i++ {
				sys.Update(txn, (txn+i)%4, []byte{byte(txn)})
			}
			sys.Commit(txn)
		}(txn)
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if _, err := f.insp.CheckChains(); err != nil {
					t.Error(err)
					return
				}
				if _, err := f.insp.CheckAcyclic(); err != nil {
					t.Error(err)
					return
				}
				if _, err := f.insp.CheckSkew(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
