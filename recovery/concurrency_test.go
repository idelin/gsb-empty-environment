package recovery

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"aries/storage"
	"aries/wal"
)

// TestConcurrentTransactions hammers the WAL and transaction manager from
// many goroutines, crashes, recovers, and checks that exactly the
// committed suffix of every page survived. Run with -race.
func TestConcurrentTransactions(t *testing.T) {
	dir, err := os.MkdirTemp("", "ariesconc")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "wal.log")

	w, err := wal.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewPageStore()
	tm := NewTxnManager(w, store)

	before := runtime.NumGoroutine()
	const workers = 8
	const txnsPerWorker = 20
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			pageID := uint32(g + 1)
			for i := 0; i < txnsPerWorker; i++ {
				txn := tm.Begin()
				data := []byte(fmt.Sprintf("w%02d-txn%02d", g, i))
				if err := tm.Update(txn, pageID, 0, data); err != nil {
					t.Errorf("update: %v", err)
					return
				}
				if i%2 == 0 {
					if err := tm.Commit(txn); err != nil {
						t.Errorf("commit: %v", err)
						return
					}
				} else {
					if err := tm.Abort(txn); err != nil {
						t.Errorf("abort: %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	assertNoGoroutineLeak(t, before)

	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	store.Crash()
	w.Close()

	w2, err := wal.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	rm := &RecoveryManager{Wal: w2, Store: store}
	rep, err := rm.Recover()
	if err != nil {
		t.Fatal(err)
	}

	// Each worker committed its even-numbered txns; the last commit per
	// page is txn 18, and the abort of txn 19 must have restored it.
	for g := 0; g < workers; g++ {
		want := []byte(fmt.Sprintf("w%02d-txn%02d", g, 18))
		if got := pageData(store, uint32(g+1), len(want)); !bytes.Equal(got, want) {
			t.Errorf("page %d = %q, want %q", g+1, got, want)
		}
	}
	// Every worker's last (odd) txn aborted: 8 aborts * 1 update = 8 CLRs
	// from Abort, plus nothing left for recovery undo.
	if len(rep.Losers) != 0 {
		t.Errorf("no transaction may be in flight after clean aborts, losers=%v", rep.Losers)
	}

	// LSN allocation under concurrency: dense, unique, gapless.
	recs := w2.Records()
	for i, r := range recs {
		if r.LSN != uint64(i+1) {
			t.Fatalf("LSN gap at index %d: got %d", i, r.LSN)
		}
	}
	// begin+update+commit+end per txn, plus one CLR for every aborted txn
	want := workers*txnsPerWorker*4 + workers*(txnsPerWorker/2)
	if len(recs) != want {
		t.Fatalf("log has %d records, want %d", len(recs), want)
	}
	w2.Close()
	assertNoGoroutineLeak(t, before)
}

// TestConcurrentWALAppend verifies atomic, unique LSN allocation.
func TestConcurrentWALAppend(t *testing.T) {
	dir, err := os.MkdirTemp("", "arieslsn")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	w, err := wal.Open(filepath.Join(dir, "wal.log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	before := runtime.NumGoroutine()
	const goroutines = 16
	const perG = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				w.Append(&wal.Record{Type: wal.TypeUpdate, TxnID: uint64(g), PageID: uint32(g)})
			}
		}(g)
	}
	wg.Wait()

	recs := w.Records()
	if len(recs) != goroutines*perG {
		t.Fatalf("got %d records, want %d", len(recs), goroutines*perG)
	}
	seen := make(map[uint64]bool, len(recs))
	for _, r := range recs {
		if seen[r.LSN] {
			t.Fatalf("duplicate LSN %d", r.LSN)
		}
		seen[r.LSN] = true
	}
	assertNoGoroutineLeak(t, before)
}
