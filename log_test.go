package applog

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var errInjected = errors.New("injected fault")

func openTestLog(t *testing.T, dir string, segMax uint64, hooks Hooks) *Log {
	t.Helper()
	opts := &Options{SegmentMaxRecords: segMax, Hooks: hooks}
	l, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}

func reopenTestLog(t *testing.T, dir string, segMax uint64, hooks Hooks) *Log {
	t.Helper()
	return openTestLog(t, dir, segMax, hooks)
}

func TestAppendReadRoundTrip(t *testing.T) {
	l := openTestLog(t, t.TempDir(), 4, Hooks{})
	for i := 0; i < 10; i++ {
		off, err := l.Append([]byte(fmt.Sprintf("record-%d", i)))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		if off != uint64(i) {
			t.Fatalf("offset = %d, want %d", off, i)
		}
	}
	if got := l.NextOffset(); got != 10 {
		t.Fatalf("nextOffset = %d, want 10", got)
	}
	for i := 0; i < 10; i++ {
		p, err := l.Read(uint64(i))
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if want := fmt.Sprintf("record-%d", i); string(p) != want {
			t.Fatalf("read %d = %q, want %q", i, p, want)
		}
	}
}

func TestReadErrors(t *testing.T) {
	l := openTestLog(t, t.TempDir(), 4, Hooks{})
	if _, err := l.Append([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Read(5); err != ErrOffsetOutOfRange {
		t.Fatalf("read future offset: %v, want ErrOffsetOutOfRange", err)
	}
	if err := l.RegisterConsumer("c", 0); err != nil {
		t.Fatal(err)
	}
	if n, err := l.Reclaim(); err != nil || n != 0 {
		t.Fatalf("reclaim = (%d,%v), want (0,nil)", n, err)
	}
}

func TestReopenAcrossRotation(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 4, Hooks{})
	for i := 0; i < 10; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	l2 := openTestLog(t, dir, 4, Hooks{})
	if got := l2.NextOffset(); got != 10 {
		t.Fatalf("nextOffset after reopen = %d, want 10", got)
	}
	for i := 0; i < 10; i++ {
		p, err := l2.Read(uint64(i))
		if err != nil {
			t.Fatalf("read %d after reopen: %v", i, err)
		}
		if p[0] != byte(i) {
			t.Fatalf("payload %d = %d", i, p[0])
		}
	}
	// Appending continues after recovery at the right offset.
	if off, err := l2.Append([]byte{10}); err != nil || off != 10 {
		t.Fatalf("append after reopen = (%d,%v), want (10,nil)", off, err)
	}
}

func TestTornTailRecovery(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 100, Hooks{})
	for i := 0; i < 5; i++ {
		if _, err := l.Append([]byte(fmt.Sprintf("r%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	// Simulate a power loss while a write was in flight: append a partial
	// record (bad header/payload) directly to the segment file.
	seg := filepath.Join(dir, "00000000000000000000.log")
	f, err := os.OpenFile(seg, os.O_WRONLY|os.O_APPEND, filePermission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("TORN")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	l2 := reopenTestLog(t, dir, 100, Hooks{})
	if got := l2.NextOffset(); got != 5 {
		t.Fatalf("nextOffset = %d, want 5 (torn bytes truncated)", got)
	}
	if off, err := l2.Append([]byte("r5")); err != nil || off != 5 {
		t.Fatalf("append after torn recovery = (%d,%v), want (5,nil)", off, err)
	}
	if p, err := l2.Read(5); err != nil || string(p) != "r5" {
		t.Fatalf("read recovered append = (%q,%v)", p, err)
	}
	info, err := os.Stat(seg)
	if err != nil {
		t.Fatal(err)
	}
	if size := info.Size(); size != 6*int64(headerSize+len("r5")) {
		t.Fatalf("segment size = %d, want %d", size, 6*(headerSize+len("r5")))
	}
}

func TestTornSegmentDropsLaterSegments(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 2, Hooks{})
	for i := 0; i < 6; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	// Corrupt the tail of the middle segment; later segments must be dropped.
	mid := filepath.Join(dir, fmt.Sprintf("%020d.log", 2))
	garbage := make([]byte, headerSize+1)
	binary.LittleEndian.PutUint32(garbage[4:], 1) // payload len 1, crc won't match
	f, err := os.OpenFile(mid, os.O_WRONLY|os.O_APPEND, filePermission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(garbage); err != nil {
		t.Fatal(err)
	}
	f.Close()

	l2 := reopenTestLog(t, dir, 2, Hooks{})
	// The torn bytes come after 2 valid records, so the clean prefix ends at
	// offset 4; the later segment (base 4) must have been dropped.
	if got := l2.NextOffset(); got != 4 {
		t.Fatalf("nextOffset = %d, want 4", got)
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%020d.log", 4))); !os.IsNotExist(err) {
		t.Fatalf("later segment still present after torn recovery: %v", err)
	}
	if off, err := l2.Append([]byte{9}); err != nil || off != 4 {
		t.Fatalf("append = (%d,%v), want (4,nil)", off, err)
	}
}

func TestAppendSyncFailureRollsBack(t *testing.T) {
	var fails int
	l := openTestLog(t, t.TempDir(), 100, Hooks{
		BeforeSegmentSync: func() error {
			fails++
			if fails == 1 {
				return errInjected
			}
			return nil
		},
	})
	if _, err := l.Append([]byte("lost")); err != errInjected {
		t.Fatalf("append = %v, want injected error", err)
	}
	if got := l.NextOffset(); got != 0 {
		t.Fatalf("nextOffset after failed append = %d, want 0", got)
	}
	off, err := l.Append([]byte("kept"))
	if err != nil || off != 0 {
		t.Fatalf("retry append = (%d,%v), want (0,nil)", off, err)
	}
	p, err := l.Read(0)
	if err != nil || string(p) != "kept" {
		t.Fatalf("read = (%q,%v), want kept", p, err)
	}
}
