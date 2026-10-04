package aries

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// hasViolation reports whether the report contains at least one violation
// of the given kind.
func hasViolation(rep *Report, kind ViolationKind) bool {
	for _, v := range rep.Violations {
		if v.Kind == kind {
			return true
		}
	}
	return false
}

// cleanRecords builds a small, fully valid WAL: txn 1 updates and commits,
// txn 2 updates, is compensated by a CLR, and ends.
func cleanRecords() []*LogRecord {
	mk := func(lsn, prev, txn uint64, typ LogType) *LogRecord {
		return &LogRecord{LSN: lsn, PrevLSN: prev, TxnID: txn, Type: typ}
	}
	r1 := mk(10, 0, 1, LogUpdate)
	r1.PageID = 1
	r2 := mk(20, 10, 1, LogCommit)
	r3 := mk(30, 20, 1, LogEnd)
	r4 := mk(40, 0, 2, LogUpdate)
	r4.PageID = 2
	r5 := mk(50, 40, 2, LogCLR)
	r5.PageID = 2
	r5.UndoNextLSN = 0
	r6 := mk(60, 50, 2, LogEnd)
	return []*LogRecord{r1, r2, r3, r4, r5, r6}
}

func TestInspectCleanWAL(t *testing.T) {
	rep := Inspector{}.InspectStructural(cleanRecords())
	if !rep.OK() {
		t.Fatalf("clean WAL flagged:\n%s", rep.Summary())
	}
}

func TestInspectLSNOrderViolation(t *testing.T) {
	recs := cleanRecords()
	recs[3].LSN = 5 // out of order relative to LSN 30
	rep := Inspector{}.InspectStructural(recs)
	if !hasViolation(rep, KindLSNOrder) {
		t.Errorf("expected LSN_ORDER violation, got:\n%s", rep.Summary())
	}
}

func TestInspectDuplicateLSN(t *testing.T) {
	recs := cleanRecords()
	recs[4].LSN = recs[3].LSN
	rep := Inspector{}.InspectStructural(recs)
	if !hasViolation(rep, KindDuplicateLSN) {
		t.Errorf("expected DUPLICATE_LSN violation, got:\n%s", rep.Summary())
	}
}

func TestInspectDanglingPrevLSN(t *testing.T) {
	recs := cleanRecords()
	recs[1].PrevLSN = 9999 // points nowhere
	rep := Inspector{}.InspectStructural(recs)
	if !hasViolation(rep, KindDanglingRef) {
		t.Errorf("expected DANGLING_REF violation, got:\n%s", rep.Summary())
	}
}

func TestInspectCrossTxnReference(t *testing.T) {
	recs := cleanRecords()
	recs[3].PrevLSN = 10 // txn 2 points into txn 1
	rep := Inspector{}.InspectStructural(recs)
	if !hasViolation(rep, KindCrossTxnRef) {
		t.Errorf("expected CROSS_TXN_REF violation, got:\n%s", rep.Summary())
	}
}

func TestInspectOrphanRecord(t *testing.T) {
	recs := cleanRecords()
	recs[1].PrevLSN = 0 // commit no longer chains to the update
	rep := Inspector{}.InspectStructural(recs)
	if !hasViolation(rep, KindOrphanRecord) {
		t.Errorf("expected ORPHAN_RECORD violation, got:\n%s", rep.Summary())
	}
}

func TestInspectOrphanLoop(t *testing.T) {
	// Two CLRs whose undoNext pointers form a cycle.
	a := &LogRecord{LSN: 10, TxnID: 1, Type: LogCLR, PageID: 1, UndoNextLSN: 20}
	b := &LogRecord{LSN: 20, PrevLSN: 10, TxnID: 1, Type: LogCLR, PageID: 1, UndoNextLSN: 10}
	end := &LogRecord{LSN: 30, PrevLSN: 20, TxnID: 1, Type: LogEnd}
	rep := Inspector{}.InspectStructural([]*LogRecord{a, b, end})
	if !hasViolation(rep, KindOrphanLoop) {
		t.Errorf("expected ORPHAN_LOOP violation, got:\n%s", rep.Summary())
	}
}

func TestInspectCLRForwardPointer(t *testing.T) {
	u := &LogRecord{LSN: 10, TxnID: 1, Type: LogUpdate, PageID: 1}
	clr := &LogRecord{LSN: 20, PrevLSN: 10, TxnID: 1, Type: LogCLR, PageID: 1, UndoNextLSN: 30}
	u2 := &LogRecord{LSN: 30, PrevLSN: 20, TxnID: 1, Type: LogUpdate, PageID: 1}
	end := &LogRecord{LSN: 40, PrevLSN: 30, TxnID: 1, Type: LogEnd}
	rep := Inspector{}.InspectStructural([]*LogRecord{u, clr, u2, end})
	if !hasViolation(rep, KindOrphanLoop) {
		t.Errorf("expected ORPHAN_LOOP for forward undoNext, got:\n%s", rep.Summary())
	}
}

func TestInspectCheckpointDanglingRefs(t *testing.T) {
	recs := cleanRecords()
	cp := &LogRecord{
		LSN: 70, Type: LogCheckpoint,
		Checkpoint: &CheckpointPayload{
			Transactions: map[uint64]TxnRecord{9: {TxnID: 9, LastLSN: 7777}},
			DirtyPages:   map[uint64]uint64{42: 8888},
		},
	}
	recs = append(recs, cp)
	rep := Inspector{}.InspectStructural(recs)
	if !hasViolation(rep, KindDanglingRef) {
		t.Errorf("expected DANGLING_REF for checkpoint refs, got:\n%s", rep.Summary())
	}
}

