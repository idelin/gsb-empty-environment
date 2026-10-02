package recovery

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"aries/db"
	"aries/metrics"
	"aries/storage"
	"aries/wal"
)

const (
	off1 = storage.DataOffset      // 8
	off2 = storage.DataOffset + 16 // 24
	off3 = storage.DataOffset + 32 // 40
)

func tempDir(t *testing.T) string {
	dir := filepath.Join(os.TempDir(), "recoverytest-"+t.Name())
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustUpdate(t *testing.T, txn *db.Txn, page int32, off uint32, val string) {
	t.Helper()
	if err := txn.Update(page, off, []byte(val)); err != nil {
		t.Fatal(err)
	}
}

// buildCrashedSystem creates the acceptance-test scenario:
//
//	T1: update P1, COMMIT            (before checkpoint)
//	--- CHECKPOINT ---
//	T2: update P2, COMMIT            (after checkpoint: redo must restore it)
//	T3: update P1, update P3         (uncommitted loser)
//	T4: update P2                    (uncommitted loser)
//	T5: update P3, update P3, ABORT, one CLR  (loser, dies mid-abort)
//
// Pages 1 and 2 are flushed after the last updates (so disk holds loser
// data), page 3 is never flushed (so redo must rebuild it), the WAL is
// flushed, and then the system "crashes" without flushing anything else.
func buildCrashedSystem(t *testing.T, dir string) {
	t.Helper()
	database, err := db.Open(filepath.Join(dir, "data.db"), filepath.Join(dir, "test.wal"), 16, nil)
	if err != nil {
		t.Fatal(err)
	}

	t1 := database.Begin()
	mustUpdate(t, t1, 1, off1, "T1-data")
	if err := t1.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	t2 := database.Begin()
	mustUpdate(t, t2, 2, off1, "T2-data")
	if err := t2.Commit(); err != nil {
		t.Fatal(err)
	}

	t3 := database.Begin()
	mustUpdate(t, t3, 1, off1, "T3-junk")
	mustUpdate(t, t3, 3, off1, "T3-junk")

	t4 := database.Begin()
	mustUpdate(t, t4, 2, off1, "T4-junk")

	t5 := database.Begin()
	mustUpdate(t, t5, 3, off2, "T5-junk1")
	mustUpdate(t, t5, 3, off3, "T5-junk2")
	if err := t5.BeginAbort(); err != nil {
		t.Fatal(err)
	}
	if more, err := t5.AbortOne(); err != nil || !more { // undoes T5-junk2, then dies
		t.Fatalf("partial abort: more=%v err=%v", more, err)
	}

	// Force some dirty pages to disk *after* the loser updates, so recovery
	// cannot rely on the losers never having reached disk.
	if err := database.Pool.FlushPage(1); err != nil {
		t.Fatal(err)
	}
	if err := database.Pool.FlushPage(2); err != nil {
		t.Fatal(err)
	}
	// WAL is durable; dirty page 3 and the buffer pool are lost in the crash.
	if err := database.Log.Flush(); err != nil {
		t.Fatal(err)
	}
	// Crash: no Close, no FlushAll.
}

func pageString(t *testing.T, database *db.DB, page int32, off uint32, n int) string {
	t.Helper()
	p, err := database.Pool.FetchPage(page)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Pool.UnpinPage(page, false)
	return string(bytes.TrimRight(p.Data[off:off+uint32(n)], "\x00"))
}

func recoverDB(t *testing.T, dir string) (*db.DB, *AnalysisResult, *metrics.Registry) {
	t.Helper()
	reg := metrics.New()
	database, err := db.Open(filepath.Join(dir, "data.db"), filepath.Join(dir, "test.wal"), 16, reg)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(database.Log, database.Pool, reg)
	res, err := eng.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return database, res, reg
}

func TestARIESCrashRecovery(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	buildCrashedSystem(t, dir)

	database, res, reg := recoverDB(t, dir)

	// --- Analysis: transaction table -------------------------------
	if res.CheckpointLSN == wal.LSNInvalid {
		t.Fatal("analysis found no checkpoint")
	}
	if len(res.TT) != 3 {
		t.Fatalf("TT size = %d, want 3 losers (%v)", len(res.TT), res.TT)
	}
	if info := res.TT[3]; info.Status != wal.StatusRunning {
		t.Fatalf("T3 status = %v, want RUNNING", info.Status)
	}
	if info := res.TT[4]; info.Status != wal.StatusRunning {
		t.Fatalf("T4 status = %v, want RUNNING", info.Status)
	}
	if info := res.TT[5]; info.Status != wal.StatusAborting {
		t.Fatalf("T5 status = %v, want ABORTING", info.Status)
	}
	if losers := res.Losers(); len(losers) != 3 {
		t.Fatalf("losers = %v, want 3", losers)
	}

	// --- Analysis: dirty page table --------------------------------
	for _, page := range []int32{1, 2, 3} {
		if _, ok := res.DPT[page]; !ok {
			t.Fatalf("DPT missing page %d: %v", page, res.DPT)
		}
	}

	// --- Data: committed work persisted, losers rolled back ---------
	// Read through a brand-new buffer pool so only durable disk state counts.
	verify, err := db.Open(filepath.Join(dir, "data.db"), filepath.Join(dir, "test.wal"), 16, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pageString(t, verify, 1, off1, 7); got != "T1-data" {
		t.Fatalf("P1 = %q, want T1-data (T1 committed, T3 rolled back)", got)
	}
	if got := pageString(t, verify, 2, off1, 7); got != "T2-data" {
		t.Fatalf("P2 = %q, want T2-data (T2 redone, T4 rolled back)", got)
	}
	for _, off := range []uint32{off1, off2, off3} {
		if got := pageString(t, verify, 3, off, 8); got != "" {
			t.Fatalf("P3@%d = %q, want empty (T3/T5 rolled back)", off, got)
		}
	}

	// --- Undo wrote CLRs and END records ----------------------------
	var clrs, ends int
	it := database.Log.Iterator()
	for {
		rec, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("iterate: %v", err)
		}
		switch rec.Type {
		case wal.TypeCLR:
			clrs++
		case wal.TypeEnd:
			ends++
		}
	}
	if clrs != 5 { // 1 pre-crash (T5) + 4 during undo (T3x2, T4, T5)
		t.Fatalf("CLR count = %d, want 5", clrs)
	}
	if ends != 5 { // T1, T2 commits + T3, T4, T5 rolled back
		t.Fatalf("END count = %d, want 5", ends)
	}

	// --- Metrics -----------------------------------------------------
	snap := reg.Snapshot()
	if got := snap.Counters["recovery.pages_redone"]; got == 0 {
		t.Fatal("redo applied nothing; page 3 should have been rebuilt")
	}
	if got := snap.Counters["recovery.clr_written"]; got != 4 {
		t.Fatalf("clr_written = %d, want 4", got)
	}
	if got := snap.Counters["recovery.txns_rolled_back"]; got != 3 {
		t.Fatalf("txns_rolled_back = %d, want 3", got)
	}
	if got := snap.Gauges["recovery.tt_size"]; got != 3 {
		t.Fatalf("tt_size gauge = %d, want 3", got)
	}
	if got := snap.Gauges["recovery.dpt_size"]; got != 3 {
		t.Fatalf("dpt_size gauge = %d, want 3", got)
	}
	for _, phase := range []string{"recovery.analysis", "recovery.redo", "recovery.undo"} {
		ts, ok := snap.Timers[phase]
		if !ok || ts.Count != 1 {
			t.Fatalf("timer %s count = %v, want exactly 1 observation", phase, ts.Count)
		}
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("unexpected error codes: %v", snap.Errors)
	}
}

func TestRecoveryIsIdempotent(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	buildCrashedSystem(t, dir)

	if _, _, reg1 := recoverDB(t, dir); reg1.Snapshot().Counters["recovery.clr_written"] != 4 {
		t.Fatalf("first recovery wrote %d CLRs, want 4",
			reg1.Snapshot().Counters["recovery.clr_written"])
	}
	// Second recovery: no losers remain, nothing is undone, data unchanged.
	database, res, reg2 := recoverDB(t, dir)
	if len(res.TT) != 0 {
		t.Fatalf("second analysis TT = %v, want empty", res.TT)
	}
	if got := reg2.Snapshot().Counters["recovery.clr_written"]; got != 0 {
		t.Fatalf("second recovery wrote %d CLRs, want 0", got)
	}
	if got := pageString(t, database, 1, off1, 7); got != "T1-data" {
		t.Fatalf("P1 = %q after second recovery, want T1-data", got)
	}
	if got := pageString(t, database, 2, off1, 7); got != "T2-data" {
		t.Fatalf("P2 = %q after second recovery, want T2-data", got)
	}
}

func TestRecoverEmptyLog(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	database, res, reg := recoverDB(t, dir)
	if len(res.TT) != 0 || len(res.DPT) != 0 {
		t.Fatalf("empty log produced TT=%v DPT=%v", res.TT, res.DPT)
	}
	if len(reg.Snapshot().Errors) != 0 {
		t.Fatalf("errors on empty log: %v", reg.Snapshot().Errors)
	}
	_ = database
}

func TestRecoveryNoGoroutineLeak(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	buildCrashedSystem(t, dir)

	before := runtime.NumGoroutine()
	recoverDB(t, dir)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
