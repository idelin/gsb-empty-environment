package wal

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func tempDir(t *testing.T) string {
	dir := filepath.Join(os.TempDir(), "waltest-"+t.Name())
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func sampleRecords() []*LogRecord {
	return []*LogRecord{
		{TxnID: 1, Type: TypeUpdate, PrevLSN: LSNInvalid, PageID: 7, Offset: 16,
			Before: []byte("old-value"), After: []byte("new-value")},
		{TxnID: 1, Type: TypeCommit, PrevLSN: 0},
		{TxnID: 2, Type: TypeAbort, PrevLSN: LSNInvalid},
		{TxnID: 2, Type: TypeCLR, PrevLSN: 100, UndoNextLSN: LSNInvalid, PageID: 3,
			Offset: 8, After: []byte("restore-me")},
		{Type: TypeBeginCheckpoint, PrevLSN: LSNInvalid},
		{Type: TypeEndCheckpoint, PrevLSN: 200,
			DPT: map[int32]LSN{1: 10, 2: 20, 9: 90},
			TT: map[int64]TxnInfo{
				1: {Status: StatusRunning, LastLSN: 10},
				2: {Status: StatusCommitted, LastLSN: 20},
				3: {Status: StatusAborting, LastLSN: 30},
			}},
		{TxnID: 1, Type: TypeEnd, PrevLSN: 50},
	}
}

func equalRecord(t *testing.T, want, got *LogRecord) {
	t.Helper()
	if want.LSN != got.LSN || want.TxnID != got.TxnID || want.Type != got.Type ||
		want.PrevLSN != got.PrevLSN || want.PageID != got.PageID ||
		want.UndoNextLSN != got.UndoNextLSN || want.Offset != got.Offset {
		t.Fatalf("header mismatch:\nwant %+v\ngot  %+v", want, got)
	}
	if !bytes.Equal(want.Before, got.Before) || !bytes.Equal(want.After, got.After) {
		t.Fatalf("image mismatch:\nwant %q/%q\ngot  %q/%q", want.Before, want.After, got.Before, got.After)
	}
	if !reflect.DeepEqual(want.DPT, got.DPT) {
		t.Fatalf("DPT mismatch: want %v got %v", want.DPT, got.DPT)
	}
	if !reflect.DeepEqual(want.TT, got.TT) {
		t.Fatalf("TT mismatch: want %v got %v", want.TT, got.TT)
	}
}

func TestAppendIterateRoundtrip(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.wal")

	lm, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	recs := sampleRecords()
	for _, r := range recs {
		if _, err := lm.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := lm.Flush(); err != nil {
		t.Fatal(err)
	}

	// LSNs are assigned in append order and are strictly increasing.
	for i := 1; i < len(recs); i++ {
		if recs[i].LSN <= recs[i-1].LSN {
			t.Fatalf("LSN not increasing: %d then %d", recs[i-1].LSN, recs[i].LSN)
		}
	}
	if lm.FlushedLSN() < recs[len(recs)-1].LSN {
		t.Fatalf("flushedLSN %d behind last record %d", lm.FlushedLSN(), recs[len(recs)-1].LSN)
	}

	it := lm.Iterator()
	for _, want := range recs {
		got, err := it.Next()
		if err != nil {
			t.Fatalf("iterate: %v", err)
		}
		equalRecord(t, want, got)
	}
	if _, err := it.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}

	// Random access by LSN.
	got, err := lm.ReadRecord(recs[3].LSN)
	if err != nil {
		t.Fatal(err)
	}
	equalRecord(t, recs[3], got)
	lm.Close()

	// Reopen: records survive, LSN sequence continues after the last one.
	lm2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if lm2.NextLSN() <= recs[len(recs)-1].LSN {
		t.Fatalf("reopened nextLSN %d not past last record %d", lm2.NextLSN(), recs[len(recs)-1].LSN)
	}
	extra := &LogRecord{TxnID: 9, Type: TypeCommit, PrevLSN: LSNInvalid}
	lsn, err := lm2.Append(extra)
	if err != nil {
		t.Fatal(err)
	}
	if lsn != lm2.NextLSN()-LSN(len(extra.encode())-frameHeaderSize) && lsn <= recs[len(recs)-1].LSN {
		t.Fatalf("append after reopen at %d overlaps old records", lsn)
	}
	lm2.Close()
}

func TestCorruptTailTreatedAsCrashPoint(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.wal")

	lm, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := lm.Append(&LogRecord{TxnID: int64(i + 1), Type: TypeCommit, PrevLSN: LSNInvalid}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lm.Flush(); err != nil {
		t.Fatal(err)
	}
	valid := lm.NextLSN()
	lm.Close()

	// Simulate a torn write: garbage appended after the valid log.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	lm2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lm2.Close()
	if lm2.NextLSN() != valid {
		t.Fatalf("nextLSN = %d, want %d (garbage truncated)", lm2.NextLSN(), valid)
	}
	it := lm2.Iterator()
	count := 0
	for {
		_, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("read %d records, want 3", count)
	}
}

func TestIteratorStopsAtCorruption(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.wal")

	lm, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lm.Close()
	var lsns []LSN
	for i := 0; i < 2; i++ {
		lsn, err := lm.Append(&LogRecord{TxnID: int64(i), Type: TypeUpdate, PageID: 1, After: []byte("x")})
		if err != nil {
			t.Fatal(err)
		}
		lsns = append(lsns, lsn)
	}
	if err := lm.Flush(); err != nil {
		t.Fatal(err)
	}
	// Corrupt one byte inside the second record's payload.
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, int64(lsns[1])+frameHeaderSize); err != nil {
		t.Fatal(err)
	}
	f.Close()

	it := lm.Iterator()
	if _, err := it.Next(); err != nil {
		t.Fatalf("first record should be readable: %v", err)
	}
	if _, err := it.Next(); err != ErrCorrupt {
		t.Fatalf("expected ErrCorrupt, got %v", err)
	}
}
