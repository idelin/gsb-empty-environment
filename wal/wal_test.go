package wal

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) (*WAL, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	return w, path
}

func TestAppendReadRoundTrip(t *testing.T) {
	w, _ := openTemp(t)
	defer w.Close()

	recs := []*Record{
		{Type: RecUpdate, TxnID: 1, PrevLSN: 0, PageID: 7, Offset: 16,
			Before: []byte("hello"), After: []byte("world")},
		{Type: RecCommit, TxnID: 1, PrevLSN: 0},
		{Type: RecCLR, TxnID: 2, PrevLSN: 5, PageID: 9, Offset: 3, UndoNext: 42,
			Before: []byte("aaa"), After: []byte("bbb")},
		{Type: RecAbort, TxnID: 3, PrevLSN: 11},
		{Type: RecEnd, TxnID: 3, PrevLSN: 12},
		{Type: RecCheckpoint,
			Txns:  []TxnEntry{{TxnID: 1, LastLSN: 100, Status: StatusCommitted}, {TxnID: 2, LastLSN: 200, Status: StatusRunning}},
			Pages: []PageEntry{{PageID: 7, RecLSN: 55}, {PageID: 9, RecLSN: 66}}},
	}

	var lsns []uint64
	for _, rec := range recs {
		lsn, err := w.Append(rec)
		if err != nil {
			t.Fatalf("append %s: %v", rec.Type, err)
		}
		lsns = append(lsns, lsn)
	}
	for i := 1; i < len(lsns); i++ {
		if lsns[i] <= lsns[i-1] {
			t.Fatalf("LSNs not strictly increasing: %v", lsns)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if w.FlushedLSN() != w.End() {
		t.Fatalf("flushedLSN %d != end %d", w.FlushedLSN(), w.End())
	}

	rd := w.ReaderFrom(0)
	for i, want := range recs {
		got, err := rd.Next()
		if err != nil {
			t.Fatalf("read record %d: %v", i, err)
		}
		if got.Type != want.Type || got.TxnID != want.TxnID || got.PrevLSN != want.PrevLSN {
			t.Fatalf("record %d header mismatch: got %+v want %+v", i, got, want)
		}
		if got.LSN != lsns[i] {
			t.Fatalf("record %d LSN mismatch: got %d want %d", i, got.LSN, lsns[i])
		}
		if got.PageID != want.PageID || got.Offset != want.Offset || got.UndoNext != want.UndoNext {
			t.Fatalf("record %d fields mismatch: got %+v want %+v", i, got, want)
		}
		if !bytes.Equal(got.Before, want.Before) || !bytes.Equal(got.After, want.After) {
			t.Fatalf("record %d images mismatch", i)
		}
		if len(got.Txns) != len(want.Txns) || len(got.Pages) != len(want.Pages) {
			t.Fatalf("record %d checkpoint payload mismatch", i)
		}
		for j := range want.Txns {
			if got.Txns[j] != want.Txns[j] {
				t.Fatalf("record %d txn entry %d mismatch", i, j)
			}
		}
		for j := range want.Pages {
			if got.Pages[j] != want.Pages[j] {
				t.Fatalf("record %d page entry %d mismatch", i, j)
			}
		}
	}
	if _, err := rd.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestReadAtAndReaderFromMiddle(t *testing.T) {
	w, _ := openTemp(t)
	defer w.Close()

	var lsns []uint64
	for i := 0; i < 5; i++ {
		lsn, err := w.Append(&Record{Type: RecUpdate, TxnID: uint64(i + 1),
			PageID: uint64(100 + i), After: []byte{byte(i)}})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		lsns = append(lsns, lsn)
	}

	rec, err := w.ReadAt(lsns[3])
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if rec.TxnID != 4 || rec.PageID != 103 {
		t.Fatalf("ReadAt returned wrong record: %+v", rec)
	}

	rd := w.ReaderFrom(lsns[2])
	for i := 2; i < 5; i++ {
		rec, err := rd.Next()
		if err != nil {
			t.Fatalf("Next %d: %v", i, err)
		}
		if rec.TxnID != uint64(i+1) {
			t.Fatalf("record %d: wrong txn %d", i, rec.TxnID)
		}
	}
	if _, err := rd.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestReopenPreservesLog(t *testing.T) {
	w, path := openTemp(t)
	lsn, err := w.Append(&Record{Type: RecCommit, TxnID: 9})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	w2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer w2.Close()
	rec, err := w2.ReadAt(lsn)
	if err != nil {
		t.Fatalf("read after reopen: %v", err)
	}
	if rec.Type != RecCommit || rec.TxnID != 9 {
		t.Fatalf("wrong record after reopen: %+v", rec)
	}
	// Appending after reopen must not overwrite existing content.
	if _, err := w2.Append(&Record{Type: RecEnd, TxnID: 9}); err != nil {
		t.Fatalf("append after reopen: %v", err)
	}
	rec, err = w2.ReadAt(lsn)
	if err != nil || rec.Type != RecCommit {
		t.Fatalf("original record corrupted after reopen+append: %v %+v", err, rec)
	}
}

func TestTornTailTreatedAsEOF(t *testing.T) {
	w, path := openTemp(t)
	if _, err := w.Append(&Record{Type: RecUpdate, TxnID: 1, PageID: 1, After: []byte("data")}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	good := w.End()
	w.Close()

	// Simulate a crash mid-append: garbage bytes after the good prefix.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.Write([]byte{0x00, 0x00, 0x10}); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	f.Close()

	w2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer w2.Close()
	rd := w2.ReaderFrom(0)
	if _, err := rd.Next(); err != nil {
		t.Fatalf("first record should be readable: %v", err)
	}
	// Reader created over the whole file sees the torn tail as EOF.
	if _, err := rd.Next(); err != io.EOF {
		t.Fatalf("torn tail should yield EOF, got %v", err)
	}
	if w2.End() <= good {
		t.Fatalf("expected file to include torn bytes")
	}
}

func TestCorruptRecordRejected(t *testing.T) {
	if _, err := decodeBody(0, []byte{0x7f, 0x00}); err == nil {
		t.Fatalf("expected error for unknown record type")
	}
	if _, err := decodeBody(0, []byte{byte(RecUpdate), 0x01}); err == nil {
		t.Fatalf("expected error for truncated record")
	}
}
