package recovery

import (
	"bytes"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"aries/wal"
)

// crashFixture builds the canonical scenario WAL and returns everything a
// recovery run needs.
func crashFixture(t *testing.T) (logPath string, endOfLog uint64, sc *Scenario) {
	t.Helper()
	logPath = filepath.Join(t.TempDir(), "wal.log")
	w, err := wal.NewWriter(logPath)
	if err != nil {
		t.Fatal(err)
	}
	sc, err = BuildCrashScenario(w)
	if err != nil {
		t.Fatal(err)
	}
	endOfLog = w.NextLSN()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return logPath, endOfLog, sc
}

func recoverOnce(t *testing.T, logPath string, endOfLog uint64, sc *Scenario) (*Report, *Storage) {
	t.Helper()
	r, err := wal.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	store := NewStorage()
	for _, p := range sc.PageIDs {
		store.SeedCleanPage(p, make([]byte, 16))
	}
	aw, err := wal.AppendWriter(logPath, endOfLog)
	if err != nil {
		t.Fatal(err)
	}
	defer aw.Close()
	rep, err := NewEngine(store).Recover(r, aw)
	if err != nil {
		t.Fatal(err)
	}
	return rep, store
}

// TestCrashRecoveryAcceptance is the acceptance boundary: 3 uncommitted and
// 2 committed transactions, crash after a checkpoint, full three-phase
// recovery must roll back the losers and persist the winners.
func TestCrashRecoveryAcceptance(t *testing.T) {
	logPath, endOfLog, sc := crashFixture(t)
	rep, store := recoverOnce(t, logPath, endOfLog, sc)

	// Analysis: winners committed, losers running/aborting.
	wantStatus := map[uint64]wal.TxnStatus{
		1: wal.TxnCommitted, 2: wal.TxnCommitted,
		3: wal.TxnRunning, 4: wal.TxnRunning, 5: wal.TxnAborting,
	}
	if len(rep.Txns) != len(wantStatus) {
		t.Fatalf("ATT size: want %d got %d", len(wantStatus), len(rep.Txns))
	}
	for id, st := range wantStatus {
		if rep.Txns[id].Status != st {
			t.Fatalf("txn %d status: want %d got %d", id, st, rep.Txns[id].Status)
		}
	}
	if len(rep.DirtyPages) != 3 {
		t.Fatalf("DPT size: want 3 got %d", len(rep.DirtyPages))
	}

	// Redo repeats history: all 9 update/CLR records re-applied.
	if rep.Redone != 9 || rep.RedoSkip != 0 {
		t.Fatalf("redo: want 9/0 got %d/%d", rep.Redone, rep.RedoSkip)
	}

	// Undo: exactly the three losers, one CLR per loser update (T3 x2, T4 x1;
	// T5's update was already compensated before the crash).
	if !reflect.DeepEqual(rep.UndoneTxns, []uint64{3, 4, 5}) {
		t.Fatalf("losers: %v", rep.UndoneTxns)
	}
	if rep.CLRs != 3 {
		t.Fatalf("CLRs: want 3 got %d", rep.CLRs)
	}
	if rep.Ends != 5 { // END for every transaction in the system
		t.Fatalf("ENDs: want 5 got %d", rep.Ends)
	}

	// Final durable state: winners persisted, losers rolled back.
	for _, p := range sc.PageIDs {
		pg, ok := store.ReadPage(p)
		if !ok {
			t.Fatalf("page %d missing", p)
		}
		if !bytes.Equal(pg.Data[:len(sc.Expect[p])], sc.Expect[p]) {
			t.Fatalf("page %d: want %q got %q", p, sc.Expect[p], pg.Data[:len(sc.Expect[p])])
		}
	}

	// The WAL itself must now be a valid, fully parseable log containing the
	// compensation records.
	r, err := wal.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	recs, err := r.Records()
	if err != nil {
		t.Fatalf("post-recovery log invalid: %v", err)
	}
	var clrs, ends int
	for _, rec := range recs {
		switch rec.Type {
		case wal.RecCLR:
			clrs++
		case wal.RecEnd:
			ends++
		}
	}
	if clrs != 4 || ends != 5 { // 1 pre-crash CLR + 3 undo CLRs
		t.Fatalf("post-recovery log: want 4 CLRs / 5 ENDs got %d/%d", clrs, ends)
	}
}

// TestRecoveryIdempotent: recovering twice must be a no-op the second time
// (CLRs make undo restart-safe; pageLSN makes redo skip durable records).
func TestRecoveryIdempotent(t *testing.T) {
	logPath, endOfLog, sc := crashFixture(t)
	rep1, store1 := recoverOnce(t, logPath, endOfLog, sc)

	r, err := wal.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	aw, err := wal.AppendWriter(logPath, aw_nextLSN(t, logPath))
	if err != nil {
		t.Fatal(err)
	}
	defer aw.Close()
	rep2, err := NewEngine(store1).Recover(r, aw)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Redone != 0 || rep2.CLRs != 0 || len(rep2.UndoneTxns) != 0 {
		t.Fatalf("second recovery not a no-op: %+v", rep2)
	}
	_ = rep1
}

func aw_nextLSN(t *testing.T, logPath string) uint64 {
	t.Helper()
	r, err := wal.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	recs, err := r.ScanValid()
	if err != nil {
		t.Fatal(err)
	}
	last := recs[len(recs)-1]
	return last.LSN + uint64(wal.EncodedSize(last))
}

// TestRedoRespectsPageLSN: updates already durable on disk are not re-applied.
func TestRedoRespectsPageLSN(t *testing.T) {
	logPath, endOfLog, sc := crashFixture(t)
	r, err := wal.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	recs, err := r.ScanValid()
	if err != nil {
		t.Fatal(err)
	}
	// Pretend the first update to page 1 (LSN 0) already reached disk.
	first := recs[0]
	store := NewStorage()
	for _, p := range sc.PageIDs {
		store.SeedCleanPage(p, make([]byte, 16))
	}
	store.SeedPage(first.PageID, first.LSN, []byte("AAAA\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"))

	aw, err := wal.AppendWriter(logPath, endOfLog)
	if err != nil {
		t.Fatal(err)
	}
	defer aw.Close()
	rep, err := NewEngine(store).Recover(r, aw)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Redone != 8 || rep.RedoSkip != 1 {
		t.Fatalf("redo with pre-durable page: want 8/1 got %d/%d", rep.Redone, rep.RedoSkip)
	}
	pg, _ := store.ReadPage(1)
	if !bytes.Equal(pg.Data[:12], sc.Expect[1]) {
		t.Fatalf("page 1: want %q got %q", sc.Expect[1], pg.Data[:12])
	}
}

// TestConcurrentStorage hammers Storage from many goroutines (run with -race).
func TestConcurrentStorage(t *testing.T) {
	s := NewStorage()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s.Apply(id%4, uint16(i%8), []byte{byte(i)}, uint64(i+1))
				s.ReadPage(id % 4)
				s.PageLSN(id % 4)
			}
		}(uint64(g))
	}
	wg.Wait()
	if s.Writes() != 16*200 {
		t.Fatalf("writes: want %d got %d", 16*200, s.Writes())
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()
	logPath, endOfLog, sc := crashFixture(t)
	recoverOnce(t, logPath, endOfLog, sc)
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
