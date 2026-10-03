package aries

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// buildCrashWAL generates the acceptance WAL: 5 transactions (T1, T2
// committed; T3, T4, T5 in flight), two fuzzy checkpoints, then a crash
// after the second checkpoint. It returns the WAL path and the disk as it
// stood at the moment of the crash (only flushed pages survive).
func buildCrashWAL(t *testing.T) (string, *Disk) {
	t.Helper()
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")

	disk := NewDisk()
	wal := NewWAL(walPath)
	db := NewDatabase(wal, disk)

	db.Update(1, 10, []byte("a0"), []byte("a1")) // T1 (will commit)
	db.Update(2, 20, []byte("b0"), []byte("b1")) // T2 (will commit)
	db.Update(3, 30, []byte("c0"), []byte("c1")) // T3 (loser)
	db.Commit(1)
	db.Update(4, 40, []byte("d0"), []byte("d1")) // T4 (loser)

	db.BP.FlushPage(10) // T1's committed page reaches disk
	db.Checkpoint()     // ckpt1: TT={2,3,4}, DPT={20,30,40}

	db.Update(2, 20, []byte("b1"), []byte("b2"))
	db.Commit(2)
	db.Update(5, 50, []byte("e0"), []byte("e1")) // T5 (loser)
	db.Update(3, 10, []byte("a1"), []byte("a2")) // loser overwrites committed page

	db.Checkpoint() // ckpt2: TT={3,4,5}, DPT={10,20,30,40,50}

	db.BP.FlushPage(20)                          // T2's committed page reaches disk
	db.Update(4, 40, []byte("d1"), []byte("d2")) // loser updates after ckpt2
	db.Update(5, 30, []byte("c1"), []byte("c3")) // loser overwrites loser's page

	if err := wal.Flush(); err != nil { // WAL is durable; buffer pool is lost
		t.Fatalf("flush wal: %v", err)
	}
	// CRASH: db and its buffer pool are discarded here.
	return walPath, disk
}

func TestThreePhaseRecovery(t *testing.T) {
	walPath, disk := buildCrashWAL(t)

	wal, err := LoadWAL(walPath)
	if err != nil {
		t.Fatalf("load wal: %v", err)
	}
	stats := Recover(wal, disk)

	// --- Analysis ---
	if len(stats.AnalysisTT) != 3 {
		t.Fatalf("analysis TT size = %d, want 3 (%v)", len(stats.AnalysisTT), stats.AnalysisTT)
	}
	for _, tx := range []TxID{3, 4, 5} {
		e, ok := stats.AnalysisTT[tx]
		if !ok {
			t.Fatalf("analysis TT missing loser tx %d", tx)
		}
		if e.Status != StatusRunning {
			t.Fatalf("tx %d status = %v, want RUNNING", tx, e.Status)
		}
	}
	wantDPTPages := []PageID{10, 20, 30, 40, 50}
	gotDPTPages := make([]PageID, 0, len(stats.AnalysisDPT))
	for p := range stats.AnalysisDPT {
		gotDPTPages = append(gotDPTPages, p)
	}
	sort.Slice(gotDPTPages, func(i, j int) bool { return gotDPTPages[i] < gotDPTPages[j] })
	if !reflect.DeepEqual(gotDPTPages, wantDPTPages) {
		t.Fatalf("analysis DPT pages = %v, want %v", gotDPTPages, wantDPTPages)
	}

	// --- Undo losers ---
	if !reflect.DeepEqual(stats.Losers, []TxID{3, 4, 5}) {
		t.Fatalf("losers = %v, want [3 4 5]", stats.Losers)
	}
	// Each loser wrote 2 updates; undo must emit exactly one CLR per update.
	if len(stats.CLRs) != 6 {
		t.Fatalf("undo wrote %d CLRs, want 6", len(stats.CLRs))
	}

	// --- Final durable state ---
	want := map[PageID]string{
		10: "a1", // T1 committed; T3's uncommitted a2 rolled back
		20: "b2", // T2 committed
		30: "c0", // T3 and T5 both rolled back
		40: "d0", // T4 rolled back
		50: "e0", // T5 rolled back
	}
	for page, wantData := range want {
		got := disk.ReadPage(page)
		if string(got.Data) != wantData {
			t.Errorf("page %d = %q, want %q", page, got.Data, wantData)
		}
		if got.Dirty {
			t.Errorf("page %d still dirty after recovery", page)
		}
	}

	// --- Redo repeated history: every redone record advanced pageLSN ---
	if len(stats.Redone) == 0 {
		t.Fatalf("redo applied nothing; expected history to be repeated")
	}
	for page, wantData := range want {
		p := disk.ReadPage(page)
		if p.PageLSN <= 0 {
			t.Errorf("page %d (%q) has no pageLSN after recovery", page, wantData)
		}
	}
}

