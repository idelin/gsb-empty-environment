package recovery

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"aries/wal"
)

// crashFixture builds the acceptance WAL:
//   - T1, T2: committed (with END) — winners
//   - T3, T4, T5: no commit — losers
//   - one checkpoint taken after T1/T2's updates, before their commits
//   - crash: log just stops
//
// It returns the WAL path, the page store as it looked on disk at crash
// time, and the LSNs of every record for fine-grained assertions.
type fixture struct {
	path  string
	store *PageStore
	lsn   map[string]wal.LSN
}

func buildCrashFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")
	w, err := wal.Open(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{path: path, store: NewPageStore(), lsn: map[string]wal.LSN{}}

	appendRec := func(key string, r *wal.Record) {
		f.lsn[key] = w.Append(r)
	}

	// T1 updates P1, P2. T2 updates P3.
	appendRec("T1.upd.P1", &wal.Record{TxnID: 1, Type: wal.TypeUpdate, PageID: 1,
		Before: []byte("init-P1"), After: []byte("T1-P1")})
	appendRec("T1.upd.P2", &wal.Record{TxnID: 1, Type: wal.TypeUpdate, PageID: 2,
		PrevLSN: f.lsn["T1.upd.P1"], Before: []byte("init-P2"), After: []byte("T1-P2")})
	appendRec("T2.upd.P3", &wal.Record{TxnID: 2, Type: wal.TypeUpdate, PageID: 3,
		Before: []byte("init-P3"), After: []byte("T2-P3")})

	// Checkpoint: T1 and T2 in flight, P1..P3 dirty.
	w.Append(&wal.Record{
		Type: wal.TypeCheckpoint,
		ChkTT: wal.TxnTable{
			1: {Status: wal.StatusRunning, LastLSN: f.lsn["T1.upd.P2"]},
			2: {Status: wal.StatusRunning, LastLSN: f.lsn["T2.upd.P3"]},
		},
		ChkDPT: wal.DirtyPageTable{
			1: f.lsn["T1.upd.P1"],
			2: f.lsn["T1.upd.P2"],
			3: f.lsn["T2.upd.P3"],
		},
	})

	// T1 and T2 commit and end.
	appendRec("T1.commit", &wal.Record{TxnID: 1, Type: wal.TypeCommit, PrevLSN: f.lsn["T1.upd.P2"]})
	appendRec("T1.end", &wal.Record{TxnID: 1, Type: wal.TypeEnd, PrevLSN: f.lsn["T1.commit"]})
	appendRec("T2.commit", &wal.Record{TxnID: 2, Type: wal.TypeCommit, PrevLSN: f.lsn["T2.upd.P3"]})
	appendRec("T2.end", &wal.Record{TxnID: 2, Type: wal.TypeEnd, PrevLSN: f.lsn["T2.commit"]})

	// T3, T4, T5: losers, never commit.
	appendRec("T3.upd.P1", &wal.Record{TxnID: 3, Type: wal.TypeUpdate, PageID: 1,
		Before: []byte("T1-P1"), After: []byte("T3-P1")})
	appendRec("T3.upd.P4", &wal.Record{TxnID: 3, Type: wal.TypeUpdate, PageID: 4,
		PrevLSN: f.lsn["T3.upd.P1"], Before: []byte("init-P4"), After: []byte("T3-P4")})
	appendRec("T4.upd.P2", &wal.Record{TxnID: 4, Type: wal.TypeUpdate, PageID: 2,
		Before: []byte("T1-P2"), After: []byte("T4-P2")})
	appendRec("T4.upd.P5", &wal.Record{TxnID: 4, Type: wal.TypeUpdate, PageID: 5,
		PrevLSN: f.lsn["T4.upd.P2"], Before: []byte("init-P5"), After: []byte("T4-P5")})
	appendRec("T5.upd.P3", &wal.Record{TxnID: 5, Type: wal.TypeUpdate, PageID: 3,
		Before: []byte("T2-P3"), After: []byte("T5-P3")})

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Disk state at crash: only P1 (with T1's committed update) was
	// flushed; everything else is still the initial image. This forces
	// Redo to do real work and lets us verify the pageLSN skip.
	for pid := wal.PageID(1); pid <= 5; pid++ {
		f.store.Put(pid, &Page{PageLSN: 0, Data: []byte(fmt.Sprintf("init-P%d", pid))})
	}
	f.store.Put(1, &Page{PageLSN: f.lsn["T1.upd.P1"], Data: []byte("T1-P1")})
	return f
}

func TestAnalysis(t *testing.T) {
	f := buildCrashFixture(t)
	records, err := wal.Scan(f.path)
	if err != nil {
		t.Fatal(err)
	}
	ar := Analyze(records)

	// Transaction table: exactly the three losers survive.
	got := SortedTxnIDs(ar.TT)
	want := []wal.TxnID{3, 4, 5}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("loser txns = %v, want %v", got, want)
	}
	for _, id := range want {
		if ar.TT[id].Status != wal.StatusRunning {
			t.Fatalf("txn %d status = %v, want RUNNING", id, ar.TT[id].Status)
		}
	}
	if ar.TT[3].LastLSN != f.lsn["T3.upd.P4"] {
		t.Fatalf("T3 lastLSN = %d, want %d", ar.TT[3].LastLSN, f.lsn["T3.upd.P4"])
	}
	if ar.TT[5].LastLSN != f.lsn["T5.upd.P3"] {
		t.Fatalf("T5 lastLSN = %d, want %d", ar.TT[5].LastLSN, f.lsn["T5.upd.P3"])
	}

	// Dirty page table: all five pages, recLSNs preserved from the
	// checkpoint for P1..P3 and first-dirty LSN for P4, P5.
	wantDPT := wal.DirtyPageTable{
		1: f.lsn["T1.upd.P1"],
		2: f.lsn["T1.upd.P2"],
		3: f.lsn["T2.upd.P3"],
		4: f.lsn["T3.upd.P4"],
		5: f.lsn["T4.upd.P5"],
	}
	if fmt.Sprint(ar.DPT) != fmt.Sprint(wantDPT) {
		t.Fatalf("DPT = %v, want %v", ar.DPT, wantDPT)
	}
	if ar.RedoFrom != f.lsn["T1.upd.P1"] {
		t.Fatalf("RedoFrom = %d, want %d", ar.RedoFrom, f.lsn["T1.upd.P1"])
	}
}

