package aries

import (
	"errors"
	"path/filepath"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func openPair(t *testing.T, dir string, inj *FaultInjector) (*WAL, *PageStore) {
	t.Helper()
	wal, err := OpenWAL(filepath.Join(dir, "wal.log"), inj)
	must(t, err)
	disk, err := OpenPageStore(filepath.Join(dir, "data.db"), inj)
	must(t, err)
	return wal, disk
}

func assertPage(t *testing.T, disk *PageStore, id uint64, want string) {
	t.Helper()
	page, err := disk.ReadPage(id)
	must(t, err)
	if got := string(trim(page.Data)); got != want {
		t.Fatalf("page %d = %q, want %q", id, got, want)
	}
}

// buildAcceptanceScenario writes the acceptance WAL: 2 committed
// transactions (T1 fully ended, T2 missing its END record) and 3
// uncommitted ones (T3 running, T4 aborting with one CLR already written,
// T5 running), with a checkpoint in the middle, then "crashes" by closing
// the files without flushing the dirty buffer pool.
func buildAcceptanceScenario(t *testing.T, dir string, inj *FaultInjector) {
	t.Helper()
	wal, disk := openPair(t, dir, inj)
	tm := NewTxnManager(wal, disk)

	must(t, tm.Begin(1))
	must(t, tm.Update(1, 1, []byte("A1")))
	must(t, tm.Update(1, 2, []byte("B1")))
	must(t, tm.Commit(1, true)) // committed + END

	must(t, tm.Begin(2))
	must(t, tm.Update(2, 3, []byte("C2")))
	must(t, tm.Commit(2, false)) // committed, END not yet written

	must(t, tm.FlushPage(1)) // only committed data reaches disk
	must(t, tm.FlushPage(2))
	must(t, tm.Checkpoint())

	must(t, tm.Begin(3))
	must(t, tm.Update(3, 1, []byte("A3"))) // uncommitted, never flushed

	must(t, tm.Begin(4))
	must(t, tm.Update(4, 4, []byte("D4")))
	must(t, tm.Abort(4, 1)) // aborting: one CLR written, crash mid-abort

	must(t, tm.Begin(5))
	must(t, tm.Update(5, 2, []byte("B5")))
	must(t, tm.Update(5, 5, []byte("E5")))

	// Crash: buffer pool (dirty pages) is lost with the process image.
	must(t, wal.Close())
	must(t, disk.Close())
}

func assertAcceptanceResult(t *testing.T, disk *PageStore) {
	t.Helper()
	assertPage(t, disk, 1, "A1") // T1 committed, T3's uncommitted A3 rolled back
	assertPage(t, disk, 2, "B1") // T1 committed, T5's uncommitted B5 rolled back
	assertPage(t, disk, 3, "C2") // T2 committed, restored by Redo
	assertPage(t, disk, 4, "")   // T4 aborted
	assertPage(t, disk, 5, "")   // T5 never committed
}

// TestRecover_AcceptanceScenario is the acceptance boundary: 3 uncommitted +
// 2 committed transactions, crash after a checkpoint, full three-phase
// recovery.
func TestRecover_AcceptanceScenario(t *testing.T) {
	dir := t.TempDir()
	inj := NewFaultInjector()
	buildAcceptanceScenario(t, dir, inj)

	wal, disk := openPair(t, dir, inj)
	defer wal.Close()
	defer disk.Close()
	eng := NewEngine(wal, disk)

	// ---- Analysis ----
	must(t, eng.Analysis())
	if len(eng.TxnTable) != 4 {
		t.Fatalf("txn table size = %d, want 4 (T2,T3,T4,T5): %v", len(eng.TxnTable), eng.TxnTable)
	}
	wantStatus := map[uint64]TxnStatus{2: TxnCommitted, 3: TxnRunning, 4: TxnAborting, 5: TxnRunning}
	for id, st := range wantStatus {
		entry, ok := eng.TxnTable[id]
		if !ok {
			t.Fatalf("txn %d missing from txn table", id)
		}
		if entry.Status != st {
			t.Fatalf("txn %d status = %s, want %s", id, entry.Status, st)
		}
	}
	if _, ok := eng.TxnTable[1]; ok {
		t.Fatalf("T1 ended before the checkpoint and must not be in the txn table")
	}
	if len(eng.DPT) != 5 {
		t.Fatalf("DPT size = %d, want 5: %v", len(eng.DPT), eng.DPT)
	}

	// ---- Redo + Undo ----
	must(t, eng.Redo())
	must(t, eng.Undo())

	assertAcceptanceResult(t, disk)

	if eng.Undone != 3 {
		t.Fatalf("undone updates = %d, want 3 (T3x1, T5x2)", eng.Undone)
	}
	if eng.CLRsWritten != 3 {
		t.Fatalf("CLRs written during undo = %d, want 3", eng.CLRsWritten)
	}
	if eng.EndsWritten != 4 {
		t.Fatalf("ENDs written = %d, want 4 (T2,T3,T4,T5)", eng.EndsWritten)
	}
	if len(eng.TxnTable) != 0 {
		t.Fatalf("txn table not empty after recovery: %v", eng.TxnTable)
	}

	// WAL bookkeeping: 1 pre-crash CLR (T4) + 3 undo CLRs; 1 pre-crash END
	// (T1) + 4 recovery ENDs.
	recs, err := wal.Scan()
	must(t, err)
	var clrs, ends int
	for _, r := range recs {
		switch r.Type {
		case RecCLR:
			clrs++
		case RecEnd:
			ends++
		}
	}
	if clrs != 4 {
		t.Fatalf("total CLRs in WAL = %d, want 4", clrs)
	}
	if ends != 5 {
		t.Fatalf("total ENDs in WAL = %d, want 5", ends)
	}
}

// TestRecover_Idempotent verifies that running recovery a second time is a
// no-op: pageLSN guards make Redo skip everything and no losers remain.
func TestRecover_Idempotent(t *testing.T) {
	dir := t.TempDir()
	inj := NewFaultInjector()
	buildAcceptanceScenario(t, dir, inj)

	wal, disk := openPair(t, dir, inj)
	defer wal.Close()
	defer disk.Close()

	first := NewEngine(wal, disk)
	must(t, first.Recover())
	assertAcceptanceResult(t, disk)

	second := NewEngine(wal, disk)
	must(t, second.Recover())
	assertAcceptanceResult(t, disk)
	if second.Redone != 0 || second.Undone != 0 || second.CLRsWritten != 0 || second.EndsWritten != 0 {
		t.Fatalf("second recovery must be a no-op, got redone=%d undone=%d clrs=%d ends=%d",
			second.Redone, second.Undone, second.CLRsWritten, second.EndsWritten)
	}
}

// TestRecover_EmptyAndCheckpointOnly covers the degenerate boundaries.
func TestRecover_EmptyAndCheckpointOnly(t *testing.T) {
	t.Run("empty WAL", func(t *testing.T) {
		dir := t.TempDir()
		wal, disk := openPair(t, dir, NewFaultInjector())
		defer wal.Close()
		defer disk.Close()
		eng := NewEngine(wal, disk)
		must(t, eng.Recover())
		if len(eng.TxnTable) != 0 || len(eng.DPT) != 0 {
			t.Fatalf("tables must be empty for an empty WAL")
		}
	})
	t.Run("checkpoint only", func(t *testing.T) {
		dir := t.TempDir()
		wal, disk := openPair(t, dir, NewFaultInjector())
		tm := NewTxnManager(wal, disk)
		must(t, tm.Checkpoint()) // empty snapshot
		must(t, wal.Close())
		must(t, disk.Close())

		wal2, disk2 := openPair(t, dir, NewFaultInjector())
		defer wal2.Close()
		defer disk2.Close()
		eng := NewEngine(wal2, disk2)
		must(t, eng.Recover())
		if eng.Redone != 0 || eng.Undone != 0 {
			t.Fatalf("nothing to redo/undo after empty checkpoint")
		}
	})
	t.Run("crash immediately after checkpoint", func(t *testing.T) {
		dir := t.TempDir()
		inj := NewFaultInjector()
		wal, disk := openPair(t, dir, inj)
		tm := NewTxnManager(wal, disk)
		must(t, tm.Begin(1))
		must(t, tm.Update(1, 1, []byte("x")))
		must(t, tm.Commit(1, true))
		must(t, tm.Checkpoint())
		must(t, wal.Close())
		must(t, disk.Close())

		wal2, disk2 := openPair(t, dir, inj)
		defer wal2.Close()
		defer disk2.Close()
		eng := NewEngine(wal2, disk2)
		must(t, eng.Recover())
		assertPage(t, disk2, 1, "x")
		if len(eng.TxnTable) != 0 {
			t.Fatalf("no transaction may survive recovery")
		}
	})
}

// TestPowerLoss_TornTail simulates a power cut in the middle of the
// workload: the last record lands torn on disk, the machine dies, and
// recovery must ignore the torn tail and roll back the loser.
func TestPowerLoss_TornTail(t *testing.T) {
	dir := t.TempDir()
	inj := NewFaultInjector()
	wal, disk := openPair(t, dir, inj)
	tm := NewTxnManager(wal, disk)

	inj.LogWritesLeft = 5 // 5 writes succeed, the 6th tears and kills power
	must(t, tm.Begin(1))
	must(t, tm.Update(1, 1, []byte("committed")))
	must(t, tm.Commit(1, true))
	must(t, tm.Begin(2))
	if err := tm.Update(2, 2, []byte("uncommitted")); !errors.Is(err, ErrPowerLoss) {
		t.Fatalf("6th log write must fail with power loss, got %v", err)
	}
	if err := tm.Commit(2, false); !errors.Is(err, ErrPowerLoss) {
		t.Fatalf("machine must stay dead after power loss, got %v", err)
	}
	if inj.TornWrites != 1 || inj.PowerLossCount != 1 {
		t.Fatalf("expected exactly one torn write and one power loss, got %d/%d",
			inj.TornWrites, inj.PowerLossCount)
	}
	wal.Close()
	disk.Close()

	// Reboot: torn tail is truncated on open, recovery proceeds.
	inj.Reboot()
	wal2, disk2 := openPair(t, dir, inj)
	defer wal2.Close()
	defer disk2.Close()
	eng := NewEngine(wal2, disk2)
	must(t, eng.Recover())
	assertPage(t, disk2, 1, "committed")
	assertPage(t, disk2, 2, "")
	if len(eng.TxnTable) != 0 {
		t.Fatalf("loser T2 must be ended by recovery")
	}
}

// TestDiskFull_DuringRecovery verifies that ENOSPC during recovery is a
// clean, retryable failure: no state is corrupted and a later retry
// completes the recovery.
func TestDiskFull_DuringRecovery(t *testing.T) {
	dir := t.TempDir()
	inj := NewFaultInjector()
	buildAcceptanceScenario(t, dir, inj)

	wal, disk := openPair(t, dir, inj)
	defer wal.Close()
	defer disk.Close()

	inj.DiskFull = true
	if err := NewEngine(wal, disk).Recover(); !errors.Is(err, ErrDiskFull) {
		t.Fatalf("recovery under disk pressure must fail with ErrDiskFull, got %v", err)
	}
	if inj.DiskFullCount == 0 {
		t.Fatalf("fault injector never observed a disk-full write")
	}

	inj.DiskFull = false
	eng := NewEngine(wal, disk)
	must(t, eng.Recover())
	assertAcceptanceResult(t, disk)
}

// TestPowerLoss_DuringUndo kills the power while recovery itself is writing
// CLRs, then reboots and re-runs recovery. The CLR/undoNext machinery must
// resume the interrupted undo exactly once.
func TestPowerLoss_DuringUndo(t *testing.T) {
	dir := t.TempDir()
	inj := NewFaultInjector()
	buildAcceptanceScenario(t, dir, inj)

	wal, disk := openPair(t, dir, inj)
	inj.LogWritesLeft = 2 // die in the middle of undo logging
	if err := NewEngine(wal, disk).Recover(); !errors.Is(err, ErrPowerLoss) {
		t.Fatalf("recovery must be interrupted by power loss, got %v", err)
	}
	wal.Close()
	disk.Close()

	inj.Reboot()
	wal2, disk2 := openPair(t, dir, inj) // torn CLR tail truncated on open
	defer wal2.Close()
	defer disk2.Close()
	eng := NewEngine(wal2, disk2)
	must(t, eng.Recover())
	assertAcceptanceResult(t, disk2)

	// Every undo must have happened exactly once: re-scan and confirm each
	// loser transaction has exactly one END.
	recs, err := wal2.Scan()
	must(t, err)
	endCount := map[uint64]int{}
	for _, r := range recs {
		if r.Type == RecEnd {
			endCount[r.TxnID]++
		}
	}
	for _, id := range []uint64{1, 2, 3, 4, 5} {
		if endCount[id] != 1 {
			t.Fatalf("txn %d has %d END records, want exactly 1", id, endCount[id])
		}
	}
}