func TestRecoveryIsIdempotent(t *testing.T) {
	walPath, disk := buildCrashWAL(t)

	wal, err := LoadWAL(walPath)
	if err != nil {
		t.Fatalf("load wal: %v", err)
	}
	Recover(wal, disk)

	// Crash again immediately after the first recovery and recover once
	// more: no losers remain, no new CLRs, state is unchanged.
	wal2, err := LoadWAL(walPath)
	if err != nil {
		t.Fatalf("reload wal: %v", err)
	}
	stats := Recover(wal2, disk)
	if len(stats.Losers) != 0 {
		t.Fatalf("second recovery found losers %v, want none", stats.Losers)
	}
	if len(stats.CLRs) != 0 {
		t.Fatalf("second recovery wrote %d CLRs, want 0", len(stats.CLRs))
	}
	want := map[PageID]string{10: "a1", 20: "b2", 30: "c0", 40: "d0", 50: "e0"}
	for page, wantData := range want {
		if got := string(disk.ReadPage(page).Data); got != wantData {
			t.Errorf("page %d = %q, want %q after idempotent recovery", page, got, wantData)
		}
	}
}

func TestRecoverWithoutCheckpoint(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")
	disk := NewDisk()
	wal := NewWAL(walPath)
	db := NewDatabase(wal, disk)

	db.Update(1, 1, []byte("x0"), []byte("x1"))
	db.Commit(1)
	db.Update(2, 2, []byte("y0"), []byte("y1")) // never commits
	if err := wal.Flush(); err != nil {
		t.Fatalf("flush wal: %v", err)
	}

	wal2, err := LoadWAL(walPath)
	if err != nil {
		t.Fatalf("load wal: %v", err)
	}
	stats := Recover(wal2, disk)
	if !reflect.DeepEqual(stats.Losers, []TxID{2}) {
		t.Fatalf("losers = %v, want [2]", stats.Losers)
	}
	if got := string(disk.ReadPage(1).Data); got != "x1" {
		t.Errorf("page 1 = %q, want x1", got)
	}
	if got := string(disk.ReadPage(2).Data); got != "y0" {
		t.Errorf("page 2 = %q, want y0", got)
	}
}

func TestRedoSkipsPersistentPages(t *testing.T) {
	dir := t.TempDir()
	walPath := filepath.Join(dir, "wal.log")
	disk := NewDisk()
	wal := NewWAL(walPath)
	db := NewDatabase(wal, disk)

	db.Update(1, 7, []byte("v0"), []byte("v1"))
	db.Commit(1)
	db.BP.FlushAll() // everything on disk; DPT empty at checkpoint
	db.Checkpoint()
	if err := wal.Flush(); err != nil {
		t.Fatalf("flush wal: %v", err)
	}

	wal2, err := LoadWAL(walPath)
	if err != nil {
		t.Fatalf("load wal: %v", err)
	}
	stats := Recover(wal2, disk)
	if len(stats.Redone) != 0 {
		t.Fatalf("redo re-applied %d records to clean pages, want 0", len(stats.Redone))
	}
	if got := string(disk.ReadPage(7).Data); got != "v1" {
		t.Errorf("page 7 = %q, want v1", got)
	}
}
