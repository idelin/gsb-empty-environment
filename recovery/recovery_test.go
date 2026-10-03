package recovery_test

import (
	"bytes"
	"encoding/json"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"aries/buffer"
	"aries/metrics"
	"aries/recovery"
	"aries/wal"
)

// scenario builds the acceptance WAL: 5 transactions, 2 committed
// (T1, T2) and 3 uncommitted (T3, T4, T5), with a checkpoint after
// T1 commits, then a crash. It returns the WAL path and the LSN of
// T1's update (used to pre-flush P1 before the crash).
func scenario(t *testing.T) (string, wal.LSN) {
	t.Helper()
	path := t.TempDir() + "/wal.log"
	w, err := wal.NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	b := wal.NewBuilder(w)

	// T1: committed before the checkpoint.
	t1lsn := b.Update(1, 1, []byte("A"), []byte("A1"))
	b.Commit(1)
	b.End(1)
	// T2: starts before the checkpoint, commits after it.
	b.Update(2, 2, []byte("B"), []byte("B1"))
	b.Checkpoint()
	// T3, T4, T5: never commit.
	b.Update(3, 1, []byte("A1"), []byte("A2"))
	b.Update(2, 3, []byte("C"), []byte("C1"))
	b.Commit(2)
	b.Update(4, 4, []byte("D"), []byte("D1"))
	b.Update(5, 3, []byte("C1"), []byte("C2"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path, t1lsn
}

func newPool(t1lsn wal.LSN) (*buffer.Pool, *metrics.Registry) {
	reg := metrics.NewRegistry()
	pool := buffer.NewPool(64, reg.Counter("buffer.hits"), reg.Counter("buffer.misses"))
	// Pre-crash state: P1 was flushed after T1's update, so its
	// pageLSN equals t1lsn and redo of T1's update must be skipped.
	pg := pool.Fetch(1)
	pg.Data = []byte("A1")
	pg.LSN = t1lsn
	pg.Dirty = true
	pool.FlushPage(1)
	pool.Crash() // lose all in-memory frames, keep disk
	return pool, reg
}

func diskData(t *testing.T, pool *buffer.Pool, id uint64) string {
	t.Helper()
	pg, ok := pool.DiskPage(id)
	if !ok {
		t.Fatalf("page %d missing from disk", id)
	}
	return string(pg.Data)
}

func TestARIESRecoveryAcceptance(t *testing.T) {
	path, t1lsn := scenario(t)
	pool, reg := newPool(t1lsn)

	eng := recovery.NewEngine(path, pool, reg)
	rep, err := eng.Recover()
	if err != nil {
		t.Fatal(err)
	}

	// Losers rolled back, winners completed.
	if got := rep.Losers; len(got) != 3 || got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("losers = %v, want [3 4 5]", got)
	}
	if len(rep.Winners) != 1 || rep.Winners[0] != 2 {
		t.Fatalf("winners = %v, want [2]", rep.Winners)
	}

	// Committed data persisted, uncommitted data rolled back.
	if got := diskData(t, pool, 1); got != "A1" {
		t.Fatalf("P1 = %q, want A1 (T3 undo)", got)
	}
	if got := diskData(t, pool, 2); got != "B1" {
		t.Fatalf("P2 = %q, want B1 (T2 redo)", got)
	}
	if got := diskData(t, pool, 3); got != "C1" {
		t.Fatalf("P3 = %q, want C1 (T5 undo, T2 redo kept)", got)
	}
	if got := diskData(t, pool, 4); got != "D" {
		t.Fatalf("P4 = %q, want D (T4 undo)", got)
	}

	// Redo repeated history: 5 updates applied, T1's update skipped
	// because P1 already carried its effects.
	if rep.Redone != 5 {
		t.Fatalf("redone = %d, want 5", rep.Redone)
	}
	if rep.Undone != 3 || rep.CLRs != 3 {
		t.Fatalf("undone=%d clrs=%d, want 3/3", rep.Undone, rep.CLRs)
	}

	// WAL now contains 3 CLRs and 5 END records (T1 pre-crash, plus
	// T2 and the 3 losers written during recovery).
	recs, err := wal.ScanAll(path)
	if err != nil {
		t.Fatal(err)
	}
	var clrs, ends int
	for _, r := range recs {
		switch r.Type {
		case wal.TypeCLR:
			clrs++
		case wal.TypeEnd:
			ends++
		}
	}
	if clrs != 3 || ends != 5 {
		t.Fatalf("wal clrs=%d ends=%d, want 3/5", clrs, ends)
	}

	// Fine-grained metrics were recorded for every phase.
	snap := reg.Snapshot()
	for _, name := range []string{
		recovery.MAnalysisDuration, recovery.MRedoDuration, recovery.MUndoDuration,
	} {
		h, ok := snap.Histograms[name]
		if !ok || h.Count != 1 {
			t.Fatalf("histogram %s count = %v, want exactly 1 observation", name, ok)
		}
	}
	if snap.Counters[recovery.MRedoApplied] != 5 || snap.Counters[recovery.MRedoSkipped] != 1 {
		t.Fatalf("redo counters = %d/%d, want 5/1",
			snap.Counters[recovery.MRedoApplied], snap.Counters[recovery.MRedoSkipped])
	}
	if snap.Counters[recovery.MUndoCLR] != 3 {
		t.Fatalf("undo clr counter = %d, want 3", snap.Counters[recovery.MUndoCLR])
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", snap.Errors)
	}
	if pool.HitRate() <= 0 {
		t.Fatalf("hit rate = %v, want > 0 (redo re-fetched pages)", pool.HitRate())
	}
}

func TestAnalysisTables(t *testing.T) {
	path, t1lsn := scenario(t)
	pool, reg := newPool(t1lsn)
	eng := recovery.NewEngine(path, pool, reg)
	if err := eng.Analysis(); err != nil {
		t.Fatal(err)
	}
	// TxnTable: T1 ended (gone), T2 committed, T3/T4/T5 running.
	if len(eng.TxnTable) != 4 {
		t.Fatalf("txn table size = %d, want 4", len(eng.TxnTable))
	}
	if eng.TxnTable[2].Status != wal.StatusCommitted {
		t.Fatalf("T2 status = %v, want COMMITTED", eng.TxnTable[2].Status)
	}
	for _, txn := range []uint64{3, 4, 5} {
		if eng.TxnTable[txn].Status != wal.StatusRunning {
			t.Fatalf("T%d status = %v, want RUNNING", txn, eng.TxnTable[txn].Status)
		}
	}
	// DPT: pages 1..4 dirty, seeded from checkpoint for P1/P2.
	if len(eng.DPT) != 4 {
		t.Fatalf("dpt size = %d, want 4: %v", len(eng.DPT), eng.DPT)
	}
}

func TestRecoverWithoutCheckpoint(t *testing.T) {
	path := t.TempDir() + "/wal.log"
	w, _ := wal.NewWriter(path)
	b := wal.NewBuilder(w)
	b.Update(1, 1, []byte("X"), []byte("X1"))
	b.Commit(1)
	b.Update(2, 1, []byte("X1"), []byte("X2"))
	w.Close()

	reg := metrics.NewRegistry()
	pool := buffer.NewPool(8, reg.Counter("h"), reg.Counter("m"))
	eng := recovery.NewEngine(path, pool, reg)
	rep, err := eng.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Losers) != 1 || rep.Losers[0] != 2 {
		t.Fatalf("losers = %v, want [2]", rep.Losers)
	}
	pg, _ := pool.DiskPage(1)
	if string(pg.Data) != "X1" {
		t.Fatalf("P1 = %q, want X1", pg.Data)
	}
}

