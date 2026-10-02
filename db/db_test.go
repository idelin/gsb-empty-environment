package db

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"aries/storage"
	"aries/wal"
)

func tempDir(t *testing.T) string {
	dir := filepath.Join(os.TempDir(), "dbtest-"+t.Name())
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func open(t *testing.T, dir string) *DB {
	t.Helper()
	database, err := Open(filepath.Join(dir, "data.db"), filepath.Join(dir, "test.wal"), 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func readPage(t *testing.T, database *DB, page int32, off uint32, n int) string {
	t.Helper()
	p, err := database.Pool.FetchPage(page)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Pool.UnpinPage(page, false)
	return string(bytes.TrimRight(p.Data[off:off+uint32(n)], "\x00"))
}

func TestCommitPersistsAndAbortRollsBack(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	database := open(t, dir)

	txn := database.Begin()
	if err := txn.Update(1, storage.DataOffset, []byte("committed")); err != nil {
		t.Fatal(err)
	}
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}

	loser := database.Begin()
	if err := loser.Update(1, storage.DataOffset, []byte("loser-junk")); err != nil {
		t.Fatal(err)
	}
	if got := readPage(t, database, 1, storage.DataOffset, 10); got != "loser-junk" {
		t.Fatalf("pre-abort = %q, want loser-junk", got)
	}
	if err := loser.Abort(); err != nil {
		t.Fatal(err)
	}
	if got := readPage(t, database, 1, storage.DataOffset, 9); got != "committed" {
		t.Fatalf("post-abort = %q, want committed", got)
	}

	// Log contains COMMIT+END for txn 1 and ABORT+CLR+END for txn 2.
	var commits, aborts, clrs, ends int
	it := database.Log.Iterator()
	for {
		rec, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch rec.Type {
		case wal.TypeCommit:
			commits++
		case wal.TypeAbort:
			aborts++
		case wal.TypeCLR:
			clrs++
			if rec.UndoNextLSN != wal.LSNInvalid {
				t.Fatalf("CLR undoNext = %d, want LSNInvalid (single update)", rec.UndoNextLSN)
			}
		case wal.TypeEnd:
			ends++
		}
	}
	if commits != 1 || aborts != 1 || clrs != 1 || ends != 2 {
		t.Fatalf("commits=%d aborts=%d clrs=%d ends=%d, want 1/1/1/2",
			commits, aborts, clrs, ends)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTxnDoneErrors(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	database := open(t, dir)
	txn := database.Begin()
	if err := txn.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := txn.Update(1, storage.DataOffset, []byte("x")); err != ErrTxnDone {
		t.Fatalf("update after commit: err = %v, want ErrTxnDone", err)
	}
	if err := txn.Commit(); err != ErrTxnDone {
		t.Fatalf("double commit: err = %v, want ErrTxnDone", err)
	}
	if err := txn.Abort(); err != ErrTxnDone {
		t.Fatalf("abort after commit: err = %v, want ErrTxnDone", err)
	}
}

func TestCheckpointWritesBothRecords(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	database := open(t, dir)

	active := database.Begin()
	if err := active.Update(2, storage.DataOffset, []byte("dirty")); err != nil {
		t.Fatal(err)
	}
	if err := database.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	var begin, end *wal.LogRecord
	it := database.Log.Iterator()
	for {
		rec, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch rec.Type {
		case wal.TypeBeginCheckpoint:
			begin = rec
		case wal.TypeEndCheckpoint:
			end = rec
		}
	}
	if begin == nil || end == nil {
		t.Fatal("checkpoint records missing")
	}
	if end.PrevLSN != begin.LSN {
		t.Fatalf("end checkpoint prevLSN = %d, want begin LSN %d", end.PrevLSN, begin.LSN)
	}
	// The active transaction was captured in the checkpoint's TT.
	if _, ok := end.TT[active.ID()]; !ok {
		t.Fatalf("checkpoint TT missing active txn %d: %v", active.ID(), end.TT)
	}
	// Page 2 was dirty when the checkpoint snapshot was taken.
	if _, ok := end.DPT[2]; !ok {
		t.Fatalf("checkpoint DPT missing page 2: %v", end.DPT)
	}
	// Checkpoint flushed the page to disk.
	if got := readPage(t, database, 2, storage.DataOffset, 5); got != "dirty" {
		t.Fatalf("page 2 = %q after checkpoint, want dirty", got)
	}
	if dpt := database.Pool.DirtyPages(); len(dpt) != 0 {
		t.Fatalf("pool still dirty after checkpoint: %v", dpt)
	}
}
