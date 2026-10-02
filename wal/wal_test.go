package wal

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tempWAL(t *testing.T) (*WAL, string, func()) {
	dir, err := ioutil.TempDir("", "waltest")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "test.log")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return w, path, func() { w.Close(); os.RemoveAll(dir) }
}

func TestAppendScanRoundTrip(t *testing.T) {
	w, _, cleanup := tempWAL(t)
	defer cleanup()

	var last LSN
	last, _ = w.Append(&Record{Type: TypeBegin, TxnID: 1})
	if last != 0 {
		t.Fatalf("first LSN = %d, want 0", last)
	}
	w.Append(&Record{Type: TypeUpdate, TxnID: 1, PrevLSN: last, PageID: 7, Offset: 3,
		OldData: []byte("old"), NewData: []byte("new!")})
	w.Append(&Record{Type: TypeCommit, TxnID: 1})
	w.Append(&Record{Type: TypeCLR, TxnID: 2, PageID: 9, UndoNext: 123,
		OldData: []byte("x"), NewData: []byte("y")})
	w.Append(&Record{Type: TypeCheckpoint, Payload: []byte{1, 2, 3, 4, 5}})

	recs, err := w.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 {
		t.Fatalf("scanned %d records, want 5", len(recs))
	}
	if recs[1].Type != TypeUpdate || string(recs[1].OldData) != "old" || string(recs[1].NewData) != "new!" {
		t.Fatalf("update record corrupted: %+v", recs[1])
	}
	if recs[3].Type != TypeCLR || recs[3].UndoNext != 123 {
		t.Fatalf("clr record corrupted: %+v", recs[3])
	}
	if string(recs[4].Payload) != string([]byte{1, 2, 3, 4, 5}) {
		t.Fatalf("checkpoint payload corrupted")
	}
	// LSNs must be strictly increasing byte offsets.
	for i := 1; i < len(recs); i++ {
		if recs[i].LSN <= recs[i-1].LSN {
			t.Fatalf("LSNs not increasing at %d", i)
		}
	}
}

func TestScanFromMiddle(t *testing.T) {
	w, _, cleanup := tempWAL(t)
	defer cleanup()
	w.Append(&Record{Type: TypeBegin, TxnID: 1})
	mid, _ := w.Append(&Record{Type: TypeBegin, TxnID: 2})
	w.Append(&Record{Type: TypeCommit, TxnID: 2})

	recs, err := w.Scan(mid)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].TxnID != 2 {
		t.Fatalf("scan from %d returned %d records", mid, len(recs))
	}
}

func TestTornTailIsIgnored(t *testing.T) {
	w, path, cleanup := tempWAL(t)
	defer cleanup()
	w.Append(&Record{Type: TypeBegin, TxnID: 1})
	w.Append(&Record{Type: TypeCommit, TxnID: 1})
	w.Flush()

	// Simulate a crash mid-write: append a partial record.
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	f.Write([]byte{0x30, 0x00, 0x00, 0x00, 0xde, 0xad})
	f.Close()

	recs, err := w.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("torn tail: scanned %d records, want 2", len(recs))
	}
}

func TestCorruptPayloadIsIgnored(t *testing.T) {
	w, path, cleanup := tempWAL(t)
	defer cleanup()
	w.Append(&Record{Type: TypeBegin, TxnID: 1})
	w.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 1, NewData: []byte("data")})
	w.Flush()

	// Flip a byte inside the second record's payload.
	f, _ := os.OpenFile(path, os.O_RDWR, 0644)
	st, _ := f.Stat()
	buf := make([]byte, st.Size())
	f.ReadAt(buf, 0)
	buf[len(buf)-1] ^= 0xFF
	f.WriteAt(buf, 0)
	f.Close()

	recs, err := w.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("corrupt CRC: scanned %d records, want 1", len(recs))
	}
}

func TestConcurrentAppendsUniqueLSNs(t *testing.T) {
	w, _, cleanup := tempWAL(t)
	defer cleanup()

	const workers = 8
	const perWorker = 200
	lsns := make(chan LSN, workers*perWorker)
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				lsn, err := w.Append(&Record{Type: TypeUpdate, TxnID: TxnID(id), PageID: PageID(i)})
				if err != nil {
					t.Error(err)
					return
				}
				lsns <- lsn
			}
		}(g)
	}
	wg.Wait()
	close(lsns)

	seen := make(map[LSN]bool)
	for lsn := range lsns {
		if seen[lsn] {
			t.Fatalf("duplicate LSN %d", lsn)
		}
		seen[lsn] = true
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("got %d LSNs, want %d", len(seen), workers*perWorker)
	}
	recs, err := w.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != workers*perWorker {
		t.Fatalf("scanned %d records, want %d", len(recs), workers*perWorker)
	}
	for i := 1; i < len(recs); i++ {
		if recs[i].LSN <= recs[i-1].LSN {
			t.Fatalf("LSNs not monotonic at %d", i)
		}
	}
}
