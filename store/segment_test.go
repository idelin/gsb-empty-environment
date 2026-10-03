package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func writeSegment(t *testing.T, path string, entries []Entry) {
	t.Helper()
	w, err := CreateWriter(path, CurrentFormat)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func readAll(t *testing.T, path string) []Entry {
	t.Helper()
	seg, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seg.Close()
	var out []Entry
	if err := seg.ForEach(func(e Entry) error {
		// Copy out of the mapping: zero-copy entries are only valid
		// until Close.
		e.Command = append([]byte(nil), e.Command...)
		out = append(out, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSegmentRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	want := sampleEntries()
	writeSegment(t, path, want)
	got := readAll(t, path)
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Slot != want[i].Slot || got[i].Term != want[i].Term ||
			got[i].NodeID != want[i].NodeID || got[i].Flags != want[i].Flags ||
			!bytes.Equal(got[i].Command, want[i].Command) {
			t.Fatalf("entry %d mismatch:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestSegmentZeroCopyAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	writeSegment(t, path, []Entry{{Slot: 1, Term: 1, NodeID: 1, Command: []byte("mmap-cmd")}})
	seg, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seg.Close()
	e, _, err := seg.Next(HeaderSize)
	if err != nil {
		t.Fatal(err)
	}
	base := seg.Bytes()
	// The decoded command must point inside the mapped region.
	lo := uintptr(unsafe.Pointer(&base[0]))
	hi := uintptr(unsafe.Pointer(&base[len(base)-1]))
	p := uintptr(unsafe.Pointer(&e.Command[0]))
	if p < lo || p > hi {
		t.Fatal("decoded command does not alias the mapped segment")
	}
}

func TestSegmentCRCMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	writeSegment(t, path, []Entry{{Slot: 1, Term: 1, NodeID: 1, Command: []byte("data")}})
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xff}, HeaderSize+recordHeaderSize); err != nil {
		t.Fatal(err)
	}
	f.Close()
	seg, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seg.Close()
	if _, _, err := seg.Next(HeaderSize); !errors.Is(err, ErrChecksum) {
		t.Fatalf("want ErrChecksum, got %v", err)
	}
}

func TestSegmentTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	entries := []Entry{
		{Slot: 1, Term: 1, NodeID: 1, Command: []byte("one")},
		{Slot: 2, Term: 1, NodeID: 1, Command: []byte("two")},
	}
	writeSegment(t, path, entries)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Truncate in the middle of the second record.
	if err := os.Truncate(path, fi.Size()-3); err != nil {
		t.Fatal(err)
	}
	seg, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seg.Close()
	var got []Entry
	if err := seg.ForEach(func(e Entry) error { // torn tail tolerated
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatalf("ForEach must tolerate torn tail: %v", err)
	}
	if len(got) != 1 || string(got[0].Command) != "one" {
		t.Fatalf("got %+v", got)
	}
	// Next must still report the truncation precisely.
	if _, _, err := seg.Next(HeaderSize + recordHeaderSize + len(Encode(entries[0], CurrentFormat))); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestSegmentRecordTooLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	hdr := make([]byte, HeaderSize)
	copy(hdr, magic[:])
	binary.LittleEndian.PutUint16(hdr[8:10], CurrentFormat)
	rec := make([]byte, recordHeaderSize)
	binary.LittleEndian.PutUint32(rec[4:8], MaxRecordSize+1)
	binary.LittleEndian.PutUint32(rec[0:4], crc32.ChecksumIEEE(rec[4:]))
	if err := os.WriteFile(path, append(hdr, rec...), 0o644); err != nil {
		t.Fatal(err)
	}
	seg, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seg.Close()
	if _, _, err := seg.Next(HeaderSize); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestSegmentBadHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	if err := os.WriteFile(path, []byte("not-a-segment-file!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if err := os.WriteFile(path, []byte("tiny"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestSegmentCleanEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	writeSegment(t, path, []Entry{{Slot: 1, Command: []byte("x")}})
	seg, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seg.Close()
	_, off, err := seg.Next(HeaderSize)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := seg.Next(off); !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF, got %v", err)
	}
}

func TestSegmentConcurrentReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	var entries []Entry
	for i := 0; i < 256; i++ {
		entries = append(entries, Entry{Slot: uint64(i), Term: 1, NodeID: 1, Command: bytes.Repeat([]byte{byte(i)}, 32)})
	}
	writeSegment(t, path, entries)
	seg, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seg.Close()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 20; r++ {
				var n int
				if err := seg.ForEach(func(e Entry) error {
					if e.Slot != uint64(n) {
						t.Errorf("slot %d, want %d", e.Slot, n)
					}
					n++
					return nil
				}); err != nil {
					t.Error(err)
					return
				}
				if n != len(entries) {
					t.Errorf("read %d entries, want %d", n, len(entries))
				}
			}
		}()
	}
	wg.Wait()
}

func countOpenFDs() int {
	for _, dir := range []string{"/dev/fd", "/proc/self/fd"} {
		if ents, err := os.ReadDir(dir); err == nil {
			return len(ents)
		}
	}
	return -1
}

func TestNoResourceLeaks(t *testing.T) {
	goroutines := runtime.NumGoroutine()
	fds := countOpenFDs()
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		path := filepath.Join(dir, "log.seg")
		w, err := CreateWriter(path, CurrentFormat)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Append(Entry{Slot: 1, Command: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		seg, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := seg.ForEach(func(Entry) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if err := seg.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if fds >= 0 {
		if got := countOpenFDs(); got != fds {
			t.Fatalf("fd leak: before=%d after=%d", fds, got)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= goroutines {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: before=%d after=%d", goroutines, runtime.NumGoroutine())
}

func TestOpenWriterAppendAndTornTailRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	writeSegment(t, path, []Entry{
		{Slot: 1, Term: 1, NodeID: 1, Command: []byte("one")},
		{Slot: 2, Term: 1, NodeID: 1, Command: []byte("two")},
	})
	// Simulate a crash mid-write: append half a record.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0xde, 0xad, 0xbe}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Reopen: torn tail is truncated, appends resume at the clean boundary.
	w, err := OpenWriter(path, CurrentFormat)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Entry{Slot: 3, Term: 2, NodeID: 2, Command: []byte("three")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := readAll(t, path)
	if len(got) != 3 || string(got[2].Command) != "three" {
		t.Fatalf("got %v entries", len(got))
	}
}

func TestSegmentV1FileCompat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.seg")
	w, err := CreateWriter(path, FormatV1)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Entry{Slot: 7, Term: 3, NodeID: 2, Command: []byte("v1-cmd")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	seg, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer seg.Close()
	if seg.Format != FormatV1 {
		t.Fatalf("format=%d, want %d", seg.Format, FormatV1)
	}
	e, _, err := seg.Next(HeaderSize)
	if err != nil {
		t.Fatal(err)
	}
	if e.Slot != 7 || e.Flags != 0 || string(e.Command) != "v1-cmd" {
		t.Fatalf("v1 entry mismatch: %+v", e)
	}
}
