package aries

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "aries-test")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func payload(s string) []byte {
	p := make([]byte, PayloadSize)
	copy(p, s)
	return p
}

func readPayload(t *testing.T, disk *DiskManager, pageID uint64) []byte {
	p, err := disk.ReadPage(pageID)
	if err != nil {
		t.Fatal(err)
	}
	return p.Payload
}

// crashScenario builds the acceptance WAL: 5 transactions (T1..T5),
// T1 commits before the checkpoint, T2 commits after it, and
// T3/T4/T5 are still uncommitted when the system "crashes".
// It returns the WAL/disk, the expected post-recovery payloads and the
// IDs of the three loser transactions.
func crashScenario(t *testing.T, dir string) (*WAL, *DiskManager, map[uint64][]byte, []uint64) {
	walPath := filepath.Join(dir, "wal.log")
	diskPath := filepath.Join(dir, "data.db")

	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := OpenDisk(diskPath)
	if err != nil {
		t.Fatal(err)
	}
	sim := NewSimulator(wal, NewBufferPool(disk))

	zero := payload("")

	t1 := sim.Begin()
	if _, err := sim.Update(t1, 1, payload("committed-by-T1")); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Update(t1, 2, payload("T1-page2")); err != nil {
		t.Fatal(err)
	}
	if err := sim.Commit(t1); err != nil {
		t.Fatal(err)
	}
	// Flush T1's pages before the checkpoint.
	if err := sim.FlushPage(1); err != nil {
		t.Fatal(err)
	}
	if err := sim.FlushPage(2); err != nil {
		t.Fatal(err)
	}

	t3 := sim.Begin()
	if _, err := sim.Update(t3, 4, payload("T3-uncommitted")); err != nil {
		t.Fatal(err)
	}

	// Checkpoint: ATT={T3}, DPT={page4}.
	if _, err := sim.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	t2 := sim.Begin()
	if _, err := sim.Update(t2, 3, payload("committed-by-T2")); err != nil {
		t.Fatal(err)
	}
	if err := sim.Commit(t2); err != nil {
		t.Fatal(err)
	}
	// page3 deliberately NOT flushed: redo must restore it.

	t4 := sim.Begin()
	if _, err := sim.Update(t4, 1, payload("T4-overwrites-T1")); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Update(t4, 5, payload("T4-page5")); err != nil {
		t.Fatal(err)
	}

	t5 := sim.Begin()
	if _, err := sim.Update(t5, 6, payload("T5-page6")); err != nil {
		t.Fatal(err)
	}

	// Steal: flush pages holding uncommitted data (T3's page4, T4's page1).
	if err := sim.FlushPage(4); err != nil {
		t.Fatal(err)
	}
	if err := sim.FlushPage(1); err != nil {
		t.Fatal(err)
	}
	if err := disk.Sync(); err != nil {
		t.Fatal(err)
	}

	// CRASH: the simulator and its buffer pool vanish. Pages 3, 5, 6 were
	// never flushed; pages 1 and 4 hold uncommitted data on disk.
	expected := map[uint64][]byte{
		1: payload("committed-by-T1"), // T4's overwrite undone
		2: payload("T1-page2"),
		3: payload("committed-by-T2"), // redone from WAL
		4: zero,                       // T3 undone
		5: zero,                       // T4 undone
		6: zero,                       // T5 undone
	}
	return wal, disk, expected, []uint64{t3, t4, t5}
}

func TestCrashRecoveryEndToEnd(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	wal, disk, expected, losers := crashScenario(t, dir)

	rm := NewRecoveryManager(wal, disk)
	if err := rm.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}

	for pageID, want := range expected {
		got := readPayload(t, disk, pageID)
		if !bytes.Equal(got, want) {
			t.Errorf("page %d: got %q, want %q", pageID, bytes.TrimRight(got, "\x00"), bytes.TrimRight(want, "\x00"))
		}
	}

	// The WAL must now contain one CLR per undone update and an END per loser.
	records, err := wal.Records()
	if err != nil {
		t.Fatal(err)
	}
	clrs, ends := 0, make(map[uint64]bool)
	for _, r := range records {
		switch r.Type {
		case LogCLR:
			clrs++
		case LogEnd:
			ends[r.TxnID] = true
		}
	}
	if clrs != 4 { // T3:1 + T4:2 + T5:1
		t.Errorf("expected 4 CLRs, got %d", clrs)
	}
	for _, loser := range losers {
		if !ends[loser] {
			t.Errorf("loser txn %d has no END record", loser)
		}
	}

	// The formal inspector must certify the recovered state: no leaks.
	rep := Inspector{}.InspectPostRecovery(records, disk)
	if !rep.OK() {
		t.Fatalf("post-recovery inspection failed:\n%s", rep.Summary())
	}
}

