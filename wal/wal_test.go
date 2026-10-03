package wal

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRecordEncodeDecodeRoundtrip(t *testing.T) {
	recs := []*Record{
		{Type: RecBegin, TxnID: 1},
		{Type: RecUpdate, TxnID: 1, PrevLSN: 1, PageID: 42, Before: -5, After: 99},
		{Type: RecCLR, TxnID: 2, PrevLSN: 7, PageID: 3, UndoNext: 4, Before: 10, After: -1},
		{Type: RecCommit, TxnID: 1, PrevLSN: 2},
		{Type: RecAbort, TxnID: 2, PrevLSN: 8},
		{Type: RecEnd, TxnID: 1, PrevLSN: 3},
		{Type: RecCheckpoint, Txns: []TxnEntry{
			{TxnID: 1, Status: TxnRunning, LastLSN: 11},
			{TxnID: 2, Status: TxnAborting, LastLSN: 12},
		}, Dirty: []DirtyEntry{
			{PageID: 5, RecLSN: 6},
			{PageID: 9, RecLSN: 10},
		}},
	}
	for i, want := range recs {
		got, err := DecodeRecord(want.Encode())
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		if !recordsEqual(got, want) {
			t.Errorf("record %d roundtrip mismatch:\n got %+v\nwant %+v", i, got, want)
		}
	}
}

func recordsEqual(a, b *Record) bool {
	if a.LSN != b.LSN || a.Type != b.Type || a.TxnID != b.TxnID || a.PrevLSN != b.PrevLSN ||
		a.PageID != b.PageID || a.UndoNext != b.UndoNext || a.Before != b.Before || a.After != b.After {
		return false
	}
	if len(a.Txns) != len(b.Txns) || len(a.Dirty) != len(b.Dirty) {
		return false
	}
	for i := range a.Txns {
		if a.Txns[i] != b.Txns[i] {
			return false
		}
	}
	for i := range a.Dirty {
		if a.Dirty[i] != b.Dirty[i] {
			return false
		}
	}
	return true
}

func TestFrameChecksumDetectsCorruption(t *testing.T) {
	frame := EncodeFrame([]byte("hello aries"))
	frames, err := DecodeFrames(frame)
	if err != nil {
		t.Fatal(err)
	}
	if string(frames[0]) != "hello aries" {
		t.Fatalf("payload = %q", frames[0])
	}
	frame[10] ^= 0xFF
	if _, err := DecodeFrames(frame); err == nil {
		t.Fatal("corrupted frame not detected")
	}
}

func TestWALVolatileVsStable(t *testing.T) {
	store := NewMemStore()
	w := New(store, 1)
	w.Append(&Record{Type: RecBegin, TxnID: 1})
	w.Append(&Record{Type: RecUpdate, TxnID: 1, PageID: 1, After: 5})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	w.Append(&Record{Type: RecUpdate, TxnID: 1, PageID: 1, After: 6})
	if got := store.StableLen(); got != 2 {
		t.Fatalf("stable = %d, want 2", got)
	}
	w.LoseVolatile() // crash
	recs, err := w.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("read %d records after crash, want 2", len(recs))
	}
	// Resume continues past the max durable LSN.
	if err := w.Resume(); err != nil {
		t.Fatal(err)
	}
	if got := w.NextLSN(); got != 3 {
		t.Fatalf("next LSN after resume = %d, want 3", got)
	}
}

func TestWALConcurrentAppend(t *testing.T) {
	store := NewMemStore()
	w := New(store, 1)
	const workers = 8
	const perWorker = 500
	var wg sync.WaitGroup
	lsns := make([][]uint64, workers)
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				lsn := w.Append(&Record{Type: RecUpdate, TxnID: uint64(g + 1), PageID: uint64(i), After: int64(i)})
				lsns[g] = append(lsns[g], lsn)
			}
		}(g)
	}
	wg.Wait()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	seen := make(map[uint64]bool, workers*perWorker)
	for g := 0; g < workers; g++ {
		for _, lsn := range lsns[g] {
			if lsn < 1 || lsn > workers*perWorker {
				t.Fatalf("LSN %d out of range", lsn)
			}
			if seen[lsn] {
				t.Fatalf("duplicate LSN %d", lsn)
			}
			seen[lsn] = true
		}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("got %d unique LSNs, want %d", len(seen), workers*perWorker)
	}
	recs, err := w.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != workers*perWorker {
		t.Fatalf("decoded %d records, want %d", len(recs), workers*perWorker)
	}
}

func TestFileStoreDurability(t *testing.T) {
	dir, err := ioutil.TempDir("", "waltest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.wal")

	fs, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	w := New(fs, 1)
	w.Append(&Record{Type: RecBegin, TxnID: 1})
	w.Append(&Record{Type: RecUpdate, TxnID: 1, PageID: 9, After: 123})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	w.Append(&Record{Type: RecUpdate, TxnID: 1, PageID: 9, After: 456}) // volatile
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: only flushed records survive.
	fs2, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fs2.Close()
	w2 := New(fs2, 1)
	recs, err := w2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("read %d records, want 2", len(recs))
	}
	if recs[1].After != 123 {
		t.Fatalf("record 1 after = %d, want 123", recs[1].After)
	}
	if err := w2.Resume(); err != nil {
		t.Fatal(err)
	}
	if got := w2.NextLSN(); got != 3 {
		t.Fatalf("next LSN = %d, want 3", got)
	}
}