func TestRecoverThreePhases(t *testing.T) {
	f := buildCrashFixture(t)
	eng := NewEngine(f.path, f.store)
	ar, stats, err := eng.Recover()
	if err != nil {
		t.Fatal(err)
	}

	// Redo repeated history: every post-crash update except the one
	// already flushed to P1 (pageLSN skip).
	if stats.RedoneUpdates != 7 {
		t.Fatalf("RedoneUpdates = %d, want 7", stats.RedoneUpdates)
	}
	// Undo rolled back the 5 loser updates and wrote 5 CLRs + 3 ENDs.
	if stats.UndoneUpdates != 5 || stats.CLRsWritten != 5 {
		t.Fatalf("undo stats = %+v, want 5 undone / 5 CLRs", stats)
	}
	if stats.EndsWritten != 3 {
		t.Fatalf("EndsWritten = %d, want 3", stats.EndsWritten)
	}

	// Final page contents: committed data persists, losers rolled back.
	wantPages := map[wal.PageID]string{
		1: "T1-P1",   // T1 committed; T3's loser update undone
		2: "T1-P2",   // T1 committed; T4's loser update undone
		3: "T2-P3",   // T2 committed; T5's loser update undone
		4: "init-P4", // only touched by loser T3
		5: "init-P5", // only touched by loser T4
	}
	for pid, want := range wantPages {
		p := f.store.Get(pid)
		if p == nil {
			t.Fatalf("page %d missing after recovery", pid)
		}
		if string(p.Data) != want {
			t.Fatalf("page %d = %q, want %q", pid, p.Data, want)
		}
	}

	// The WAL now contains the compensation records.
	final, err := wal.Scan(f.path)
	if err != nil {
		t.Fatal(err)
	}
	var clrs, ends int
	clrByTxn := map[wal.TxnID][]*wal.Record{}
	for _, r := range final {
		switch r.Type {
		case wal.TypeCLR:
			clrs++
			clrByTxn[r.TxnID] = append(clrByTxn[r.TxnID], r)
		case wal.TypeEnd:
			ends++
		}
	}
	if clrs != 5 || ends != 5 { // 2 original ENDs (T1,T2) + 3 new (T3,T4,T5)
		t.Fatalf("final WAL: CLRs=%d ENDs=%d, want 5 and 5", clrs, ends)
	}
	// CLR undo chains: undo runs in reverse, so T3's P4 update is
	// compensated first and its CLR points back at T3's P1 update.
	if got := clrByTxn[3][0].UndoNext; got != f.lsn["T3.upd.P1"] {
		t.Fatalf("T3 first CLR undoNext = %d, want %d", got, f.lsn["T3.upd.P1"])
	}
	if got := clrByTxn[3][1].UndoNext; got != 0 {
		t.Fatalf("T3 last CLR undoNext = %d, want 0 (undo complete)", got)
	}
	// CLR restores the before-image.
	if string(clrByTxn[5][0].After) != "T2-P3" {
		t.Fatalf("T5 CLR after-image = %q, want %q", clrByTxn[5][0].After, "T2-P3")
	}
	_ = ar
}

