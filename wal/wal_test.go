package wal

import (
	"os"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	path := t.TempDir() + "/wal.log"
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	b := NewBuilder(w)
	b.Update(1, 10, []byte("a"), []byte("b"))
	b.Commit(1)
	b.Checkpoint()
	b.Update(2, 11, []byte("x"), []byte("y"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	recs, err := ScanAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 4 {
		t.Fatalf("records = %d, want 4", len(recs))
	}
	if recs[0].LSN != 1 || recs[0].Type != TypeUpdate || string(recs[0].After) != "b" {
		t.Fatalf("record 0 wrong: %+v", recs[0])
	}
	if recs[1].PrevLSN != 1 {
		t.Fatalf("commit prevLSN = %d, want 1", recs[1].PrevLSN)
	}
	if recs[2].Type != TypeCheckpoint || len(recs[2].Txns) != 1 {
		t.Fatalf("checkpoint payload wrong: %+v", recs[2])
	}
}

func TestAppenderContinuesLSN(t *testing.T) {
	path := t.TempDir() + "/wal.log"
	w, _ := NewWriter(path)
	b := NewBuilder(w)
	b.Update(1, 1, nil, []byte("v"))
	w.Close()

	aw, err := OpenAppender(path)
	if err != nil {
		t.Fatal(err)
	}
	lsn, err := aw.Append(&LogRecord{Type: TypeEnd, TxnID: 1})
	if err != nil {
		t.Fatal(err)
	}
	aw.Close()
	if lsn != 2 {
		t.Fatalf("appended LSN = %d, want 2", lsn)
	}
}

func TestCorruptTailDetected(t *testing.T) {
	path := t.TempDir() + "/wal.log"
	w, _ := NewWriter(path)
	b := NewBuilder(w)
	b.Update(1, 1, nil, []byte("v"))
	w.Close()

	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	f.Write([]byte{1, 2, 3}) // torn frame
	f.Close()

	recs, err := ScanAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("ScanAll returned %d records, want 1 (torn tail tolerated)", len(recs))
	}

	r, _ := NewReader(path)
	defer r.Close()
	if _, err := r.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(); err != ErrCorrupt {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
}

func TestLastLSN(t *testing.T) {
	path := t.TempDir() + "/wal.log"
	w, _ := NewWriter(path)
	w.Close()
	if lsn, _ := LastLSN(path); lsn != 0 {
		t.Fatalf("empty log LastLSN = %d, want 0", lsn)
	}
}
