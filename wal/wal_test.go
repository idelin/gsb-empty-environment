package wal

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRecordRoundTrip(t *testing.T) {
	recs := []*Record{
		{Type: TypeUpdate, TxnID: 7, PageID: 42, PrevLSN: 3, Before: []byte("before"), After: []byte("after")},
		{Type: TypeCLR, TxnID: 7, PageID: 42, PrevLSN: 9, UndoNextLSN: 3, Before: []byte("x"), After: []byte("y")},
		{Type: TypeCommit, TxnID: 7, PrevLSN: 11},
		{Type: TypeEnd, TxnID: 7, PrevLSN: 12},
		{Type: TypeAbort, TxnID: 8},
		{
			Type: TypeCheckpoint,
			DPT:  map[uint32]LSN{5: 100, 2: 30},
			TT: map[uint32]TxnCheckpointEntry{
				1: {LastLSN: 10, Status: StatusRunning},
				2: {LastLSN: 20, Status: StatusCommitted},
			},
		},
	}
	for i, rec := range recs {
		rec.LSN = LSN(i + 1)
		got, err := DecodeRecord(rec.Encode())
		if err != nil {
			t.Fatalf("decode %v: %v", rec.Type, err)
		}
		if !bytes.Equal(got.Encode(), rec.Encode()) {
			t.Fatalf("round trip mismatch for %v", rec.Type)
		}
		if got.Type != rec.Type || got.TxnID != rec.TxnID || got.PageID != rec.PageID ||
			got.PrevLSN != rec.PrevLSN || got.UndoNextLSN != rec.UndoNextLSN {
			t.Fatalf("field mismatch for %v", rec.Type)
		}
	}
}

func TestCheckpointEncodingDeterministic(t *testing.T) {
	mk := func() *Record {
		return &Record{
			Type: TypeCheckpoint, LSN: 1,
			DPT: map[uint32]LSN{9: 1, 1: 2, 5: 3},
			TT:  map[uint32]TxnCheckpointEntry{3: {9, 0}, 1: {8, 1}},
		}
	}
	if !bytes.Equal(mk().Encode(), mk().Encode()) {
		t.Fatal("checkpoint encoding not deterministic")
	}
}

func TestAppendAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		lsn := w.Append(&Record{Type: TypeUpdate, TxnID: uint32(i), PageID: uint32(i % 7)})
		if lsn != LSN(i+1) {
			t.Fatalf("lsn = %d, want %d", lsn, i+1)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if w2.Len() != 100 {
		t.Fatalf("reopened WAL has %d records, want 100", w2.Len())
	}
	lsn := w2.Append(&Record{Type: TypeCommit, TxnID: 1})
	if lsn != 101 {
		t.Fatalf("append after reopen got LSN %d, want 101", lsn)
	}
}

func TestTornTailTolerated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 1})
	w.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 2})
	w.Close()
	// Simulate a crash mid-write: append garbage bytes.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0, 0, 0, 100, 1, 2}) // declares 100 bytes, provides 2
	f.Close()
	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if w2.Len() != 2 {
		t.Fatalf("torn tail: got %d records, want 2", w2.Len())
	}
	// Log is still appendable after truncation of the torn tail.
	if lsn := w2.Append(&Record{Type: TypeEnd, TxnID: 1}); lsn != 3 {
		t.Fatalf("append after torn tail got LSN %d, want 3", lsn)
	}
}

func TestConcurrentAppends(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "log.wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	const goroutines = 16
	const perG = 200
	seen := make([]bool, goroutines*perG)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				lsn := w.Append(&Record{Type: TypeUpdate, TxnID: uint32(g), PageID: uint32(i)})
				mu.Lock()
				if seen[lsn-1] {
					t.Errorf("duplicate LSN %d", lsn)
				}
				seen[lsn-1] = true
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	if w.Len() != goroutines*perG {
		t.Fatalf("len = %d, want %d", w.Len(), goroutines*perG)
	}
	for i, ok := range seen {
		if !ok {
			t.Fatalf("LSN %d missing", i+1)
		}
	}
}
