package aries

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func mustBegin(t *testing.T, s *System) uint64 {
	t.Helper()
	id, err := s.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return id
}

func mustWrite(t *testing.T, s *System, txnID, pageID uint64, off int, data []byte) {
	t.Helper()
	if err := s.Write(txnID, pageID, off, data); err != nil {
		t.Fatalf("Write(txn=%d,page=%d): %v", txnID, pageID, err)
	}
}

func mustCommit(t *testing.T, s *System, txnID uint64) {
	t.Helper()
	if err := s.Commit(txnID); err != nil {
		t.Fatalf("Commit(%d): %v", txnID, err)
	}
}

func mustRead(t *testing.T, s *System, pageID uint64, off, n int) []byte {
	t.Helper()
	b, err := s.Read(pageID, off, n)
	if err != nil {
		t.Fatalf("Read(page=%d): %v", pageID, err)
	}
	return b
}

func pad(s string, n int) []byte {
	b := make([]byte, n)
	copy(b, s)
	return b
}

// TestCrashDuringBatchUpdate is the acceptance scenario: a batch of
// transfers is in flight when the power fails. Some transactions are
// committed but their dirty pages were never flushed; one transaction is
// incomplete. After restart, recovery must redo the committed work and
// undo the incomplete work, leaving data uncorrupted.
func TestCrashDuringBatchUpdate(t *testing.T) {
	dir := t.TempDir()

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Batch of 10 committed transfers, each touching two pages.
	for i := 0; i < 10; i++ {
		tx := mustBegin(t, s)
		mustWrite(t, s, tx, uint64(i%3), PageHeaderSize, pad(fmt.Sprintf("committed-%02d", i), 16))
		mustWrite(t, s, tx, uint64((i+1)%3), PageHeaderSize+16, pad(fmt.Sprintf("batch-item-%02d", i), 16))
		mustCommit(t, s, tx)
	}
	// One more transaction starts but the power fails mid-flight.
	loser := mustBegin(t, s)
	mustWrite(t, s, loser, 0, PageHeaderSize, pad("CORRUPTED-----", 16))
	mustWrite(t, s, loser, 5, PageHeaderSize, pad("loser-data----", 16))

	// Nothing was ever flushed: no checkpoint, no page writes.
	if got := len(s.DirtyPageTable()); got == 0 {
		t.Fatal("expected dirty pages before crash")
	}
	s.Crash() // simulated power loss: buffer pool and tables vanish

	// Restart and recover.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s2.Recover()
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(stats.Winners) != 0 {
		t.Fatalf("expected no in-doubt winners, got %v", stats.Winners)
	}
	if len(stats.Losers) != 1 || stats.Losers[0] != loser {
		t.Fatalf("expected loser txn %d, got %v", loser, stats.Losers)
	}
	if stats.Redone == 0 {
		t.Fatal("expected redo to re-apply committed updates")
	}
	if stats.Undone != 2 {
		t.Fatalf("expected 2 undone updates, got %d", stats.Undone)
	}

	// Committed data survived even though it was never flushed.
	want := pad("committed-09", 16)
	if got := mustRead(t, s2, 0, PageHeaderSize, 16); !bytes.Equal(got, want) {
		t.Fatalf("page 0: got %q want %q", got, want)
	}
	// The loser's writes are gone.
	if got := mustRead(t, s2, 5, PageHeaderSize, 16); !bytes.Equal(got, make([]byte, 16)) {
		t.Fatalf("loser page not rolled back: %q", got)
	}

	// Verify durability from a completely fresh process view (disk only).
	s2.Crash()
	s3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, s3, 0, PageHeaderSize, 16); !bytes.Equal(got, want) {
		t.Fatalf("page 0 after restart: got %q want %q", got, want)
	}
	if got := mustRead(t, s3, 5, PageHeaderSize, 16); !bytes.Equal(got, make([]byte, 16)) {
		t.Fatalf("loser page after restart: %q", got)
	}
	s3.Crash()
}

