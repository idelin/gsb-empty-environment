package pagestore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var errBoom = errors.New("injected crash")

func boom() func() error { return func() error { return errBoom } }

func str(b []byte) string { return string(bytes.TrimRight(b, "\x00")) }

func mustOpen(t *testing.T, dir string) *DB {
	t.Helper()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	return db
}

func mustRead(t *testing.T, db *DB, id uint64) string {
	t.Helper()
	data, _, err := db.ReadPage(id)
	if err != nil {
		t.Fatalf("ReadPage(%d): %v", id, err)
	}
	return str(data)
}

func commitPage(t *testing.T, db *DB, id uint64, val string) {
	t.Helper()
	tx := db.Begin()
	if err := tx.Set(id, []byte(val)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestCommitReadAndReopen(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	tx := db.Begin()
	tx.Set(0, []byte("hello"))
	tx.Set(1, []byte("world"))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, db, 0); got != "hello" {
		t.Fatalf("page 0 = %q, want hello", got)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, dir)
	if got := mustRead(t, db, 1); got != "world" {
		t.Fatalf("page 1 = %q, want world", got)
	}
	db.Close()
}

func TestUncommittedNotVisible(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	defer db.Close()
	tx := db.Begin()
	tx.Set(0, []byte("staged"))
	if got := mustRead(t, db, 0); got != "" {
		t.Fatalf("uncommitted write visible: %q", got)
	}
}

// A crash anywhere before the WAL fsync must not expose half a transaction.
func TestCrashBeforeCommitSyncDiscardsTxn(t *testing.T) {
	for _, fp := range []string{FailCommitBeforeAppend, FailCommitBeforeSync} {
		t.Run(fp, func(t *testing.T) {
			dir := t.TempDir()
			db := mustOpen(t, dir)
			commitPage(t, db, 0, "v1")
			if err := db.Checkpoint(); err != nil {
				t.Fatal(err)
			}
			db.SetFailpoint(fp, boom())
			tx := db.Begin()
			tx.Set(0, []byte("v2"))
			tx.Set(1, []byte("v2"))
			if err := tx.Commit(); err != errBoom {
				t.Fatalf("Commit err = %v, want injected crash", err)
			}
			// Simulate process crash: abandon the handle, reopen the dir.
			db = mustOpen(t, dir)
			defer db.Close()
			if got := mustRead(t, db, 0); got != "v1" {
				t.Fatalf("page 0 = %q, want v1 (no partial txn)", got)
			}
			if got := mustRead(t, db, 1); got != "" {
				t.Fatalf("page 1 = %q, want empty (no partial to be seen)", got)
			}
		})
	}
}

// A crash after the commit record is fsynced must be replayed by recovery.
func TestCrashAfterCommitSyncReplays(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	commitPage(t, db, 0, "old")
	db.SetFailpoint(FailCommitAfterSync, boom())
	tx := db.Begin()
	tx.Set(0, []byte("new"))
	tx.Set(1, []byte("new"))
	if err := tx.Commit(); err != errBoom {
		t.Fatalf("Commit err = %v, want injected crash", err)
	}
	db = mustOpen(t, dir)
	defer db.Close()
	if got := mustRead(t, db, 0); got != "new" {
		t.Fatalf("page 0 = %q, want new (replayed from WAL)", got)
	}
	if got := mustRead(t, db, 1); got != "new" {
		t.Fatalf("page 1 = %q, want new (replayed from WAL)", got)
	}
}

// Replaying the same committed WAL twice must yield byte-identical pages.
func TestReplayTwiceIdempotent(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	tx := db.Begin()
	tx.Set(0, []byte("alpha"))
	tx.Set(5, []byte("beta"))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Crash with the WAL still on disk (no checkpoint).
	db2 := mustOpen(t, dir) // recovery pass 1
	defer db2.Close()
	snap1, err := os.ReadFile(filepath.Join(dir, pageFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := db2.recover(); err != nil { // recovery pass 2 on the same WAL
		t.Fatal(err)
	}
	snap2, err := os.ReadFile(filepath.Join(dir, pageFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snap1, snap2) {
		t.Fatal("second replay changed the page file: replay is not idempotent")
	}
	if got := mustRead(t, db2, 0); got != "alpha" {
		t.Fatalf("page 0 = %q, want alpha", got)
	}
	if got := mustRead(t, db2, 5); got != "beta" {
		t.Fatalf("page 5 = %q, want beta", got)
	}
}

// A torn tail (crash while appending) is recognized and truncated; the
// committed prefix is still recovered.
func TestTruncatedTailIgnored(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	commitPage(t, db, 0, "tail-ok")
	walPath := filepath.Join(dir, walFileName)
	before, err := os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	// Crash mid-append: garbage bytes plus half a record at the tail.
	f, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0xDE, 0xAD, 0xBE})
	half := encodeRecord(record{lsn: 99, txn: 99, typ: recPage, pageID: 7, data: make([]byte, PageSize)})
	f.Write(half[:len(half)/2])
	f.Close()

	db = mustOpen(t, dir)
	defer db.Close()
	if got := mustRead(t, db, 0); got != "tail-ok" {
		t.Fatalf("page 0 = %q, want tail-ok", got)
	}
	after, err := os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("torn tail not truncated: WAL size %d, want %d", after.Size(), before.Size())
	}
}

// A corrupt final commit record looks like a torn tail: the incomplete
// transaction is discarded, earlier committed ones survive.
func TestCorruptTailRecordDiscardsLastTxn(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	commitPage(t, db, 0, "A")
	commitPage(t, db, 1, "B")
	walPath := filepath.Join(dir, walFileName)
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xFF // break the last record's CRC
	if err := os.WriteFile(walPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, dir)
	defer db.Close()
	if got := mustRead(t, db, 0); got != "A" {
		t.Fatalf("page 0 = %q, want A", got)
	}
	if got := mustRead(t, db, 1); got != "" {
		t.Fatalf("page 1 = %q, want empty (txn without valid commit discarded)", got)
	}
}

// Corruption in the middle of the log is NOT an ignorable tail: Open must
// fail loudly instead of silently dropping the valid records behind it.
func TestMidLogCorruptionFails(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	commitPage(t, db, 0, "A")
	commitPage(t, db, 1, "B")
	walPath := filepath.Join(dir, walFileName)
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatal(err)
	}
	data[10] ^= 0xFF // corrupt the first record; valid records follow
	if err := os.WriteFile(walPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("Open succeeded despite mid-log corruption")
	} else if !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("error %q does not report corruption", err)
	}
}