func TestRecoverEmptyWAL(t *testing.T) {
	path := t.TempDir() + "/wal.log"
	w, _ := wal.NewWriter(path)
	w.Close()
	reg := metrics.NewRegistry()
	pool := buffer.NewPool(8, reg.Counter("h"), reg.Counter("m"))
	rep, err := recovery.NewEngine(path, pool, reg).Recover()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Redone != 0 || len(rep.Losers) != 0 || len(rep.Winners) != 0 {
		t.Fatalf("unexpected report: %+v", rep)
	}
}

func TestRecoverCorruptTail(t *testing.T) {
	path, t1lsn := scenario(t)
	// Simulate a torn write: append garbage at the tail.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0xde, 0xad, 0xbe})
	f.Close()

	pool, reg := newPool(t1lsn)
	rep, err := recovery.NewEngine(path, pool, reg).Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Losers) != 3 {
		t.Fatalf("losers = %v, want 3 losers despite torn tail", rep.Losers)
	}
	snap := reg.Snapshot()
	if snap.Errors[metrics.ErrWALCorrupt] != 1 {
		t.Fatalf("WAL_CORRUPT errors = %d, want 1", snap.Errors[metrics.ErrWALCorrupt])
	}
}

func TestMetricsExportAfterRecovery(t *testing.T) {
	path, t1lsn := scenario(t)
	pool, reg := newPool(t1lsn)
	if _, err := recovery.NewEngine(path, pool, reg).Recover(); err != nil {
		t.Fatal(err)
	}
	var js bytes.Buffer
	if err := reg.ExportJSON(&js); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil {
		t.Fatalf("exported JSON invalid: %v", err)
	}
	var txt bytes.Buffer
	if err := reg.ExportText(&txt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(txt.Bytes(), []byte("aries.redo.applied 5")) {
		t.Fatalf("text export missing redo counter:\n%s", txt.String())
	}
}

func TestRecoveryConcurrentAndLeakFree(t *testing.T) {
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, t1lsn := scenario(t)
			pool, reg := newPool(t1lsn)
			if _, err := recovery.NewEngine(path, pool, reg).Recover(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	// No goroutines may leak: allow the runtime a moment to settle.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