// TestWinnerCommittedButNotEnded covers a crash between COMMIT and END:
// analysis must classify the transaction as a winner and recovery must
// append the missing END record.
func TestWinnerCommittedButNotEnded(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, s)
	mustWrite(t, s, tx, 1, PageHeaderSize, pad("winner-data----", 16))
	// Hand-roll a commit without END to simulate crashing between the two.
	te := s.txnTable[tx]
	if _, err := s.lm.Append(&LogRecord{Type: LogCommit, TxnID: tx, PrevLSN: te.LastLSN}); err != nil {
		t.Fatal(err)
	}
	s.Crash()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Winners) != 1 || stats.Winners[0] != tx {
		t.Fatalf("expected winner txn %d, got %v", tx, stats.Winners)
	}
	if got := mustRead(t, s2, 1, PageHeaderSize, 16); !bytes.Equal(got, pad("winner-data----", 16)) {
		t.Fatalf("winner data missing: %q", got)
	}
	// The END record must exist now.
	foundEnd := false
	for _, r := range s2.LogRecords() {
		if r.TxnID == tx && r.Type == LogEnd {
			foundEnd = true
		}
	}
	if !foundEnd {
		t.Fatal("recovery did not write END for committed transaction")
	}
	s2.Crash()
}

// TestCheckpointThenCrash verifies recovery works from a checkpoint:
// pages flushed before the checkpoint stay, committed-but-unflushed pages
// after it are redone, and a loser started after it is undone.
func TestCheckpointThenCrash(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx1 := mustBegin(t, s)
	mustWrite(t, s, tx1, 0, PageHeaderSize, pad("before-ckpt---", 16))
	mustCommit(t, s, tx1)
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if got := len(s.DirtyPageTable()); got != 0 {
		t.Fatalf("checkpoint should clear the DPT, got %d entries", got)
	}

	tx2 := mustBegin(t, s)
	mustWrite(t, s, tx2, 0, PageHeaderSize, pad("after-ckpt----", 16))
	mustCommit(t, s, tx2) // committed but not flushed

	tx3 := mustBegin(t, s)
	mustWrite(t, s, tx3, 1, PageHeaderSize, pad("loser---------", 16))
	s.Crash()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if stats.CheckpointLSN < 0 {
		t.Fatal("analysis did not find the checkpoint")
	}
	if got := mustRead(t, s2, 0, PageHeaderSize, 16); !bytes.Equal(got, pad("after-ckpt----", 16)) {
		t.Fatalf("page 0: got %q", got)
	}
	if got := mustRead(t, s2, 1, PageHeaderSize, 16); !bytes.Equal(got, make([]byte, 16)) {
		t.Fatalf("page 1: loser not undone: %q", got)
	}
	s2.Crash()
}

// TestRecoveryIsIdempotent crashes again right after recovery and checks
// that a second recovery produces the same state (CLR/pageLSN logic).
func TestRecoveryIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx1 := mustBegin(t, s)
	mustWrite(t, s, tx1, 0, PageHeaderSize, pad("stable-value--", 16))
	mustCommit(t, s, tx1)
	tx2 := mustBegin(t, s)
	mustWrite(t, s, tx2, 0, PageHeaderSize, pad("loser--------", 16))
	s.Crash()

	for round := 0; round < 3; round++ {
		sx, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sx.Recover(); err != nil {
			t.Fatalf("Recover round %d: %v", round, err)
		}
		if got := mustRead(t, sx, 0, PageHeaderSize, 16); !bytes.Equal(got, pad("stable-value--", 16)) {
			t.Fatalf("round %d: got %q", round, got)
		}
		sx.Crash()
	}
}