func TestRecoverIdempotent(t *testing.T) {
	f := buildCrashFixture(t)
	eng := NewEngine(f.path, f.store)
	if _, _, err := eng.Recover(); err != nil {
		t.Fatal(err)
	}
	// A second crash + recovery must find no losers: the END records
	// written by the first undo close out T3/T4/T5.
	_, stats, err := eng.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if stats.UndoneUpdates != 0 || stats.CLRsWritten != 0 || stats.EndsWritten != 0 {
		t.Fatalf("second recovery undid work: %+v", stats)
	}
	wantPages := map[wal.PageID]string{1: "T1-P1", 2: "T1-P2", 3: "T2-P3", 4: "init-P4", 5: "init-P5"}
	for pid, want := range wantPages {
		if got := string(f.store.Get(pid).Data); got != want {
			t.Fatalf("after 2nd recovery page %d = %q, want %q", pid, got, want)
		}
	}
}

func TestRedoRespectsPageLSN(t *testing.T) {
	f := buildCrashFixture(t)
	// Pretend the crash happened after every committed page was flushed
	// with its final committed image and a high pageLSN.
	f.store.Put(1, &Page{PageLSN: f.lsn["T3.upd.P1"], Data: []byte("T3-P1")})
	records, _ := wal.Scan(f.path)
	ar := Analyze(records)
	st := &Stats{}
	eng := NewEngine(f.path, f.store)
	if err := eng.Redo(records, ar, st); err != nil {
		t.Fatal(err)
	}
	// P1's T1 update (LSN below pageLSN) and T3's update (== pageLSN)
	// must both be skipped.
	if got := string(f.store.Get(1).Data); got != "T3-P1" {
		t.Fatalf("P1 = %q, want untouched %q", got, "T3-P1")
	}
}

func TestAbortingTxnUndo(t *testing.T) {
	// A transaction that began aborting before the crash (ABORT record +
	// one CLR already written) must resume undo from the CLR's undoNext.
	dir := t.TempDir()
	path := filepath.Join(dir, "abort.wal")
	w, _ := wal.Open(path, 16)
	l1 := w.Append(&wal.Record{TxnID: 7, Type: wal.TypeUpdate, PageID: 9,
		Before: []byte("v0"), After: []byte("v1")})
	l2 := w.Append(&wal.Record{TxnID: 7, Type: wal.TypeUpdate, PageID: 9, PrevLSN: l1,
		Before: []byte("v1"), After: []byte("v2")})
	w.Append(&wal.Record{TxnID: 7, Type: wal.TypeAbort, PrevLSN: l2})
	// Undo of the second update already happened before the crash.
	w.Append(&wal.Record{TxnID: 7, Type: wal.TypeCLR, PrevLSN: l2, PageID: 9,
		UndoNext: l1, Before: []byte("v2"), After: []byte("v1")})
	w.Close()

	store := NewPageStore()
	store.Put(9, &Page{PageLSN: 0, Data: []byte("v0")})
	eng := NewEngine(path, store)
	_, stats, err := eng.Recover()
	if err != nil {
		t.Fatal(err)
	}
	// Only the first update still needs undoing; the CLR is skipped via
	// its undoNext pointer.
	if stats.UndoneUpdates != 1 {
		t.Fatalf("UndoneUpdates = %d, want 1", stats.UndoneUpdates)
	}
	if got := string(store.Get(9).Data); got != "v0" {
		t.Fatalf("page 9 = %q, want v0", got)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
