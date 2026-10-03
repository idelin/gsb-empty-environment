package wal

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func tempPath(t *testing.T) (string, func()) {
	dir, err := ioutil.TempDir("", "wal-test")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "wal.log"), func() { os.RemoveAll(dir) }
}

func TestAppendAndReadAll(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cp := &CheckpointData{
		Txns:       map[uint64]TxnSnapshot{1: {LastLSN: 1, Status: TxnCommitted}},
		DirtyPages: map[uint64]LSN{7: 1},
	}
	if _, err := w.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 7, Before: []byte("a"), After: []byte("b")}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(&Record{Type: TypeCommit, TxnID: 1, PrevLSN: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(&Record{Type: TypeCheckpoint, Checkpoint: cp}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	recs, err := ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
	if recs[0].LSN != 1 || recs[1].LSN != 2 || recs[2].LSN != 3 {
		t.Fatalf("LSNs should be sequential: %d %d %d", recs[0].LSN, recs[1].LSN, recs[2].LSN)
	}
	if string(recs[0].Before) != "a" || string(recs[0].After) != "b" {
		t.Fatalf("update payload corrupted: %q -> %q", recs[0].Before, recs[0].After)
	}
	got := recs[2].Checkpoint
	if got == nil || got.Txns[1].Status != TxnCommitted || got.DirtyPages[7] != 1 {
		t.Fatalf("checkpoint payload corrupted: %+v", got)
	}
}

func TestOpenContinuesLSNSequence(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lsn, err := w2.Append(&Record{Type: TypeEnd, TxnID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if lsn != 2 {
		t.Fatalf("reopened WAL should continue at LSN 2, got %d", lsn)
	}
	w2.Close()
}

func TestReadAllMissingFile(t *testing.T) {
	recs, err := ReadAll(filepath.Join(os.TempDir(), "wal-test-no-such-file.log"))
	if err != nil || recs != nil {
		t.Fatalf("missing file should yield empty log, got %v, %v", recs, err)
	}
}

func TestReadAllIgnoresTruncatedTail(t *testing.T) {
	path, cleanup := tempPath(t)
	defer cleanup()

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 1, After: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash mid-write by appending a partial frame.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0, 0, 0, 50, 1, 2})
	f.Close()

	recs, err := ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("truncated tail should be ignored, got %d records", len(recs))
	}
}
