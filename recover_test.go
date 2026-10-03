package aries

import (
	"bytes"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// crashScenario builds the acceptance WAL:
//   - 5 transactions: t2 and t4 commit; t1, t3, t5 are uncommitted at crash.
//   - a checkpoint sits in the middle of the log.
//   - t2 gets its END record after the checkpoint.
//   - before the crash, only P1 (stale: before t3's 2nd update) and P2
//     (stale: before t5's update) are flushed; P3 never reaches disk.
type crashScenario struct {
	walPath string
	dbPath  string
	lsn     map[string]uint64
}

func buildCrashScenario(t *testing.T, dir string) *crashScenario {
	t.Helper()
	s := &crashScenario{
		walPath: filepath.Join(dir, "wal.log"),
		dbPath:  filepath.Join(dir, "db.dat"),
		lsn:     make(map[string]uint64),
	}
	wal, err := CreateWAL(s.walPath)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := OpenPageStore(s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	prev := map[uint64]uint64{}

	update := func(name string, txn uint64, page uint32, off uint32, newData []byte) {
		old, err := ps.ReadAt(page, off, len(newData))
		if err != nil {
			t.Fatal(err)
		}
		rec := &Record{Type: TypeUpdate, TxnID: txn, PrevLSN: prev[txn], PageID: page,
			Offset: off, OldData: old, NewData: newData}
		l, err := wal.Append(rec)
		if err != nil {
			t.Fatal(err)
		}
		prev[txn] = l
		s.lsn[name] = l
		if err := ps.ApplyUpdate(page, off, newData, l); err != nil {
			t.Fatal(err)
		}
	}
	txnRec := func(typ byte, txn uint64) uint64 {
		l, err := wal.Append(&Record{Type: typ, TxnID: txn, PrevLSN: prev[txn]})
		if err != nil {
			t.Fatal(err)
		}
		prev[txn] = l
		return l
	}

	// --- before checkpoint ---
	update("t1u1", 1, 1, 0, []byte("A1")) // t1 -> P1[0:2]
	update("t2u1", 2, 2, 0, []byte("B1")) // t2 -> P2[0:2]
	s.lsn["t2commit"] = txnRec(TypeCommit, 2)
	update("t3u1", 3, 1, 8, []byte("C1")) // t3 -> P1[8:10]

	// --- checkpoint ---
	beginLSN, err := wal.Append(&Record{Type: TypeBeginCheckpoint})
	if err != nil {
		t.Fatal(err)
	}
	_, err = wal.Append(&Record{
		Type: TypeEndCheckpoint, BeginLSN: beginLSN,
		Txns: []TxnSnapshot{
			{TxnID: 1, LastLSN: s.lsn["t1u1"], Status: byte(StatusRunning)},
			{TxnID: 2, LastLSN: s.lsn["t2commit"], Status: byte(StatusCommitted)},
			{TxnID: 3, LastLSN: s.lsn["t3u1"], Status: byte(StatusRunning)},
		},
		DirtyPages: []DirtyPageSnapshot{
			{PageID: 1, RecLSN: s.lsn["t1u1"]},
			{PageID: 2, RecLSN: s.lsn["t2u1"]},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Flush P1 and P2 now: they reach disk in this exact (pre-crash stale) state.
	if err := ps.FlushPage(1); err != nil {
		t.Fatal(err)
	}
	if err := ps.FlushPage(2); err != nil {
		t.Fatal(err)
	}

	// --- after checkpoint ---
	txnRec(TypeEnd, 2)                    // t2 fully done
	update("t4u1", 4, 3, 0, []byte("D1")) // t4 -> P3[0:2] (winner)
	s.lsn["t4commit"] = txnRec(TypeCommit, 4)
	update("t5u1", 5, 2, 8, []byte("E1"))  // t5 -> P2[8:10] (loser)
	update("t1u2", 1, 3, 8, []byte("A2"))  // t1 -> P3[8:10] (loser)
	update("t3u2", 3, 1, 16, []byte("C2")) // t3 -> P1[16:18] (loser)

	if err := wal.Sync(); err != nil {
		t.Fatal(err)
	}
	wal.Close()
	// crash: in-memory page state is lost; only the two flushed pages survive.
	return s
}

func pageBytes(t *testing.T, ps *PageStore, page uint32, off uint32, n int) []byte {
	t.Helper()
	b, err := ps.ReadAt(page, off, n)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAnalysisPhase(t *testing.T) {
	s := buildCrashScenario(t, t.TempDir())
	r, err := NewRecovery(s.walPath, s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.WAL.Close()
	r.Analysis()

	// Transaction table: t1, t3, t5 active (losers); t4 committed (winner);
	// t2 removed by its END record.
	if len(r.TT) != 4 {
		t.Fatalf("TT size %d, want 4: %+v", len(r.TT), r.TT)
	}
	check := func(txn uint64, status TxnStatus, lastKey string) {
		e, ok := r.TT[txn]
		if !ok {
			t.Fatalf("txn %d missing from TT", txn)
		}
		if e.Status != status {
			t.Fatalf("txn %d status %v, want %v", txn, e.Status, status)
		}
		if e.LastLSN != s.lsn[lastKey] {
			t.Fatalf("txn %d lastLSN %d, want %d (%s)", txn, e.LastLSN, s.lsn[lastKey], lastKey)
		}
	}
	check(1, StatusRunning, "t1u2")
	check(3, StatusRunning, "t3u2")
	check(4, StatusCommitted, "t4commit")
	check(5, StatusRunning, "t5u1")
	if _, ok := r.TT[2]; ok {
		t.Fatalf("txn 2 should have been removed by its END record")
	}

	// Dirty page table: P1/P2 from the checkpoint snapshot, P3 from the scan.
	wantDPT := map[uint32]uint64{1: s.lsn["t1u1"], 2: s.lsn["t2u1"], 3: s.lsn["t4u1"]}
	if len(r.DPT) != len(wantDPT) {
		t.Fatalf("DPT %+v, want %+v", r.DPT, wantDPT)
	}
	for page, recLSN := range wantDPT {
		if r.DPT[page] != recLSN {
			t.Fatalf("DPT[%d]=%d, want %d", page, r.DPT[page], recLSN)
		}
	}

	losers := r.Losers()
	if len(losers) != 3 || losers[0].TxnID != 1 || losers[1].TxnID != 3 || losers[2].TxnID != 5 {
		t.Fatalf("losers %+v, want txns [1 3 5]", losers)
	}
	winners := r.Winners()
	if len(winners) != 1 || winners[0].TxnID != 4 {
		t.Fatalf("winners %+v, want txn [4]", winners)
	}
}

func TestRedoRepeatsHistory(t *testing.T) {
	s := buildCrashScenario(t, t.TempDir())
	r, err := NewRecovery(s.walPath, s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.WAL.Close()
	r.Analysis()
	if err := r.Redo(); err != nil {
		t.Fatal(err)
	}

	// After redo, even loser updates are physically present (history repeated).
	assert := func(page uint32, off uint32, want string) {
		if got := pageBytes(t, r.Pages, page, off, 2); !bytes.Equal(got, []byte(want)) {
			t.Fatalf("P%d[%d] = %q, want %q", page, off, got, want)
		}
	}
	assert(1, 0, "A1")  // t1u1 (was on disk)
	assert(1, 8, "C1")  // t3u1 (was on disk)
	assert(1, 16, "C2") // t3u2 (redone)
	assert(2, 0, "B1")  // t2u1 (was on disk)
	assert(2, 8, "E1")  // t5u1 (redone)
	assert(3, 0, "D1")  // t4u1 (redone; P3 never reached disk)
	assert(3, 8, "A2")  // t1u2 (redone)

	// pageLSN bookkeeping: every page reflects its newest log record.
	if got := r.Pages.PageLSN(1); got != s.lsn["t3u2"] {
		t.Fatalf("P1 pageLSN %d, want %d", got, s.lsn["t3u2"])
	}
	if got := r.Pages.PageLSN(2); got != s.lsn["t5u1"] {
		t.Fatalf("P2 pageLSN %d, want %d", got, s.lsn["t5u1"])
	}
	if got := r.Pages.PageLSN(3); got != s.lsn["t1u2"] {
		t.Fatalf("P3 pageLSN %d, want %d", got, s.lsn["t1u2"])
	}
}

func TestFullRecovery(t *testing.T) {
	dir := t.TempDir()
	s := buildCrashScenario(t, dir)

	r, err := Recover(s.walPath, s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	// Reload pages purely from disk: proves committed data is durable and
	// loser changes were physically rolled back.
	ps, err := OpenPageStore(s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	zeros2 := []byte{0, 0}
	assert := func(page uint32, off uint32, want []byte) {
		if got := pageBytes(t, ps, page, off, 2); !bytes.Equal(got, want) {
			t.Fatalf("P%d[%d] = %q, want %q", page, off, got, want)
		}
	}
	// t1, t3, t5 (uncommitted) fully rolled back:
	assert(1, 0, zeros2)  // t1u1 undone
	assert(1, 8, zeros2)  // t3u1 undone
	assert(1, 16, zeros2) // t3u2 undone
	assert(2, 8, zeros2)  // t5u1 undone
	assert(3, 8, zeros2)  // t1u2 undone
	// t2, t4 (committed) durable:
	assert(2, 0, []byte("B1"))
	assert(3, 0, []byte("D1"))

	// The undo pass must have logged exactly one CLR per loser update
	// (t1:2, t3:2, t5:1) and one END per loser transaction.
	img, err := MapWAL(s.walPath)
	if err != nil {
		t.Fatal(err)
	}
	clrByTxn := map[uint64][]*Record{}
	endByTxn := map[uint64]int{}
	img.Scan(func(rec *Record) bool {
		switch rec.Type {
		case TypeCLR:
			clrByTxn[rec.TxnID] = append(clrByTxn[rec.TxnID], rec)
		case TypeEnd:
			endByTxn[rec.TxnID]++
		}
		return true
	})
	if len(clrByTxn[1]) != 2 || len(clrByTxn[3]) != 2 || len(clrByTxn[5]) != 1 {
		t.Fatalf("CLR counts: t1=%d t3=%d t5=%d, want 2/2/1",
			len(clrByTxn[1]), len(clrByTxn[3]), len(clrByTxn[5]))
	}
	if endByTxn[1] != 1 || endByTxn[3] != 1 || endByTxn[5] != 1 {
		t.Fatalf("END counts: %+v, want one per loser", endByTxn)
	}

	// CLR chains: t1's first CLR compensates t1u2 and points back to t1u1;
	// the second compensates t1u1 and terminates the chain (undoNextLSN=0).
	t1 := clrByTxn[1]
	if t1[0].UndoNextLSN != s.lsn["t1u1"] {
		t.Fatalf("CLR(t1u2).undoNext = %d, want %d", t1[0].UndoNextLSN, s.lsn["t1u1"])
	}
	if t1[1].UndoNextLSN != 0 {
		t.Fatalf("CLR(t1u1).undoNext = %d, want 0", t1[1].UndoNextLSN)
	}
	// A CLR's redo image is the original update's before-image.
	if !bytes.Equal(t1[0].NewData, zeros2) {
		t.Fatalf("CLR(t1u2) redo data %q, want zeros", t1[0].NewData)
	}
	// CLR LSNs must be stamped onto the pages they compensated.
	if got := ps.PageLSN(3); got != t1[0].LSN {
		t.Fatalf("P3 pageLSN %d, want CLR LSN %d", got, t1[0].LSN)
	}
}

// TestRecoveryRestartIdempotent: running recovery again on the recovered
// database must be a no-op (no new log records, unchanged data).
func TestRecoveryRestartIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := buildCrashScenario(t, dir)

	r1, err := Recover(s.walPath, s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := r1.Close(); err != nil {
		t.Fatal(err)
	}

	countRecords := func() int {
		img, err := MapWAL(s.walPath)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		img.Scan(func(*Record) bool { n++; return true })
		return n
	}
	before := countRecords()

	r2, err := Recover(s.walPath, s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Second analysis: no losers remain (ENDs were logged for all of them).
	if losers := r2.Losers(); len(losers) != 0 {
		t.Fatalf("second recovery found losers: %+v", losers)
	}
	if err := r2.Close(); err != nil {
		t.Fatal(err)
	}
	if after := countRecords(); after != before {
		t.Fatalf("second recovery appended records: before=%d after=%d", before, after)
	}

	ps, err := OpenPageStore(s.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := pageBytes(t, ps, 2, 0, 2); !bytes.Equal(got, []byte("B1")) {
		t.Fatalf("committed data changed by restart: %q", got)
	}
	if got := pageBytes(t, ps, 1, 0, 2); !bytes.Equal(got, []byte{0, 0}) {
		t.Fatalf("undone data resurrected by restart: %q", got)
	}
}

// TestNoLeaks: recovery spawns no goroutines and releases all resources;
// after Close no dirty (unflushed) buffers remain.
func TestNoLeaks(t *testing.T) {
	settle := func() int {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		return runtime.NumGoroutine()
	}
	before := settle()
	for i := 0; i < 3; i++ {
		dir := t.TempDir()
		s := buildCrashScenario(t, dir)
		r, err := Recover(s.walPath, s.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		if r.Pages.DirtyCount() != 0 {
			t.Fatalf("unflushed dirty pages remain after Close: %d", r.Pages.DirtyCount())
		}
		// Closed resources reject further use.
		if _, err := r.WAL.Append(&Record{Type: TypeCommit, TxnID: 99}); err == nil {
			t.Fatalf("append on closed WAL succeeded")
		}
	}
	if after := settle(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
