package segvm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publish(t *testing.T, m *Manager, adds []Change, removes []string) uint64 {
	t.Helper()
	v, err := m.Publish(adds, removes)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return v
}

func logBytes(t *testing.T, dir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return data
}

func TestIncompleteTailToleratedAndTruncated(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, m, []Change{{Name: "a", Data: []byte("aaa")}}, nil)
	publish(t, m, []Change{{Name: "b", Data: []byte("bbb")}}, nil)
	validLen := len(logBytes(t, dir))

	// Simulate a torn write: half of a third record frame.
	frame := encodeRecord(&Record{Version: 3, Prev: "whatever"})
	f, err := os.OpenFile(filepath.Join(dir, "manifest.log"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(frame[:len(frame)/2]); err != nil {
		t.Fatal(err)
	}
	f.Close()

	m2, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatalf("open with incomplete tail: %v", err)
	}
	if m2.Version() != 2 {
		t.Fatalf("version = %d, want 2", m2.Version())
	}
	if got := len(logBytes(t, dir)); got != validLen {
		t.Fatalf("log not truncated: size %d, want %d", got, validLen)
	}
	// Store still usable after repair.
	publish(t, m2, []Change{{Name: "c", Data: []byte("ccc")}}, nil)
	if m2.Version() != 3 {
		t.Fatalf("version = %d, want 3", m2.Version())
	}
}

func TestCorruptCompleteRecordFails(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, m, []Change{{Name: "a", Data: []byte("aaa")}}, nil)
	publish(t, m, []Change{{Name: "b", Data: []byte("bbb")}}, nil)

	// Flip a payload byte inside the LAST record, keeping the frame
	// length intact: this is a complete-but-corrupt record.
	data := logBytes(t, dir)
	data[len(data)-6] ^= 0xFF
	if err := os.WriteFile(filepath.Join(dir, "manifest.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Open(OSFS{}, dir)
	if !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("err = %v, want ErrCorruptRecord", err)
	}
}

func TestCorruptMiddleRecordFails(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, m, []Change{{Name: "a", Data: []byte("aaa")}}, nil)
	publish(t, m, []Change{{Name: "b", Data: []byte("bbb")}}, nil)
	publish(t, m, []Change{{Name: "c", Data: []byte("ccc")}}, nil)

	data := logBytes(t, dir)
	data[10] ^= 0xFF // inside first record's payload
	if err := os.WriteFile(filepath.Join(dir, "manifest.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(OSFS{}, dir); err == nil {
		t.Fatal("expected error for corrupt middle record")
	}
}

func TestVersionChainEnforced(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, m, []Change{{Name: "a", Data: []byte("aaa")}}, nil)

	// Hand-craft a second record with a skipped version number but a
	// valid CRC: structural chain violation, not a checksum issue.
	r1 := m.Versions()[0]
	bad := &Record{Version: 3, Prev: recordHash(&r1), Adds: []SegmentMeta{{Name: "b", Digest: "x", Size: 1}}}
	f, err := os.OpenFile(filepath.Join(dir, "manifest.log"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(encodeRecord(bad))
	f.Close()

	_, err = Open(OSFS{}, dir)
	if err == nil || !strings.Contains(err.Error(), "version chain") {
		t.Fatalf("err = %v, want version chain error", err)
	}
}

func TestHashChainEnforced(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, m, []Change{{Name: "a", Data: []byte("aaa")}}, nil)

	bad := &Record{Version: 2, Prev: "bogus", Adds: []SegmentMeta{{Name: "b", Digest: "x", Size: 1}}}
	f, err := os.OpenFile(filepath.Join(dir, "manifest.log"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(encodeRecord(bad))
	f.Close()

	_, err = Open(OSFS{}, dir)
	if err == nil || !strings.Contains(err.Error(), "hash chain") {
		t.Fatalf("err = %v, want hash chain error", err)
	}
}

func TestShortGarbageTailTolerated(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, m, []Change{{Name: "a", Data: []byte("aaa")}}, nil)

	// Fewer than 4 trailing bytes: cannot even be a length prefix.
	f, err := os.OpenFile(filepath.Join(dir, "manifest.log"), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0x00, 0x01, 0x02})
	f.Close()

	m2, err := Open(OSFS{}, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if m2.Version() != 1 {
		t.Fatalf("version = %d, want 1", m2.Version())
	}
}
