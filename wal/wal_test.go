package wal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func tempWALPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "waltest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "wal.log")
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	recs := []*Record{
		{Type: TypeBegin, TxnID: 1},
		{Type: TypeUpdate, TxnID: 1, PrevLSN: 1, PageID: 7, Offset: 12,
			OldData: []byte("old-value"), NewData: []byte("new-value")},
		{Type: TypeCommit, TxnID: 1, PrevLSN: 2},
		{Type: TypeAbort, TxnID: 2, PrevLSN: 5},
		{Type: TypeCLR, TxnID: 2, PrevLSN: 6, PageID: 3, Offset: 1,
			OldData: []byte("a"), NewData: []byte("b"), UndoNextLSN: 4},
		{Type: TypeEnd, TxnID: 1, PrevLSN: 3},
		{Type: TypeCheckpoint,
			DirtyPages: map[uint32]uint64{1: 10, 22: 33},
			Txns:       map[uint64]uint64{5: 44, 6: 55}},
	}
	for i, want := range recs {
		dec, n, err := DecodeFrame(want.Encode())
		if err != nil {
			t.Fatalf("record %d: decode: %v", i, err)
		}
		if n != len(want.Encode()) {
			t.Fatalf("record %d: consumed %d, want %d", i, n, len(want.Encode()))
		}
		if dec.Type != want.Type || dec.TxnID != want.TxnID ||
			dec.PageID != want.PageID || dec.Offset != want.Offset ||
			dec.UndoNextLSN != want.UndoNextLSN ||
			!bytes.Equal(dec.OldData, want.OldData) ||
			!bytes.Equal(dec.NewData, want.NewData) {
			t.Fatalf("record %d: round trip mismatch: %+v vs %+v", i, dec, want)
		}
		for k, v := range want.DirtyPages {
			if dec.DirtyPages[k] != v {
				t.Fatalf("record %d: DPT[%d]=%d, want %d", i, k, dec.DirtyPages[k], v)
			}
		}
		for k, v := range want.Txns {
			if dec.Txns[k] != v {
				t.Fatalf("record %d: Txns[%d]=%d, want %d", i, k, dec.Txns[k], v)
			}
		}
	}
}

func TestDecodeRejectsCorruption(t *testing.T) {
	good := (&Record{Type: TypeUpdate, TxnID: 1, PageID: 1, NewData: []byte("xyz")}).Encode()
	cases := map[string][]byte{
		"empty":         {},
		"short-len":     {1, 0},
		"truncated":     good[:len(good)/2],
		"bad-crc":       append(append([]byte(nil), good[:len(good)-1]...), good[len(good)-1]^0xFF),
		"garbage-body":  bytes.Repeat([]byte{0xAB}, 64),
		"len-too-small": {1, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	for name, buf := range cases {
		if _, _, err := DecodeFrame(buf); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestScanStopsAtCorruptTail(t *testing.T) {
	path := tempWALPath(t)
	w, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		w.Append(&Record{Type: TypeBegin, TxnID: uint64(i + 1)})
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	w.Close()

	// Simulate a crash leaving garbage after the durable prefix.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x01})
	f.Close()

	recs, good, err := ScanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 5 {
		t.Fatalf("scanned %d records, want 5", len(recs))
	}
	fi, _ := os.Stat(path)
	if good >= fi.Size() {
		t.Fatalf("goodBytes=%d should be < file size %d", good, fi.Size())
	}
}

func TestOpenTruncatesTornTailAndSelfHeals(t *testing.T) {
	path := tempWALPath(t)
	inj := NewInjector()
	w, err := Open(path, inj)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(&Record{Type: TypeBegin, TxnID: 1})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	// Crash mid-write: only half of the record lands on disk.
	inj.Arm(FaultTornWrite)
	w.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 9, NewData: []byte("payload")})
	if err := w.Flush(); err != ErrTornWrite {
		t.Fatalf("want ErrTornWrite, got %v", err)
	}
	if inj.TornWrites != 1 {
		t.Fatalf("TornWrites=%d, want 1", inj.TornWrites)
	}
	w.Close() // crash: pending remainder is lost

	// Reopen: the torn tail must be truncated, the log stays usable.
	w2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	recs := w2.Records()
	if len(recs) != 1 || recs[0].Type != TypeBegin {
		t.Fatalf("after heal: %v records, want only BEGIN", recs)
	}
	w2.Append(&Record{Type: TypeCommit, TxnID: 1, PrevLSN: 1})
	if err := w2.Flush(); err != nil {
		t.Fatalf("flush after heal: %v", err)
	}
	w2.Close()

	recs, _, err = ScanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[1].Type != TypeCommit {
		t.Fatalf("final log has %d records, want 2 (BEGIN, COMMIT)", len(recs))
	}
}

func TestDiskFullFlushRetrySelfHeals(t *testing.T) {
	path := tempWALPath(t)
	inj := NewInjector()
	w, err := Open(path, inj)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(&Record{Type: TypeBegin, TxnID: 1})
	w.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 1, NewData: []byte("d")})

	inj.Arm(FaultDiskFull)
	if err := w.Flush(); err != ErrDiskFull {
		t.Fatalf("want ErrDiskFull, got %v", err)
	}
	if inj.FailedWrites == 0 {
		t.Fatal("expected failed write statistics")
	}

	// Operator frees disk space: the very same buffered records flush fine.
	inj.Clear()
	if err := w.Flush(); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	w.Close()
	recs, _, err := ScanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2 (nothing lost, nothing duplicated)", len(recs))
	}
}

func TestPowerLossDropsUnflushedTail(t *testing.T) {
	path := tempWALPath(t)
	inj := NewInjector()
	w, err := Open(path, inj)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(&Record{Type: TypeBegin, TxnID: 1})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	// Power fails: subsequent "successful" flushes never reach the disk.
	inj.Arm(FaultPowerLoss)
	w.Append(&Record{Type: TypeUpdate, TxnID: 1, PageID: 1, NewData: []byte("lost")})
	if err := w.Flush(); err != nil {
		t.Fatalf("power-loss flush should look successful, got %v", err)
	}
	if inj.DroppedWrites == 0 {
		t.Fatal("expected dropped write statistics")
	}
	w.Close()

	recs, _, err := ScanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Type != TypeBegin {
		t.Fatalf("only the durable BEGIN may survive, got %v", recs)
	}
}

func TestAppendAndFlushRollbackOnDiskFull(t *testing.T) {
	path := tempWALPath(t)
	inj := NewInjector()
	w, err := Open(path, inj)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(&Record{Type: TypeBegin, TxnID: 1})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}

	inj.Arm(FaultDiskFull)
	commit := &Record{Type: TypeCommit, TxnID: 1, PrevLSN: 1}
	if err := w.AppendAndFlush(commit); err != ErrDiskFull {
		t.Fatalf("want ErrDiskFull, got %v", err)
	}
	inj.Clear()

	// The failed commit must not exist anywhere: not durable, not pending.
	if got := len(w.Records()); got != 1 {
		t.Fatalf("failed commit leaked into log buffer: %d records", got)
	}
	w.Append(&Record{Type: TypeAbort, TxnID: 1, PrevLSN: 1})
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	recs, _, _ := ScanFile(path)
	if len(recs) != 2 || recs[1].Type != TypeAbort {
		t.Fatalf("want BEGIN+ABORT, got %v", recs)
	}
}
