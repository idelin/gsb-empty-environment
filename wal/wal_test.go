package wal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []*Record{
		{LSN: 1, Type: TypeBegin, TxnID: 7},
		{LSN: 2, Type: TypeUpdate, TxnID: 7, PrevLSN: 1, PageID: 42, Before: []byte("old"), After: []byte("new")},
		{LSN: 3, Type: TypeCommit, TxnID: 7, PrevLSN: 2},
		{LSN: 4, Type: TypeAbort, TxnID: 8, PrevLSN: 9},
		{LSN: 5, Type: TypeEnd, TxnID: 7, PrevLSN: 3},
		{LSN: 6, Type: TypeCLR, TxnID: 8, PrevLSN: 4, PageID: 3, UndoNext: 1, Before: []byte("a"), After: []byte("b")},
		{LSN: 7, Type: TypeCheckpoint, ATT: map[int]uint64{1: 6, 2: 5}, DPT: map[int]uint64{10: 2, 20: 4}},
		{LSN: 8, Type: TypeUpdate, TxnID: 1, PageID: 1, Before: nil, After: nil},
	}
	for _, want := range cases {
		frame := encodeFrame(want)
		got, err := decodeFrame(frame[0:8], frame[8:])
		if err != nil {
			t.Fatalf("decode %s: %v", want.Type, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
		}
	}
}

func TestDecodeRejectsTruncation(t *testing.T) {
	rec := &Record{LSN: 1, Type: TypeUpdate, TxnID: 1, PageID: 1, After: []byte("payload")}
	frame := encodeFrame(rec)
	for _, cut := range []int{9, 12, len(frame) - 1} {
		if _, err := decodeFrame(frame[0:8], frame[8:cut]); err == nil {
			t.Fatalf("expected error decoding %d-byte body", cut-8)
		}
	}
}

func TestAppendAndScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	m, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for i := 0; i < 10; i++ {
		m.Append(&Record{Type: TypeUpdate, TxnID: i, PageID: i, After: []byte{byte(i)}})
	}
	var lsns []uint64
	err = m.ScanFrom(5, func(rec *Record) error {
		lsns = append(lsns, rec.LSN)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lsns) != 6 || lsns[0] != 5 || lsns[5] != 10 {
		t.Fatalf("ScanFrom(5) returned %v", lsns)
	}
	rec, err := m.Read(3)
	if err != nil {
		t.Fatal(err)
	}
	if rec.TxnID != 2 || !bytes.Equal(rec.After, []byte{2}) {
		t.Fatalf("Read(3) = %+v", rec)
	}
	if _, err := m.Read(999); err == nil {
		t.Fatal("expected error for missing LSN")
	}
}

func TestReopenRebuildsIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	m, _ := OpenLog(path)
	m.Append(&Record{Type: TypeBegin, TxnID: 1})
	m.Append(&Record{Type: TypeCommit, TxnID: 1, PrevLSN: 1})
	m.Flush(2)
	m.Close()

	m2, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if m2.LastLSN() != 2 {
		t.Fatalf("LastLSN = %d, want 2", m2.LastLSN())
	}
	rec, err := m2.Read(2)
	if err != nil || rec.Type != TypeCommit {
		t.Fatalf("Read(2) = %+v, %v", rec, err)
	}
}

func TestCorruptionDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	m, _ := OpenLog(path)
	for i := 0; i < 3; i++ {
		m.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: i, After: []byte("data")})
	}
	m.Flush(3)
	m.Close()

	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, 20); err != nil { // flip a byte inside record 1
		t.Fatal(err)
	}
	f.Close()

	if _, err := OpenLog(path); err == nil {
		t.Fatal("expected corruption error on reopen")
	}
}

func TestConcurrentAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	m, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	const workers = 8
	const perWorker = 125
	var wg sync.WaitGroup
	seen := make([][]uint64, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				lsn := m.Append(&Record{Type: TypeUpdate, TxnID: w, PageID: i, After: []byte{byte(w)}})
				seen[w] = append(seen[w], lsn)
			}
		}(w)
	}
	wg.Wait()
	uniq := make(map[uint64]bool)
	for _, list := range seen {
		for _, lsn := range list {
			if uniq[lsn] {
				t.Fatalf("duplicate LSN %d", lsn)
			}
			uniq[lsn] = true
		}
	}
	if len(uniq) != workers*perWorker {
		t.Fatalf("got %d unique LSNs, want %d", len(uniq), workers*perWorker)
	}
	count := 0
	m.ScanFrom(0, func(*Record) error { count++; return nil })
	if count != workers*perWorker {
		t.Fatalf("scanned %d records, want %d", count, workers*perWorker)
	}
}

func TestFlushMonotonic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	m, _ := OpenLog(path)
	defer m.Close()
	l1 := m.Append(&Record{Type: TypeBegin, TxnID: 1})
	m.Flush(l1)
	if got := m.FlushedLSN(); got != l1 {
		t.Fatalf("FlushedLSN = %d, want %d", got, l1)
	}
	l2 := m.Append(&Record{Type: TypeBegin, TxnID: 2})
	m.Flush(l1) // stale flush must not regress
	if got := m.FlushedLSN(); got != l1 {
		t.Fatalf("FlushedLSN regressed to %d", got)
	}
	m.Flush(l2)
	if got := m.FlushedLSN(); got != l2 {
		t.Fatalf("FlushedLSN = %d, want %d", got, l2)
	}
}

func TestMasterRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master")
	if lsn, err := ReadMaster(path); err != nil || lsn != 0 {
		t.Fatalf("missing master: lsn=%d err=%v", lsn, err)
	}
	if err := WriteMaster(path, 42); err != nil {
		t.Fatal(err)
	}
	if lsn, err := ReadMaster(path); err != nil || lsn != 42 {
		t.Fatalf("lsn=%d err=%v", lsn, err)
	}
	if err := os.WriteFile(path, []byte{1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMaster(path); err == nil {
		t.Fatal("expected error for corrupt master")
	}
}

func TestScanStopsOnCallbackError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.log")
	m, _ := OpenLog(path)
	defer m.Close()
	for i := 0; i < 5; i++ {
		m.Append(&Record{Type: TypeBegin, TxnID: i})
	}
	stop := fmt.Errorf("stop")
	visited := 0
	err := m.ScanFrom(0, func(*Record) error {
		visited++
		return stop
	})
	if err != stop || visited != 1 {
		t.Fatalf("err=%v visited=%d", err, visited)
	}
}