func TestInspectCheckpointWrongPageRef(t *testing.T) {
	recs := cleanRecords()
	cp := &LogRecord{
		LSN: 70, Type: LogCheckpoint,
		Checkpoint: &CheckpointPayload{
			Transactions: map[uint64]TxnRecord{},
			DirtyPages:   map[uint64]uint64{99: 10}, // LSN 10 is an update on page 1
		},
	}
	recs = append(recs, cp)
	rep := Inspector{}.InspectStructural(recs)
	if !hasViolation(rep, KindCheckpointRef) {
		t.Errorf("expected CHECKPOINT_REF violation, got:\n%s", rep.Summary())
	}
}

func TestInspectUnterminatedTxn(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	disk, err := OpenDisk(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	// Txn 1 commits but never ends: a leaked transaction.
	recs := []*LogRecord{
		{LSN: 10, TxnID: 1, Type: LogUpdate, PageID: 1, After: payload("x")},
		{LSN: 20, PrevLSN: 10, TxnID: 1, Type: LogCommit},
	}
	rep := Inspector{}.InspectPostRecovery(recs, disk)
	if !hasViolation(rep, KindUnterminatedTxn) {
		t.Errorf("expected UNTERMINATED_TXN violation, got:\n%s", rep.Summary())
	}
}

func TestInspectDataSkewStalePage(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	disk, err := OpenDisk(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	// WAL says page 1 was updated at LSN 10, but disk pageLSN is 0.
	recs := []*LogRecord{
		{LSN: 10, TxnID: 1, Type: LogUpdate, PageID: 1, After: payload("x")},
		{LSN: 20, PrevLSN: 10, TxnID: 1, Type: LogCommit},
		{LSN: 30, PrevLSN: 20, TxnID: 1, Type: LogEnd},
	}
	rep := Inspector{}.InspectPostRecovery(recs, disk)
	if !hasViolation(rep, KindDataSkew) {
		t.Errorf("expected DATA_SKEW (stale) violation, got:\n%s", rep.Summary())
	}
}

func TestInspectDataSkewPhantomWrite(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	disk, err := OpenDisk(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	// Disk claims pageLSN 99999, far beyond the WAL frontier.
	if err := disk.WritePage(&Page{ID: 1, LSN: 99999, Payload: payload("ghost")}); err != nil {
		t.Fatal(err)
	}
	recs := []*LogRecord{
		{LSN: 10, TxnID: 1, Type: LogUpdate, PageID: 1, After: payload("x")},
		{LSN: 20, PrevLSN: 10, TxnID: 1, Type: LogCommit},
		{LSN: 30, PrevLSN: 20, TxnID: 1, Type: LogEnd},
	}
	rep := Inspector{}.InspectPostRecovery(recs, disk)
	if !hasViolation(rep, KindDataSkew) {
		t.Errorf("expected DATA_SKEW (phantom) violation, got:\n%s", rep.Summary())
	}
}

// TestConcurrentAppendAndInspect hammers the WAL from many goroutines while
// the inspector validates snapshots concurrently. Run with -race: any data
// race or torn read fails the test. The final WAL must be violation-free.
func TestConcurrentAppendAndInspect(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	wal, err := OpenWAL(filepath.Join(dir, "wal.log"))
	if err != nil {
		t.Fatal(err)
	}
	disk, err := OpenDisk(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	sim := NewSimulator(wal, NewBufferPool(disk))

	const workers = 8
	const opsPerWorker = 25

	var workersWG, inspectorsWG sync.WaitGroup
	stop := make(chan struct{})

	// Concurrent inspectors.
	inspector := Inspector{}
	inspectErrs := make(chan error, 64)
	for i := 0; i < 2; i++ {
		inspectorsWG.Add(1)
		go func() {
			defer inspectorsWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				records, err := wal.Records()
				if err != nil {
					inspectErrs <- fmt.Errorf("read wal: %v", err)
					return
				}
				if rep := inspector.InspectStructural(records); !rep.OK() {
					inspectErrs <- fmt.Errorf("concurrent snapshot violated invariants:\n%s", rep.Summary())
					return
				}
			}
		}()
	}

	// Concurrent transaction workers.
	for w := 0; w < workers; w++ {
		workersWG.Add(1)
		go func(w int) {
			defer workersWG.Done()
			for i := 0; i < opsPerWorker; i++ {
				txn := sim.Begin()
				pageID := uint64(w*opsPerWorker + i + 1)
				if _, err := sim.Update(txn, pageID, payload(fmt.Sprintf("w%d-op%d", w, i))); err != nil {
					inspectErrs <- fmt.Errorf("update: %v", err)
					return
				}
				if err := sim.Commit(txn); err != nil {
					inspectErrs <- fmt.Errorf("commit: %v", err)
					return
				}
			}
		}(w)
	}

	workersWG.Wait()
	close(stop)
	inspectorsWG.Wait()

	select {
	case err := <-inspectErrs:
		t.Fatal(err)
	default:
	}

	records, err := wal.Records()
	if err != nil {
		t.Fatal(err)
	}
	wantRecords := workers * opsPerWorker * 3 // update + commit + end
	if len(records) != wantRecords {
		t.Fatalf("WAL holds %d records, want %d", len(records), wantRecords)
	}
	if rep := inspector.InspectStructural(records); !rep.OK() {
		t.Fatalf("final WAL violated invariants:\n%s", rep.Summary())
	}

	// Full recovery over the concurrently produced WAL must also be clean.
	rm := NewRecoveryManager(wal, disk)
	if err := rm.Recover(); err != nil {
		t.Fatal(err)
	}
	records, err = wal.Records()
	if err != nil {
		t.Fatal(err)
	}
	if rep := inspector.InspectPostRecovery(records, disk); !rep.OK() {
		t.Fatalf("post-recovery inspection failed:\n%s", rep.Summary())
	}
}
