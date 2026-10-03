package aries

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWALAppendScanRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	wal, err := CreateWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	var want []*Record
	for i := 0; i < 10; i++ {
		rec := &Record{Type: TypeUpdate, TxnID: uint64(i + 1), PageID: uint32(i), Offset: uint32(i * 8),
			OldData: []byte(fmt.Sprintf("old-%d", i)), NewData: []byte(fmt.Sprintf("new-%d", i))}
		if _, err := wal.Append(rec); err != nil {
			t.Fatal(err)
		}
		want = append(want, rec)
	}
	if _, err := wal.Append(&Record{Type: TypeCommit, TxnID: 1, PrevLSN: want[0].LSN}); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	img, err := MapWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	if img.Version != CurrentVersion {
		t.Fatalf("version %d", img.Version)
	}
	var got []*Record
	img.Scan(func(rec *Record) bool {
		got = append(got, rec)
		return true
	})
	if len(got) != 11 {
		t.Fatalf("scanned %d records, want 11", len(got))
	}
	prev := uint64(0)
	for i, rec := range got {
		if rec.LSN <= prev && i > 0 {
			t.Fatalf("LSNs not increasing at %d: %d after %d", i, rec.LSN, prev)
		}
		prev = rec.LSN
	}
	if got[0].LSN != FileHeaderSize {
		t.Fatalf("first LSN %d, want %d", got[0].LSN, FileHeaderSize)
	}
	// LSNs are exact file offsets: DecodeAt must reproduce each record.
	for _, rec := range got {
		again, err := img.DecodeAt(rec.LSN)
		if err != nil {
			t.Fatal(err)
		}
		if again.Type != rec.Type || again.TxnID != rec.TxnID {
			t.Fatalf("DecodeAt(%d) mismatch", rec.LSN)
		}
	}
}

func TestWALConcurrentAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	wal, err := CreateWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const perWorker = 100
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				rec := &Record{Type: TypeUpdate, TxnID: uint64(g*perWorker + i + 1),
					PageID: uint32(g), Offset: uint32(i % 128), NewData: []byte{byte(g), byte(i)}}
				if _, err := wal.Append(rec); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	img, err := MapWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[uint64]bool)
	count := 0
	img.Scan(func(rec *Record) bool {
		if seen[rec.LSN] {
			t.Errorf("duplicate LSN %d", rec.LSN)
		}
		seen[rec.LSN] = true
		count++
		return true
	})
	if count != workers*perWorker {
		t.Fatalf("scanned %d records, want %d", count, workers*perWorker)
	}
}

// TestOpenWALTruncatesTornTail simulates a crash in the middle of a record
// write: OpenWAL must drop the partial tail and append after the last
// complete record.
func TestOpenWALTruncatesTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	wal, err := CreateWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wal.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 1, NewData: []byte("aa")}); err != nil {
		t.Fatal(err)
	}
	second, err := wal.Append(&Record{Type: TypeCommit, TxnID: 1, PrevLSN: 1})
	if err != nil {
		t.Fatal(err)
	}
	goodEnd := wal.NextLSN()
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	// Crash mid-write: half of a third record lands on disk.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	torn := (&Record{Type: TypeUpdate, TxnID: 2, PageID: 9, NewData: []byte("torn-write-never-finished")}).Encode()
	if _, err := f.Write(torn[:len(torn)/2]); err != nil {
		t.Fatal(err)
	}
	f.Close()

	wal2, err := OpenWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	if wal2.NextLSN() != goodEnd {
		t.Fatalf("NextLSN %d, want torn tail truncated to %d", wal2.NextLSN(), goodEnd)
	}
	if _, err := wal2.Append(&Record{Type: TypeEnd, TxnID: 1, PrevLSN: second}); err != nil {
		t.Fatal(err)
	}
	wal2.Close()

	img, err := MapWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var last *Record
	img.Scan(func(rec *Record) bool {
		count++
		last = rec
		return true
	})
	if count != 3 {
		t.Fatalf("scanned %d records, want 3", count)
	}
	if last.Type != TypeEnd || last.TxnID != 1 {
		t.Fatalf("last record %+v, want END txn 1", last)
	}
}

func TestWALClosedErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	wal, err := CreateWAL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := wal.Append(&Record{Type: TypeCommit, TxnID: 1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("append after close: %v", err)
	}
	if err := wal.Sync(); !errors.Is(err, ErrClosed) {
		t.Fatalf("sync after close: %v", err)
	}
}

func TestWALBadHeader(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad-magic.log")
	if err := os.WriteFile(bad, []byte("NOPE\x00\x02\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MapWAL(bad); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("bad magic: %v", err)
	}

	future := filepath.Join(dir, "future.log")
	if err := os.WriteFile(future, []byte{'A', 'R', 'I', 'S', 0, 99, 0, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MapWAL(future); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("future version: %v", err)
	}

	short := filepath.Join(dir, "short.log")
	if err := os.WriteFile(short, []byte{'A', 'R'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MapWAL(short); !errors.Is(err, ErrShortBuffer) {
		t.Fatalf("short file: %v", err)
	}
}