func TestAnalysisTables(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	wal, disk, _, wantLosers := crashScenario(t, dir)

	rm := NewRecoveryManager(wal, disk)
	records, err := wal.Records()
	if err != nil {
		t.Fatal(err)
	}
	rm.records = records
	rm.byLSN = make(map[uint64]*LogRecord)
	for _, r := range records {
		rm.byLSN[r.LSN] = r
	}
	if err := rm.Analysis(); err != nil {
		t.Fatal(err)
	}

	// ATT: the three losers running; T1 and T2 ended and must be gone.
	if len(rm.ATT) != 3 {
		t.Fatalf("ATT size = %d, want 3 (%v)", len(rm.ATT), rm.ATT)
	}
	for _, id := range wantLosers {
		tr, ok := rm.ATT[id]
		if !ok {
			t.Errorf("txn %d missing from ATT", id)
			continue
		}
		if tr.Status != TxnRunning {
			t.Errorf("txn %d status = %s, want RUNNING", id, tr.Status)
		}
		if tr.LastLSN == 0 {
			t.Errorf("txn %d has zero lastLSN", id)
		}
	}

	// DPT: pages 4 (from checkpoint), 3, 1, 5, 6 (after checkpoint).
	wantPages := map[uint64]bool{1: true, 3: true, 4: true, 5: true, 6: true}
	if len(rm.DPT) != len(wantPages) {
		t.Fatalf("DPT = %v, want pages %v", rm.DPT, wantPages)
	}
	for pageID, recLSN := range rm.DPT {
		if !wantPages[pageID] {
			t.Errorf("unexpected page %d in DPT", pageID)
		}
		if recLSN == 0 {
			t.Errorf("page %d has zero recLSN", pageID)
		}
		if rec, ok := rm.byLSN[recLSN]; !ok || rec.PageID != pageID {
			t.Errorf("page %d recLSN %d does not reference a record for that page", pageID, recLSN)
		}
	}

	losers := rm.Losers()
	if len(losers) != len(wantLosers) {
		t.Fatalf("losers = %v, want %v", losers, wantLosers)
	}
	for i := range losers {
		if losers[i] != wantLosers[i] {
			t.Errorf("losers = %v, want %v", losers, wantLosers)
			break
		}
	}
}

func TestRecoveryIsIdempotent(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	wal, disk, expected, _ := crashScenario(t, dir)

	rm := NewRecoveryManager(wal, disk)
	if err := rm.Recover(); err != nil {
		t.Fatal(err)
	}
	// Recover again from the post-recovery WAL: nothing may change.
	rm2 := NewRecoveryManager(wal, disk)
	if err := rm2.Recover(); err != nil {
		t.Fatal(err)
	}
	for pageID, want := range expected {
		if got := readPayload(t, disk, pageID); !bytes.Equal(got, want) {
			t.Errorf("page %d changed after second recovery", pageID)
		}
	}
	records, err := wal.Records()
	if err != nil {
		t.Fatal(err)
	}
	if rep := (Inspector{}).InspectPostRecovery(records, disk); !rep.OK() {
		t.Fatalf("inspection after re-recovery failed:\n%s", rep.Summary())
	}
}

func TestUndoAbortedTransaction(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	wal, _ := OpenWAL(filepath.Join(dir, "wal.log"))
	disk, _ := OpenDisk(filepath.Join(dir, "data.db"))
	sim := NewSimulator(wal, NewBufferPool(disk))

	tx := sim.Begin()
	if _, err := sim.Update(tx, 9, payload("aborted-txn-data")); err != nil {
		t.Fatal(err)
	}
	if err := sim.Abort(tx); err != nil {
		t.Fatal(err)
	}
	if err := sim.FlushPage(9); err != nil {
		t.Fatal(err)
	}
	if err := disk.Sync(); err != nil {
		t.Fatal(err)
	}

	rm := NewRecoveryManager(wal, disk)
	if err := rm.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := readPayload(t, disk, 9); !bytes.Equal(got, payload("")) {
		t.Errorf("aborted txn data survived: %q", bytes.TrimRight(got, "\x00"))
	}
	records, _ := wal.Records()
	if rep := (Inspector{}).InspectPostRecovery(records, disk); !rep.OK() {
		t.Fatalf("inspection failed:\n%s", rep.Summary())
	}
}

func TestRedoRespectsPageLSN(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	wal, _ := OpenWAL(filepath.Join(dir, "wal.log"))
	disk, _ := OpenDisk(filepath.Join(dir, "data.db"))
	sim := NewSimulator(wal, NewBufferPool(disk))

	tx := sim.Begin()
	if _, err := sim.Update(tx, 7, payload("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Update(tx, 7, payload("v2")); err != nil {
		t.Fatal(err)
	}
	if err := sim.Commit(tx); err != nil {
		t.Fatal(err)
	}
	// Flush only after v2: disk already has the newest image.
	if err := sim.FlushPage(7); err != nil {
		t.Fatal(err)
	}
	disk.Sync()

	rm := NewRecoveryManager(wal, disk)
	if err := rm.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := readPayload(t, disk, 7); !bytes.Equal(got, payload("v2")) {
		t.Errorf("page 7 = %q, want v2", bytes.TrimRight(got, "\x00"))
	}
}
