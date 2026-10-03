package wal

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"unsafe"
)

func goroutines() int {
	runtime.GC()
	return runtime.NumGoroutine()
}

func sampleRecords() []*Record {
	return []*Record{
		{Type: RecUpdate, TxnID: 7, PrevLSN: 0, PageID: 42, Offset: 3,
			Before: []byte("old-value"), After: []byte("new-value")},
		{Type: RecCommit, TxnID: 7, PrevLSN: 100},
		{Type: RecAbort, TxnID: 8},
		{Type: RecCLR, TxnID: 8, PrevLSN: 55, PageID: 9, Offset: 1, UndoNext: 12,
			Before: []byte("b"), After: []byte("a")},
		{Type: RecCheckpoint,
			Txns:  []CheckpointTxn{{TxnID: 7, LastLSN: 100, Status: TxnCommitted}, {TxnID: 8, LastLSN: 200, Status: TxnAborting}},
			Pages: []CheckpointPage{{PageID: 42, RecLSN: 5}, {PageID: 9, RecLSN: 6}}},
		{Type: RecEnd, TxnID: 7, PrevLSN: 300},
		{Type: RecUpdate, TxnID: 1, PageID: 1, Offset: 0, Before: nil, After: nil}, // empty images
	}
}

func equalRecord(t *testing.T, want, got *Record) {
	t.Helper()
	wc, gc := want.Clone(), got.Clone()
	wc.LSN, gc.LSN = 0, 0 // LSN assigned by encoder/writer
	if wc.Type != gc.Type || wc.TxnID != gc.TxnID || wc.PrevLSN != gc.PrevLSN ||
		wc.PageID != gc.PageID || wc.Offset != gc.Offset || wc.UndoNext != gc.UndoNext {
		t.Fatalf("header mismatch: want %+v got %+v", wc, gc)
	}
	if !bytes.Equal(wc.Before, gc.Before) || !bytes.Equal(wc.After, gc.After) {
		t.Fatalf("images mismatch: want %q/%q got %q/%q", wc.Before, wc.After, gc.Before, gc.After)
	}
	if len(wc.Txns) != len(gc.Txns) || len(wc.Pages) != len(gc.Pages) {
		t.Fatalf("checkpoint mismatch: want %+v got %+v", wc, gc)
	}
	for i := range wc.Txns {
		if wc.Txns[i] != gc.Txns[i] {
			t.Fatalf("checkpoint txn mismatch: want %+v got %+v", wc.Txns[i], gc.Txns[i])
		}
	}
	for i := range wc.Pages {
		if wc.Pages[i] != gc.Pages[i] {
			t.Fatalf("checkpoint page mismatch: want %+v got %+v", wc.Pages[i], gc.Pages[i])
		}
	}
}

func TestCodecRoundTrip(t *testing.T) {
	var buf []byte
	var lsn uint64
	for _, rec := range sampleRecords() {
		rec.LSN = lsn
		var err error
		buf, err = Encode(buf, rec)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		lsn = uint64(len(buf))
	}
	off := 0
	for _, want := range sampleRecords() {
		got, n, err := Decode(buf[off:])
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		equalRecord(t, want, got)
		if got.LSN != uint64(off) {
			t.Fatalf("LSN: want %d got %d", off, got.LSN)
		}
		off += n
	}
	if off != len(buf) {
		t.Fatalf("trailing bytes: consumed %d of %d", off, len(buf))
	}
}

func TestCodecMaxImage(t *testing.T) {
	img := bytes.Repeat([]byte{0xAB}, maxImage)
	rec := &Record{Type: RecUpdate, TxnID: 1, PageID: 1, Before: img, After: img}
	buf, err := Encode(nil, rec)
	if err != nil {
		t.Fatalf("Encode max image: %v", err)
	}
	got, _, err := Decode(buf)
	if err != nil {
		t.Fatalf("Decode max image: %v", err)
	}
	if !bytes.Equal(got.After, img) {
		t.Fatal("max-size image corrupted")
	}
	tooBig := &Record{Type: RecUpdate, TxnID: 1, PageID: 1, After: make([]byte, maxImage+1)}
	if _, err := Encode(nil, tooBig); err != ErrImageTooBig {
		t.Fatalf("oversize image: want ErrImageTooBig got %v", err)
	}
}

// TestDecodeNeverPanics truncates and bit-flips a valid record in every
// possible way; Decode must always return an error, never panic.
func TestDecodeNeverPanics(t *testing.T) {
	rec := &Record{Type: RecCLR, TxnID: 3, PageID: 2, Offset: 9, UndoNext: 4,
		Before: []byte("before"), After: []byte("after")}
	buf, err := Encode(nil, rec)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(buf); i++ {
		if _, _, err := Decode(buf[:i]); err == nil {
			t.Fatalf("truncated to %d bytes: want error", i)
		}
	}
	for i := range buf {
		for bit := uint(0); bit < 8; bit++ {
			mut := append([]byte(nil), buf...)
			mut[i] ^= 1 << bit
			Decode(mut) // must not panic; error or (rarely) valid
		}
	}
}

