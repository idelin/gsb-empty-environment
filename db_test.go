package aries

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

const testPageSize = 128

func tempDB(t *testing.T) (string, *DB) {
	dir, err := ioutil.TempDir("", "aries-test")
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir, testPageSize)
	if err != nil {
		t.Fatal(err)
	}
	return dir, db
}

func readDataFile(t *testing.T, dir string) []byte {
	raw, err := ioutil.ReadFile(filepath.Join(dir, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func pagePrefix(t *testing.T, db *DB, id PageID, n int) string {
	data, err := db.ReadPage(id)
	if err != nil {
		t.Fatal(err)
	}
	return string(data[:n])
}

// TestCrashRecovery is the acceptance scenario: a power cut in the
// middle of a batch of updates. After restart, committed-but-unflushed
// changes must be redone and uncommitted changes must be undone.
func TestCrashRecovery(t *testing.T) {
	dir, db := tempDB(t)
	defer os.RemoveAll(dir)

	// Seed two pages and flush them with a checkpoint.
	seed := db.Begin()
	seed.Write(0, 0, []byte("AAAA"))
	seed.Write(1, 0, []byte("BBBB"))
	seed.Commit()
	db.Checkpoint()

	// Committed transaction whose dirty pages stay only in memory.
	committed := db.Begin()
	committed.Write(0, 0, []byte("CCCC"))
	committed.Write(1, 0, []byte("DDDD"))
	committed.Commit()

	// In-flight transaction: updates applied, never committed.
	loser := db.Begin()
	loser.Write(0, 0, []byte("EEEE"))
	loser.Write(1, 0, []byte("FFFF"))

	// Before the crash, the data file must hold only the seeded values:
	// the later dirty pages were never flushed.
	raw := readDataFile(t, dir)
	if !bytes.Contains(raw, []byte("AAAA")) || !bytes.Contains(raw, []byte("BBBB")) {
		t.Fatalf("seeded data missing from data file")
	}
	if bytes.Contains(raw, []byte("CCCC")) || bytes.Contains(raw, []byte("EEEE")) {
		t.Fatalf("dirty page leaked to disk before checkpoint")
	}

	// *** sudden power loss ***
	db.Crash()

	// Restart: ARIES analysis + redo + undo runs inside Open.
	db2, err := Open(dir, testPageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	rep := db2.LastRecovery

	// Analysis must have rebuilt the table: the committed txn already
	// wrote its END record before the crash, so only the loser remains.
	if _, ok := rep.TxnTable[committed.ID()]; ok {
		t.Fatalf("analysis: committed txn %d should have been ended before crash", committed.ID())
	}
	if rep.TxnTable[loser.ID()] != StatusRunning {
		t.Fatalf("analysis: loser txn %d has status %v", loser.ID(), rep.TxnTable[loser.ID()])
	}
	if len(rep.DPT) != 2 {
		t.Fatalf("analysis: DPT = %v, want 2 entries", rep.DPT)
	}

	// Redo must have replayed all 4 post-checkpoint updates (history is
	// repeated, including the loser's changes).
	if len(rep.Redone) != 4 {
		t.Fatalf("redo: replayed %d records, want 4", len(rep.Redone))
	}

	// Undo must have rolled back exactly the loser, with one CLR per
	// update it made.
	if len(rep.Losers) != 1 || rep.Losers[0] != loser.ID() {
		t.Fatalf("undo: losers = %v, want [%d]", rep.Losers, loser.ID())
	}
	if len(rep.CLRs) != 2 {
		t.Fatalf("undo: wrote %d CLRs, want 2", len(rep.CLRs))
	}

	// Final state: committed changes present, loser's changes gone.
	if got := pagePrefix(t, db2, 0, 4); got != "CCCC" {
		t.Fatalf("page 0 = %q, want %q (committed value)", got, "CCCC")
	}
	if got := pagePrefix(t, db2, 1, 4); got != "DDDD" {
		t.Fatalf("page 1 = %q, want %q (committed value)", got, "DDDD")
	}

	// After a checkpoint the recovered state must be on disk, with no
	// trace of the loser's values anywhere.
	db2.Checkpoint()
	raw = readDataFile(t, dir)
	if !bytes.Contains(raw, []byte("CCCC")) || !bytes.Contains(raw, []byte("DDDD")) {
		t.Fatalf("recovered committed data missing from data file")
	}
	if bytes.Contains(raw, []byte("EEEE")) || bytes.Contains(raw, []byte("FFFF")) {
		t.Fatalf("uncommitted data found in data file after recovery")
	}
}

// TestAbortRollsBack checks that an explicit abort undoes a
// transaction's updates via CLRs.
func TestAbortRollsBack(t *testing.T) {
	dir, db := tempDB(t)
	defer os.RemoveAll(dir)
	defer db.Close()

	setup := db.Begin()
	setup.Write(0, 0, []byte("AAAA"))
	setup.Commit()

	tx := db.Begin()
	tx.Write(0, 0, []byte("ZZZZ"))
	if got := pagePrefix(t, db, 0, 4); got != "ZZZZ" {
		t.Fatalf("before abort: page 0 = %q, want ZZZZ", got)
	}
	tx.Abort()
	if got := pagePrefix(t, db, 0, 4); got != "AAAA" {
		t.Fatalf("after abort: page 0 = %q, want AAAA", got)
	}

	clrs := 0
	for _, rec := range db.LogRecords() {
		if rec.Type == RecCLR && rec.TxID == tx.ID() {
			clrs++
		}
	}
	if clrs != 1 {
		t.Fatalf("abort wrote %d CLRs, want 1", clrs)
	}
}

// TestCleanShutdownReopen verifies durability across a clean restart.
func TestCleanShutdownReopen(t *testing.T) {
	dir, db := tempDB(t)
	defer os.RemoveAll(dir)

	tx := db.Begin()
	tx.Write(3, 0, []byte("persist-me"))
	tx.Commit()
	db.Close()

	db2, err := Open(dir, testPageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if got := pagePrefix(t, db2, 3, 10); got != "persist-me" {
		t.Fatalf("page 3 = %q, want %q", got, "persist-me")
	}
	if len(db2.LastRecovery.Losers) != 0 {
		t.Fatalf("clean restart should have no losers, got %v", db2.LastRecovery.Losers)
	}
}

// TestRepeatedRecovery crashes again right after recovery (no
// checkpoint) and verifies recovery is idempotent.
func TestRepeatedRecovery(t *testing.T) {
	dir, db := tempDB(t)
	defer os.RemoveAll(dir)

	committed := db.Begin()
	committed.Write(0, 0, []byte("CCCC"))
	committed.Commit()
	loser := db.Begin()
	loser.Write(0, 0, []byte("EEEE"))
	db.Crash()

	db2, err := Open(dir, testPageSize)
	if err != nil {
		t.Fatal(err)
	}
	if got := pagePrefix(t, db2, 0, 4); got != "CCCC" {
		t.Fatalf("first recovery: page 0 = %q, want CCCC", got)
	}
	// Crash again before any checkpoint: CLRs and ENDs are in the WAL.
	db2.Crash()

	db3, err := Open(dir, testPageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer db3.Close()
	if got := pagePrefix(t, db3, 0, 4); got != "CCCC" {
		t.Fatalf("second recovery: page 0 = %q, want CCCC", got)
	}
	if len(db3.LastRecovery.Losers) != 0 {
		t.Fatalf("second recovery: losers = %v, want none", db3.LastRecovery.Losers)
	}
}

// TestManyTransactions exercises a larger batch with interleaved
// commits and one loser, checking every page after recovery.
func TestManyTransactions(t *testing.T) {
	dir, db := tempDB(t)
	defer os.RemoveAll(dir)

	const pages = 16
	// Every committed txn i writes its marker to page i%pages.
	for i := 0; i < 50; i++ {
		tx := db.Begin()
		marker := []byte{byte('a' + i%26), byte('0' + i%10)}
		tx.Write(PageID(i%pages), 0, marker)
		tx.Commit()
	}
	// Loser scribbles over several pages.
	loser := db.Begin()
	for i := 0; i < pages; i++ {
		loser.Write(PageID(i), 0, []byte("XX"))
	}
	db.Crash()

	db2, err := Open(dir, testPageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	// Recompute the expected winner per page: the last committed txn
	// that touched it.
	want := make(map[PageID][]byte)
	for i := 0; i < 50; i++ {
		want[PageID(i%pages)] = []byte{byte('a' + i%26), byte('0' + i%10)}
	}
	for pid, marker := range want {
		if got := pagePrefix(t, db2, pid, 2); got != string(marker) {
			t.Fatalf("page %d = %q, want %q", pid, got, marker)
		}
	}
	if len(db2.LastRecovery.Losers) != 1 || db2.LastRecovery.Losers[0] != loser.ID() {
		t.Fatalf("losers = %v, want [%d]", db2.LastRecovery.Losers, loser.ID())
	}
}