// A power loss halfway through a page write leaves a checksum-broken page;
// recovery repairs it from the WAL image.
func TestTornPageRepairedFromWAL(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	commitPage(t, db, 2, "torn-target")
	db.SetFailpoint(FailTornPageWrite, func() error { return nil })
	if err := db.Checkpoint(); err == nil {
		t.Fatal("checkpoint with torn-write failpoint succeeded")
	}
	// Crash: the data file now holds half a page with a bad checksum.
	db = mustOpen(t, dir)
	defer db.Close()
	if got := mustRead(t, db, 2); got != "torn-target" {
		t.Fatalf("page 2 = %q, want torn-target (repaired from WAL)", got)
	}
}

func TestPageChecksumDetectsCorruption(t *testing.T) {
	p := &page{version: 7, data: bytes.Repeat([]byte("x"), PageSize)}
	buf := p.marshal()
	q, err := unmarshalPage(buf)
	if err != nil {
		t.Fatal(err)
	}
	if q.version != 7 || !bytes.Equal(q.data, p.data) {
		t.Fatal("page round-trip mismatch")
	}
	for _, off := range []int{0, 8, 12, pageDiskSize - 1} { // version, crc, data, data
		bad := append([]byte(nil), buf...)
		bad[off] ^= 0xFF
		if _, err := unmarshalPage(bad); !errors.Is(err, errPageChecksum) {
			t.Fatalf("corrupt byte at %d: err = %v, want checksum mismatch", off, err)
		}
	}
}

func TestPageVersionMonotonic(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	defer db.Close()
	commitPage(t, db, 0, "one")
	_, v1, err := db.ReadPage(0)
	if err != nil {
		t.Fatal(err)
	}
	commitPage(t, db, 0, "two")
	_, v2, err := db.ReadPage(0)
	if err != nil {
		t.Fatal(err)
	}
	if v1 == 0 || v2 <= v1 {
		t.Fatalf("versions %d then %d, want strictly increasing", v1, v2)
	}
}

// The LSN counter must survive a checkpoint (which truncates the WAL) plus
// a restart, or post-restart commits would reuse LSNs already reflected in
// the data file and recovery would skip them as "already applied".
func TestLSNSurvivesCheckpointAndRestart(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir)
	commitPage(t, db, 0, "one")
	if err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = mustOpen(t, dir)
	db.SetFailpoint(FailCommitAfterSync, boom())
	tx := db.Begin()
	tx.Set(0, []byte("two"))
	if err := tx.Commit(); err != errBoom {
		t.Fatalf("Commit err = %v, want injected crash", err)
	}
	db = mustOpen(t, dir)
	defer db.Close()
	if got := mustRead(t, db, 0); got != "two" {
		t.Fatalf("page 0 = %q, want two (LSN reused across restart?)", got)
	}
}
