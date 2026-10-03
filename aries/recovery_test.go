package aries

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// crashSim captures the files that survive a crash so the test can
// "reboot" onto them with fresh in-memory structures.
type crashSim struct {
	dir      string
	walPath  string
	pagePath string
}

func newCrashSim(t *testing.T) *crashSim {
	dir := tempDir(t)
	return &crashSim{
		dir:      dir,
		walPath:  filepath.Join(dir, "wal.log"),
		pagePath: filepath.Join(dir, "pages.db"),
	}
}

func (cs *crashSim) open(t *testing.T) (*WAL, *PageStore, *BufferPool, *TxnMgr) {
	t.Helper()
	wal, err := OpenWAL(cs.walPath)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	store, err := OpenPageStore(cs.pagePath, 64)
	if err != nil {
		t.Fatalf("open page store: %v", err)
	}
	pool := NewBufferPool(store)
	return wal, store, pool, NewTxnMgr(wal, pool)
}

// TestCrashRecoveryAcceptance builds the required acceptance scenario:
// 5 transactions (2 committed, 3 uncommitted, one of them crashed
// mid-abort with a CLR already written), a fuzzy checkpoint in the
// middle, then a crash. It verifies that the three-phase ARIES recovery
// repeats history, rolls back exactly the uncommitted changes, and
// preserves the committed ones.
func TestCrashRecoveryAcceptance(t *testing.T) {
	cs := newCrashSim(t)
	defer cleanup(cs.dir)

	wal, store, pool, tm := cs.open(t)

	// --- Phase 1: before the checkpoint ---------------------------------
	t1 := tm.Begin() // will commit, and be flushed to disk
	mustWrite(t, tm, t1, 0, "AAAA0001")
	mustWrite(t, tm, t1, 1, "BBBB0001")
	if err := tm.Commit(t1); err != nil {
		t.Fatalf("commit t1: %v", err)
	}
	// Simulate a background page flush: t1's pages reach disk.
	if err := pool.FlushAll(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	t2 := tm.Begin() // will commit after the checkpoint
	mustWrite(t, tm, t2, 2, "CCCC0002")

	t3 := tm.Begin() // stays uncommitted
	mustWrite(t, tm, t3, 0, "aaaat3a!")

	t4 := tm.Begin() // will abort, crash mid-abort
	mustWrite(t, tm, t4, 3, "ddddt4a!")

	t5 := tm.Begin() // stays uncommitted
	mustWrite(t, tm, t5, 1, "bbbbt5a!")

	// --- Fuzzy checkpoint ------------------------------------------------
	chkLSN, err := tm.Checkpoint()
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if chkLSN == 0 {
		t.Fatal("checkpoint returned LSN 0")
	}

	// --- Phase 2: after the checkpoint ----------------------------------
	mustWrite(t, tm, t2, 2, "CCCC-002")
	if err := tm.Commit(t2); err != nil {
		t.Fatalf("commit t2: %v", err)
	}

	mustWrite(t, tm, t3, 0, "aaaat3b!")

	mustWrite(t, tm, t4, 3, "ddddt4b!")
	if err := tm.StartAbort(t4); err != nil {
		t.Fatalf("start abort t4: %v", err)
	}
	// Undo exactly one operation, then "crash" before finishing the abort.
	more, err := tm.RollbackStep(t4)
	if err != nil || !more {
		t.Fatalf("rollback step t4: more=%v err=%v", more, err)
	}

	mustWrite(t, tm, t5, 1, "bbbbt5b!")

	// --- Crash -----------------------------------------------------------
	// The log tail is durable; the buffer pool (with all dirty pages) is
	// lost. Do NOT flush the pool.
	if err := wal.Flush(); err != nil {
		t.Fatalf("flush wal: %v", err)
	}
	wal.Close()
	store.Close()

	// --- Reboot & recover -------------------------------------------------
	wal2, store2, pool2, _ := cs.open(t)
	defer wal2.Close()
	defer store2.Close()

	stats, err := Recover(wal2, pool2)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}

	// Analysis: t3, t4, t5 are losers; t2 is a winner (committed after the
	// checkpoint). t1 ended before the checkpoint and is long gone.
	if want := []uint64{t3, t4, t5}; !reflect.DeepEqual(stats.Losers, want) {
		t.Fatalf("losers = %v, want %v", stats.Losers, want)
	}
	if want := []uint64{t2}; !reflect.DeepEqual(stats.Winners, want) {
		t.Fatalf("winners = %v, want %v", stats.Winners, want)
	}
	if stats.CheckpointLSN != chkLSN {
		t.Fatalf("analysis started at checkpoint LSN %d, want %d", stats.CheckpointLSN, chkLSN)
	}

	// Undo: t3 has 2 updates, t4 has 1 remaining (one was already
	// compensated by the runtime CLR), t5 has 2 => 5 CLRs during recovery.
	if stats.UndoneOps != 5 {
		t.Fatalf("undone ops = %d, want 5", stats.UndoneOps)
	}
	if stats.RedoneOps == 0 {
		t.Fatal("redo applied nothing; history was not repeated")
	}

	// Committed data must survive; uncommitted data must be gone.
	checks := []struct {
		page uint32
		want string
	}{
		{0, "AAAA0001"},                         // t1 committed; t3's uncommitted changes rolled back
		{1, "BBBB0001"},                         // t1 committed; t5's uncommitted changes rolled back
		{2, "CCCC-002"},                         // t2 committed after the checkpoint
		{3, "\x00\x00\x00\x00\x00\x00\x00\x00"}, // t4 fully rolled back
	}
	for _, c := range checks {
		if got := pageString(t, store2, c.page, 8); got != c.want {
			t.Fatalf("page %d = %q, want %q", c.page, got, c.want)
		}
	}

	// Every loser must have an END record now, and each undo a CLR.
	var ends, clrs int
	if err := wal2.Scan(func(r *LogRecord) bool {
		switch r.Type {
		case LogEnd:
			ends++
		case LogCLR:
			clrs++
		}
		return true
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if ends != 5 { // t1, t2 committed-ends + t3, t4, t5 recovery-ends
		t.Fatalf("END records = %d, want 5", ends)
	}
	if clrs != 6 { // 1 runtime CLR (t4) + 5 recovery CLRs
		t.Fatalf("CLR records = %d, want 6", clrs)
	}

	// Recovery must be idempotent: a second crash/recovery changes nothing.
	wal2.Flush()
	stats2, err := Recover(wal2, pool2)
	if err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if stats2.UndoneOps != 0 || len(stats2.Losers) != 0 {
		t.Fatalf("second recovery not a no-op: %+v", stats2)
	}
	for _, c := range checks {
		if got := pageString(t, store2, c.page, 8); got != c.want {
			t.Fatalf("after re-recovery page %d = %q, want %q", c.page, got, c.want)
		}
	}
}

// TestRecoveryWithoutCheckpoint verifies analysis from the log start when
// no checkpoint exists.
func TestRecoveryWithoutCheckpoint(t *testing.T) {
	cs := newCrashSim(t)
	defer cleanup(cs.dir)

	wal, store, _, tm := cs.open(t)
	tx := tm.Begin()
	mustWrite(t, tm, tx, 0, "committed")
	ty := tm.Begin()
	mustWrite(t, tm, ty, 1, "doomed!!")
	if err := tm.Commit(tx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	wal.Flush()
	wal.Close()
	store.Close()

	wal2, store2, pool2, _ := cs.open(t)
	defer wal2.Close()
	defer store2.Close()
	stats, err := Recover(wal2, pool2)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if stats.CheckpointLSN != 0 {
		t.Fatalf("unexpected checkpoint LSN %d", stats.CheckpointLSN)
	}
	if !reflect.DeepEqual(stats.Losers, []uint64{ty}) {
		t.Fatalf("losers = %v, want [%d]", stats.Losers, ty)
	}
	if got := pageString(t, store2, 0, 9); got != "committed" {
		t.Fatalf("page 0 = %q, want %q", got, "committed")
	}
	if got := pageString(t, store2, 1, 8); got != "\x00\x00\x00\x00\x00\x00\x00\x00" {
		t.Fatalf("page 1 = %q, want zeros", got)
	}
}

// TestRuntimeAbort verifies synchronous abort via CLRs while running.
func TestRuntimeAbort(t *testing.T) {
	cs := newCrashSim(t)
	defer cleanup(cs.dir)

	wal, store, pool, tm := cs.open(t)
	defer wal.Close()
	defer store.Close()

	tx := tm.Begin()
	mustWrite(t, tm, tx, 0, "11111111")
	mustWrite(t, tm, tx, 0, "22222222")
	if err := tm.Abort(tx); err != nil {
		t.Fatalf("abort: %v", err)
	}
	if err := pool.FlushAll(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := pageString(t, store, 0, 8); got != "\x00\x00\x00\x00\x00\x00\x00\x00" {
		t.Fatalf("page 0 = %q after abort, want zeros", got)
	}

	// The WAL must tell the whole story: UPDATE UPDATE ABORT CLR CLR END.
	var kinds []string
	wal.Scan(func(r *LogRecord) bool {
		kinds = append(kinds, r.Type.String())
		return true
	})
	want := "UPDATE UPDATE ABORT CLR CLR END"
	if got := strings.Join(kinds, " "); got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}
