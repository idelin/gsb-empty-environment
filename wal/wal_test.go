package wal

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRecordRoundtrip(t *testing.T) {
	recs := []*Record{
		{LSN: 1, TxnID: 7, Type: TypeUpdate, PageID: 42,
			Before: []byte("before"), After: []byte("after")},
		{LSN: 2, TxnID: 7, Type: TypeCommit, PrevLSN: 1},
		{LSN: 3, TxnID: 9, Type: TypeCLR, PrevLSN: 5, PageID: 3, UndoNext: 4,
			Before: []byte("x"), After: []byte("y")},
		{LSN: 4, Type: TypeCheckpoint,
			ChkTT:  TxnTable{1: {Status: StatusRunning, LastLSN: 10}, 2: {Status: StatusCommitted, LastLSN: 20}},
			ChkDPT: DirtyPageTable{5: 100, 6: 200}},
		{LSN: 5, TxnID: 1, Type: TypeEnd, PrevLSN: 4},
	}
	var buf bytes.Buffer
	for _, r := range recs {
		buf.Write(r.Marshal())
	}
	got, err := ScanReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(recs) {
		t.Fatalf("scanned %d records, want %d", len(got), len(recs))
	}
	for i, want := range recs {
		g := got[i]
		if g.LSN != want.LSN || g.TxnID != want.TxnID || g.Type != want.Type ||
			g.PrevLSN != want.PrevLSN || g.PageID != want.PageID || g.UndoNext != want.UndoNext {
			t.Fatalf("record %d header mismatch: %+v vs %+v", i, g, want)
		}
		if !bytes.Equal(g.Before, want.Before) || !bytes.Equal(g.After, want.After) {
			t.Fatalf("record %d payload mismatch", i)
		}
	}
	// Checkpoint payload survived.
	chk := got[3]
	if chk.ChkTT[2].Status != StatusCommitted || chk.ChkTT[1].LastLSN != 10 {
		t.Fatalf("checkpoint TT corrupted: %+v", chk.ChkTT)
	}
	if chk.ChkDPT[5] != 100 || chk.ChkDPT[6] != 200 {
		t.Fatalf("checkpoint DPT corrupted: %+v", chk.ChkDPT)
	}
}

func TestScanToleratesTornTail(t *testing.T) {
	r1 := &Record{LSN: 1, TxnID: 1, Type: TypeUpdate, PageID: 1, After: []byte("a")}
	r2 := &Record{LSN: 2, TxnID: 1, Type: TypeUpdate, PageID: 2, After: []byte("b")}
	data := append(r1.Marshal(), r2.Marshal()...)
	// Simulate a crash mid-write: chop the second record in half.
	torn := data[:len(r1.Marshal())+7]
	got, err := ScanReader(bytes.NewReader(torn))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].LSN != 1 {
		t.Fatalf("torn scan got %d records, want 1", len(got))
	}
}

func TestConcurrentAppendUniqueLSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conc.wal")
	w, err := Open(path, 4096)
	if err != nil {
		t.Fatal(err)
	}
	const writers = 16
	const perWriter = 500
	lsns := make([][]LSN, writers)
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				lsn := w.Append(&Record{TxnID: TxnID(g), Type: TypeUpdate,
					PageID: PageID(i), After: []byte{byte(g), byte(i)}})
				lsns[g] = append(lsns[g], lsn)
			}
		}(g)
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// Every LSN in [1, writers*perWriter] assigned exactly once.
	seen := map[LSN]bool{}
	for _, l := range lsns {
		for _, lsn := range l {
			if seen[lsn] {
				t.Fatalf("LSN %d assigned twice", lsn)
			}
			seen[lsn] = true
		}
	}
	if len(seen) != writers*perWriter {
		t.Fatalf("got %d unique LSNs, want %d", len(seen), writers*perWriter)
	}
	recs, err := Scan(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != writers*perWriter {
		t.Fatalf("scanned %d records, want %d", len(recs), writers*perWriter)
	}
}

func TestOpenAppendContinuesLSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "append.wal")
	w, _ := Open(path, 16)
	w.Append(&Record{TxnID: 1, Type: TypeUpdate, PageID: 1, After: []byte("a")})
	w.Close()

	w2, err := OpenAppend(path, 16, 2)
	if err != nil {
		t.Fatal(err)
	}
	if lsn := w2.Append(&Record{TxnID: 1, Type: TypeCommit, PrevLSN: 1}); lsn != 2 {
		t.Fatalf("appended LSN = %d, want 2", lsn)
	}
	w2.Close()

	recs, err := Scan(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[1].Type != TypeCommit {
		t.Fatalf("got %d records, want 2 with COMMIT tail", len(recs))
	}
	st, _ := os.Stat(path)
	if st.Size() == 0 {
		t.Fatal("WAL file empty after append")
	}
}

func BenchmarkAppend(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.wal")
	w, _ := Open(path, 1<<16)
	defer w.Close()
	rec := &Record{TxnID: 1, Type: TypeUpdate, PageID: 1,
		Before: []byte("0123456789abcdef"), After: []byte("fedcba9876543210")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Append(rec)
	}
}