func TestDecodeCorruptions(t *testing.T) {
	rec := &Record{Type: RecUpdate, TxnID: 1, PageID: 1, After: []byte("x")}
	good, err := Encode(nil, rec)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(fn func(b []byte)) []byte {
		b := append([]byte(nil), good...)
		fn(b)
		return b
	}
	fixCRC := func(b []byte) {
		binary.LittleEndian.PutUint32(b[36:40], crc32.ChecksumIEEE(b[0:36]))
	}

	cases := []struct {
		name string
		buf  []byte
		want error
	}{
		{"empty", nil, ErrShortHeader},
		{"short header", good[:HeaderSize-1], ErrShortHeader},
		{"bad magic", mutate(func(b []byte) { b[0] ^= 0xFF; fixCRC(b) }), ErrBadMagic},
		{"version zero", mutate(func(b []byte) { b[4] = 0; fixCRC(b) }), ErrBadVersion},
		{"huge payload", mutate(func(b []byte) {
			binary.LittleEndian.PutUint32(b[32:36], 0xFFFFFFFF)
			fixCRC(b)
		}), ErrCorrupt},
		{"short payload", good[:len(good)-1], ErrShortPayload},
		{"bad crc", mutate(func(b []byte) { b[36] ^= 0xFF }), ErrBadCRC},
		{"bad type", mutate(func(b []byte) { b[5] = 0xEE; fixCRC(b) }), ErrBadType},
		{"image overrun", mutate(func(b []byte) {
			b[HeaderSize+10] = 0xFF // beforeLen = 65280, payload far smaller
			b[HeaderSize+11] = 0xFF
			fixCRC(b)
		}), ErrCorrupt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := Decode(tc.buf); err != tc.want {
				t.Fatalf("want %v got %v", tc.want, err)
			}
		})
	}
}

// TestForwardCompatibility: a record written by a *newer* format version with
// extra trailing payload bytes must still decode, extras ignored.
func TestForwardCompatibility(t *testing.T) {
	rec := &Record{Type: RecUpdate, TxnID: 5, PageID: 6, Offset: 1, After: []byte("data")}
	buf, err := Encode(nil, rec)
	if err != nil {
		t.Fatal(err)
	}
	extra := []byte("future-extension-bytes")
	payloadLen := uint32(len(buf) - HeaderSize + len(extra))
	fut := make([]byte, HeaderSize, HeaderSize+int(payloadLen))
	copy(fut, buf[:HeaderSize])
	fut[4] = FormatVersion + 7 // newer version
	binary.LittleEndian.PutUint32(fut[32:36], payloadLen)
	fut = append(fut, buf[HeaderSize:]...)
	fut = append(fut, extra...)
	binary.LittleEndian.PutUint32(fut[36:40], crc32.ChecksumIEEE(fut[0:36]))

	got, n, err := Decode(fut)
	if err != nil {
		t.Fatalf("future record rejected: %v", err)
	}
	if n != len(fut) {
		t.Fatalf("consumed %d of %d", n, len(fut))
	}
	if got.TxnID != 5 || got.PageID != 6 || string(got.After) != "data" {
		t.Fatalf("decoded %+v", got)
	}
}

func writeSampleLog(t *testing.T, n int) (string, []*Record) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wal.log")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	var recs []*Record
	for i := 0; i < n; i++ {
		rec := &Record{Type: RecUpdate, TxnID: uint64(i%3 + 1), PageID: uint64(i%5 + 1),
			Offset: uint16(i % 8), Before: []byte{byte(i)}, After: []byte{byte(i + 1)}}
		if _, err := w.Append(rec); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, rec.Clone())
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path, recs
}

func TestReaderRoundTripAndZeroCopy(t *testing.T) {
	path, wants := writeSampleLog(t, 64)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	recs, err := r.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != len(wants) {
		t.Fatalf("want %d records got %d", len(wants), len(recs))
	}
	base := uintptr(0)
	if len(r.Buf()) > 0 {
		base = uintptr(ptrOf(&r.Buf()[0]))
	}
	end := base + uintptr(len(r.Buf()))
	for i, got := range recs {
		equalRecord(t, wants[i], got)
		// Zero-copy: decoded images must point inside the mapped buffer.
		for _, img := range [][]byte{got.Before, got.After} {
			if len(img) == 0 {
				continue
			}
			p := uintptr(ptrOf(&img[0]))
			if p < base || p >= end {
				t.Fatalf("record %d image copied out of mmap region", i)
			}
		}
	}
}

func ptrOf(b *byte) unsafe.Pointer { return unsafe.Pointer(b) }

func TestScanValidToleratesTornTail(t *testing.T) {
	path, wants := writeSampleLog(t, 10)
	// Simulate a crash mid-write: append garbage.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xDE, 0xAD, 0xBE}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	recs, err := r.ScanValid()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != len(wants) {
		t.Fatalf("torn tail: want %d valid records got %d", len(wants), len(recs))
	}
	if _, err := r.Records(); err == nil {
		t.Fatal("strict Records() must reject torn tail")
	}
}

func TestConcurrentAccess(t *testing.T) {
	path, _ := writeSampleLog(t, 256)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := r.ScanValid(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	// Concurrent appends to a separate writer.
	w, err := NewWriter(filepath.Join(t.TempDir(), "w.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := w.Append(&Record{Type: RecUpdate, TxnID: uint64(id), PageID: 1, After: []byte{byte(i)}}); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if got := w.NextLSN(); got == 0 {
		t.Fatal("no records appended")
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	before := goroutines()
	path, _ := writeSampleLog(t, 8)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Records(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if after := goroutines(); after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
