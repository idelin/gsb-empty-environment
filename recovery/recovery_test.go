package recovery

import (
	"encoding/binary"
	"io"
	"path/filepath"
	"testing"

	"aries/store"
	"aries/wal"
)

func u64val(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return b[:]
}

func readU64(data []byte) uint64 {
	return binary.BigEndian.Uint64(data[:8])
}

// crashSite holds the durable artifacts left behind by a crash.
type crashSite struct {
	walPath string
}

// buildAcceptanceWAL creates the acceptance-boundary workload:
//
//	T1, T2 committed; T3, T4, T5 uncommitted; a checkpoint taken after
//	T1/T2's first updates; then a crash. Pages 1 and 2 (containing
//	uncommitted data) are forced to disk before the crash; pages 3, 4
//	(committed) and 5 (uncommitted) exist only in the lost buffer pool.
//
// Expected post-recovery durable state:
//
//	P1=100 (T1 kept, T3 undone)  P2=200 (T2 kept, T4 undone)
//	P3=300 (T1 redone)           P4=400 (T2 redone)  P5=0 (T5 undone)
func buildAcceptanceWAL(t *testing.T) crashSite {
	t.Helper()
	dir := t.TempDir()
	walPath := filepath.Join(dir, "acceptance.wal")

	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	st := store.New(w)
	rt := NewRuntime(w, st)

	rt.Begin(1)
	rt.Begin(2)
	if _, err := rt.Update(1, 1, 0, u64val(0), u64val(100)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := rt.Update(2, 2, 0, u64val(0), u64val(200)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := rt.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, err := rt.Update(1, 3, 0, u64val(0), u64val(300)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := rt.Commit(1); err != nil {
		t.Fatalf("commit t1: %v", err)
	}
	if err := rt.End(1); err != nil {
		t.Fatalf("end t1: %v", err)
	}

	rt.Begin(3)
	if _, err := rt.Update(3, 1, 0, u64val(100), u64val(150)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := rt.Update(2, 4, 0, u64val(0), u64val(400)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := rt.Commit(2); err != nil { // committed, but END never written
		t.Fatalf("commit t2: %v", err)
	}

	rt.Begin(4)
	rt.Begin(5)
	if _, err := rt.Update(4, 2, 0, u64val(200), u64val(250)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := rt.Update(5, 5, 0, u64val(0), u64val(500)); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := w.Flush(); err != nil {
		t.Fatalf("flush wal: %v", err)
	}
	// Force pages holding uncommitted data to disk (steal policy).
	if err := rt.FlushPage(1); err != nil {
		t.Fatalf("flush p1: %v", err)
	}
	if err := rt.FlushPage(2); err != nil {
		t.Fatalf("flush p2: %v", err)
	}

	// Crash: volatile buffer pool (pages 3, 4, 5) is lost.
	st.Crash()
	if err := w.Close(); err != nil {
		t.Fatalf("close wal: %v", err)
	}
	return crashSite{walPath: walPath}
}

func reopen(t *testing.T, site crashSite) (*wal.WAL, *store.Store) {
	t.Helper()
	w, err := wal.Open(site.walPath)
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	return w, store.New(w)
}

func assertFinalState(t *testing.T, st *store.Store) {
	t.Helper()
	want := map[uint64]uint64{1: 100, 2: 200, 3: 300, 4: 400, 5: 0}
	for page, val := range want {
		data, _ := st.DiskPage(page)
		if got := readU64(data); got != val {
			t.Fatalf("page %d: got %d, want %d", page, got, val)
		}
	}
}

func TestAcceptanceCrashRecovery(t *testing.T) {
	site := buildAcceptanceWAL(t)
	w, st := reopen(t, site)
	defer w.Close()

	eng := &Engine{WAL: w, Store: st}
	if err := eng.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	assertFinalState(t, st)

	// The log must now contain one CLR per loser update and END
	// records for every transaction.
	var clrs, ends int
	endSeen := map[uint64]bool{}
	rd := w.ReaderFrom(0)
	for {
		rec, err := rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch rec.Type {
		case wal.RecCLR:
			clrs++
		case wal.RecEnd:
			ends++
			endSeen[rec.TxnID] = true
		}
	}
	if clrs != 3 {
		t.Fatalf("expected 3 CLRs (one per loser update), got %d", clrs)
	}
	for _, id := range []uint64{1, 2, 3, 4, 5} {
		if !endSeen[id] {
			t.Fatalf("missing END record for txn %d", id)
		}
	}
}

func TestAnalysisTables(t *testing.T) {
	site := buildAcceptanceWAL(t)
	w, st := reopen(t, site)
	defer w.Close()

	eng := &Engine{WAL: w, Store: st}
	tt, dpt, redoLSN, err := eng.Analysis()
	if err != nil {
		t.Fatalf("analysis: %v", err)
	}

	// T1 wrote END (removed); T2 committed without END; T3/T4/T5 running.
	if _, ok := tt[1]; ok {
		t.Fatalf("txn 1 should have been removed from the transaction table")
	}
	if att := tt[2]; att == nil || att.Status != wal.StatusCommitted {
		t.Fatalf("txn 2 should be COMMITTED, got %+v", att)
	}
	for _, id := range []uint64{3, 4, 5} {
		if att := tt[id]; att == nil || att.Status != wal.StatusRunning {
			t.Fatalf("txn %d should be RUNNING, got %+v", id, att)
		}
	}

	// All five pages are dirty; recLSN of P1/P2 comes from the
	// checkpoint, the rest from their first update after it.
	if len(dpt) != 5 {
		t.Fatalf("expected 5 dirty pages, got %d (%v)", len(dpt), dpt)
	}
	for page, recLSN := range dpt {
		rec, err := w.ReadAt(recLSN)
		if err != nil {
			t.Fatalf("read recLSN of page %d: %v", page, err)
		}
		if rec.Type != wal.RecUpdate || rec.PageID != page {
			t.Fatalf("page %d recLSN points at %s for page %d", page, rec.Type, rec.PageID)
		}
	}
	if redoLSN != dpt[1] && redoLSN != dpt[2] {
		t.Fatalf("redoLSN %d should be the smallest recLSN (pages 1/2 from checkpoint)", redoLSN)
	}
}

func TestRecoveryIsIdempotent(t *testing.T) {
	site := buildAcceptanceWAL(t)

	w, st := reopen(t, site)
	eng := &Engine{WAL: w, Store: st}
	if err := eng.Recover(); err != nil {
		t.Fatalf("first recover: %v", err)
	}
	assertFinalState(t, st)
	logEnd := w.End()
	w.Close()

	// Crash again immediately after recovery, recover once more.
	w2, st2 := reopen(t, site)
	defer w2.Close()
	eng2 := &Engine{WAL: w2, Store: st2}
	if err := eng2.Recover(); err != nil {
		t.Fatalf("second recover: %v", err)
	}
	assertFinalState(t, st2)
	if w2.End() != logEnd {
		t.Fatalf("second recovery should append nothing, log grew %d -> %d", logEnd, w2.End())
	}
}

func TestCrashDuringUndo(t *testing.T) {
	site := buildAcceptanceWAL(t)

	// First attempt crashes after applying exactly one CLR.
	w, st := reopen(t, site)
	eng := &Engine{WAL: w, Store: st, CrashAfterUndo: 1}
	err := eng.Recover()
	if err != ErrSimulatedCrash {
		t.Fatalf("expected simulated crash, got %v", err)
	}
	w.Close()

	// Second attempt must resume undo via CLR undoNext chains and
	// never undo the same update twice.
	w2, st2 := reopen(t, site)
	defer w2.Close()
	eng2 := &Engine{WAL: w2, Store: st2}
	if err := eng2.Recover(); err != nil {
		t.Fatalf("recover after undo crash: %v", err)
	}
	assertFinalState(t, st2)
}

func TestAbortingTransactionIsUndone(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "abort.wal")
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	st := store.New(w)
	rt := NewRuntime(w, st)

	rt.Begin(1)
	if _, err := rt.Update(1, 1, 0, u64val(0), u64val(10)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := rt.Update(1, 1, 8, u64val(0), u64val(20)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := rt.Abort(1); err != nil { // ABORT in log, rollback incomplete at crash
		t.Fatalf("abort: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := rt.FlushPage(1); err != nil {
		t.Fatalf("flush page: %v", err)
	}
	st.Crash()
	w.Close()

	w2, st2 := reopen(t, crashSite{walPath: walPath})
	defer w2.Close()
	eng := &Engine{WAL: w2, Store: st2}
	tt, _, _, err := eng.Analysis()
	if err != nil {
		t.Fatalf("analysis: %v", err)
	}
	if att := tt[1]; att == nil || att.Status != wal.StatusAborting {
		t.Fatalf("txn 1 should be ABORTING, got %+v", att)
	}
	if err := eng.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	data, _ := st2.DiskPage(1)
	if got := readU64(data); got != 0 {
		t.Fatalf("aborted txn update at off 0 not undone: got %d", got)
	}
	if got := readU64(data[8:]); got != 0 {
		t.Fatalf("aborted txn update at off 8 not undone: got %d", got)
	}
}

func TestRecoverEmptyLog(t *testing.T) {
	dir := t.TempDir()
	w, err := wal.Open(filepath.Join(dir, "empty.wal"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer w.Close()
	st := store.New(w)
	eng := &Engine{WAL: w, Store: st}
	if err := eng.Recover(); err != nil {
		t.Fatalf("recover empty log: %v", err)
	}
}

// TestUndoFollowsFullPrevLSNChain is a regression test for the
// LSN-0 ambiguity: the first record of a log sits at a small LSN,
// which must not be confused with the "no previous record" sentinel.
// A loser with several updates must be undone completely.
func TestUndoFollowsFullPrevLSNChain(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "chain.wal")
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	st := store.New(w)
	rt := NewRuntime(w, st)

	rt.Begin(1)
	for page := uint64(1); page <= 4; page++ {
		if _, err := rt.Update(1, page, 0, u64val(0), u64val(page*10)); err != nil {
			t.Fatalf("update: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	for page := uint64(1); page <= 4; page++ {
		if err := rt.FlushPage(page); err != nil {
			t.Fatalf("flush page: %v", err)
		}
	}
	st.Crash()
	w.Close()

	w2, st2 := reopen(t, crashSite{walPath: walPath})
	defer w2.Close()
	eng := &Engine{WAL: w2, Store: st2}
	if err := eng.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	for page := uint64(1); page <= 4; page++ {
		data, _ := st2.DiskPage(page)
		if got := readU64(data); got != 0 {
			t.Fatalf("page %d: update not undone (got %d); prevLSN chain broken", page, got)
		}
	}
}