// TestMultipleLosersInterleaved checks that undo follows the global
// reverse-LSN order across several incomplete transactions.
func TestMultipleLosersInterleaved(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := mustBegin(t, s)
	mustWrite(t, s, base, 0, PageHeaderSize, pad("base----------", 16))
	mustWrite(t, s, base, 1, PageHeaderSize, pad("base----------", 16))
	mustCommit(t, s, base)
	if err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	l1 := mustBegin(t, s)
	mustWrite(t, s, l1, 0, PageHeaderSize, pad("loser1-a------", 16))
	l2 := mustBegin(t, s)
	mustWrite(t, s, l2, 1, PageHeaderSize, pad("loser2-a------", 16))
	mustWrite(t, s, l1, 0, PageHeaderSize, pad("loser1-b------", 16))
	mustWrite(t, s, l2, 0, PageHeaderSize, pad("loser2--------", 16))
	s.Crash()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Losers) != 2 {
		t.Fatalf("expected 2 losers, got %v", stats.Losers)
	}
	if stats.Undone != 4 {
		t.Fatalf("expected 4 undone updates, got %d", stats.Undone)
	}
	for page := uint64(0); page <= 1; page++ {
		if got := mustRead(t, s2, page, PageHeaderSize, 16); !bytes.Equal(got, pad("base----------", 16)) {
			t.Fatalf("page %d: got %q", page, got)
		}
	}
	s2.Crash()
}

// TestAbortRollsBack checks normal (non-crash) abort with CLRs.
func TestAbortRollsBack(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx1 := mustBegin(t, s)
	mustWrite(t, s, tx1, 0, PageHeaderSize, pad("original------", 16))
	mustCommit(t, s, tx1)

	tx2 := mustBegin(t, s)
	mustWrite(t, s, tx2, 0, PageHeaderSize, pad("changed-------", 16))
	mustWrite(t, s, tx2, 0, PageHeaderSize, pad("changed-again-", 16))
	if err := s.Abort(tx2); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, s, 0, PageHeaderSize, 16); !bytes.Equal(got, pad("original------", 16)) {
		t.Fatalf("abort failed: %q", got)
	}
	// The abort wrote CLRs.
	clrs := 0
	for _, r := range s.LogRecords() {
		if r.Type == LogCLR && r.TxnID == tx2 {
			clrs++
		}
	}
	if clrs != 2 {
		t.Fatalf("expected 2 CLRs from abort, got %d", clrs)
	}
	s.Crash()
}

// TestTornLogTail simulates a crash in the middle of writing a log
// record: the partial record must be truncated on open and ignored.
func TestTornLogTail(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, s)
	mustWrite(t, s, tx, 0, PageHeaderSize, pad("durable-------", 16))
	mustCommit(t, s, tx)
	s.Crash()

	// Append garbage: half a record frame.
	walPath := filepath.Join(dir, "wal.log")
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0, 0, 0, 200, 1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, s2, 0, PageHeaderSize, 16); !bytes.Equal(got, pad("durable-------", 16)) {
		t.Fatalf("got %q", got)
	}
	// New records after recovery must be readable (file was truncated).
	s2.Crash()
	s3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range s3.LogRecords() {
		if r.Type == 0 || r.TxnID > 1000 {
			t.Fatalf("corrupt record survived: %v", r)
		}
	}
	s3.Crash()
}

// TestWALProtocolLogBeforeData verifies the log record is durable before
// the in-memory page is touched: the DPT entry and pageLSN must equal the
// update's LSN.
func TestWALProtocolLogBeforeData(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := mustBegin(t, s)
	mustWrite(t, s, tx, 7, PageHeaderSize, pad("wal-rule------", 16))
	dpt := s.DirtyPageTable()
	recLSN, ok := dpt[7]
	if !ok {
		t.Fatal("page 7 missing from DPT")
	}
	last := s.LogRecords()[len(s.LogRecords())-1]
	if last.Type != LogUpdate || last.LSN != recLSN {
		t.Fatalf("DPT recLSN %d does not match update LSN %d", recLSN, last.LSN)
	}
	mustCommit(t, s, tx)
	s.Crash()
}
