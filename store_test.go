package kvlog

import (
	"bytes"
	"io/ioutil"
	"os"
	"testing"
)

func mustOpen(t *testing.T, fs FS) *Store {
	t.Helper()
	s, err := Open(fs)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func mustAppend(t *testing.T, s *Store, key, val string) {
	t.Helper()
	if err := s.Append(key, []byte(val)); err != nil {
		t.Fatalf("Append(%q): %v", key, err)
	}
}

func mustGet(t *testing.T, s *Store, key, want string) {
	t.Helper()
	got, ok, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if !ok {
		t.Fatalf("Get(%q): not found, want %q", key, want)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, got, want)
	}
}

func mustMiss(t *testing.T, s *Store, key string) {
	t.Helper()
	if _, ok, err := s.Get(key); err != nil || ok {
		t.Fatalf("Get(%q): ok=%v err=%v, want clean miss", key, ok, err)
	}
}

// crash simulates power loss: unsynced writes vanish, store reopens.
func crash(t *testing.T, fs *MemFS, s *Store) *Store {
	t.Helper()
	s.Close()
	fs.Crash()
	return mustOpen(t, fs)
}

func TestAppendGetRoundtrip(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "a", "1")
	mustAppend(t, s, "b", "2")
	mustAppend(t, s, "a", "3")
	mustGet(t, s, "a", "3")
	mustGet(t, s, "b", "2")
	mustMiss(t, s, "nope")
}

// Scenario: torn append. Only part of the tail record reached disk.
// Rebuild must ignore it and keep the store usable.
func TestRebuildIgnoresTornTail(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "a", "1")
	goodTail := s.tail

	// Torn write: only a prefix of the record is synced.
	torn := encodeRecord(record{gen: s.gen, version: 1, key: "b", value: []byte("2")})
	if _, err := s.log.WriteAt(torn[:len(torn)/2], s.tail); err != nil {
		t.Fatal(err)
	}
	s.log.Sync()

	s = crash(t, fs, s)
	if s.tail != goodTail {
		t.Fatalf("tail = %d, want %d (torn record must be truncated)", s.tail, goodTail)
	}
	mustGet(t, s, "a", "1")
	mustMiss(t, s, "b")
	// Log stays appendable after truncation.
	mustAppend(t, s, "c", "3")
	s = crash(t, fs, s)
	mustGet(t, s, "a", "1")
	mustGet(t, s, "c", "3")
}

// Scenario: garbage (not even a partial record) at the tail.
func TestRebuildIgnoresGarbageTail(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "a", "1")
	if _, err := s.log.WriteAt([]byte{0xde, 0xad, 0xbe, 0xef, 0x42}, s.tail); err != nil {
		t.Fatal(err)
	}
	s.log.Sync()
	s = crash(t, fs, s)
	mustGet(t, s, "a", "1")
}

// Scenario: data append completed, directory update never happened.
// Rebuild from the log recovers everything.
func TestDataSurvivesMissingDirectoryUpdate(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "a", "1")
	mustAppend(t, s, "b", "2")
	// No PersistDir: crash between data sync and directory update.
	s = crash(t, fs, s)
	mustGet(t, s, "a", "1")
	mustGet(t, s, "b", "2")
}

// Scenario: directory snapshot is older than the log (appends happened
// after PersistDir). The stale snapshot entry must not shadow the newer
// record in the log.
func TestStaleSnapshotVersionRejected(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "k", "v1")
	if err := s.PersistDir(); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, s, "k", "v2") // log moves ahead of the snapshot
	s = crash(t, fs, s)
	mustGet(t, s, "k", "v2")
}

// Scenario: directory file is corrupted (torn snapshot write, bit rot).
// Open must fall back to the log instead of failing or misreading.
func TestCorruptDirectoryFallsBackToLog(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "a", "1")
	mustAppend(t, s, "b", "2")
	if err := s.PersistDir(); err != nil {
		t.Fatal(err)
	}
	// Corrupt the snapshot in place and make the corruption durable.
	f, err := fs.OpenFile(dirFileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xff, 0x00, 0xff}, 5); err != nil {
		t.Fatal(err)
	}
	f.Sync()
	s = crash(t, fs, s)
	mustGet(t, s, "a", "1")
	mustGet(t, s, "b", "2")
}

// Scenario: crash during compaction, before the atomic rename. The old
// log must be untouched and the orphan temp file cleaned up.
func TestCompactionCrashBeforeRename(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "a", "1")
	mustAppend(t, s, "b", "2")
	if _, _, _, err := s.writeCompacted(); err != nil {
		t.Fatal(err)
	}
	// Crash in the window: new file synced, rename not done.
	s = crash(t, fs, s)
	if _, ok := fs.files[compactFileName]; ok {
		t.Fatal("orphan compaction temp file not cleaned up on open")
	}
	mustGet(t, s, "a", "1")
	mustGet(t, s, "b", "2")
}

// Scenario: crash after the compaction rename but before the new
// directory snapshot. The on-disk snapshot names the old generation and
// must be ignored; rebuild from the new log yields correct data.
func TestCompactionCrashAfterRenameBeforeDirPersist(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "a", "1")
	mustAppend(t, s, "a", "2")
	mustAppend(t, s, "b", "3")
	if err := s.PersistDir(); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	// Crash before PersistDir: snapshot on disk is from the old generation.
	s = crash(t, fs, s)
	mustGet(t, s, "a", "2")
	mustGet(t, s, "b", "3")
}

// Scenario: stale offsets from before compaction must never misread.
// The old offset of "a" either fails record validation (key/version/gen)
// or names a superseded version; Get must fall back to the log.
func TestStaleOffsetAfterCompactionCannotMisread(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "a", "one")
	mustAppend(t, s, "b", "two")
	mustAppend(t, s, "a", "three")
	stale := map[string]dirEntry{}
	for k, e := range s.dir {
		stale[k] = e
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	// Simulate a directory that survived the switch with old offsets.
	s.dir = stale
	mustGet(t, s, "a", "three")
	mustGet(t, s, "b", "two")
	mustMiss(t, s, "c")
}

// Scenario: snapshot from a previous generation must be rejected wholesale.
func TestSnapshotGenerationMismatchRejected(t *testing.T) {
	fs := NewMemFS()
	s := mustOpen(t, fs)
	mustAppend(t, s, "a", "1")
	if err := s.PersistDir(); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	s = crash(t, fs, s)
	mustGet(t, s, "a", "1")
}

func TestOSFSRoundtrip(t *testing.T) {
	dir, err := ioutil.TempDir("", "kvlog")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	fs := NewOSFS(dir)
	s := mustOpen(t, fs)
	mustAppend(t, s, "x", "10")
	mustAppend(t, s, "y", "20")
	if err := s.PersistDir(); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistDir(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = mustOpen(t, fs)
	mustGet(t, s, "x", "10")
	mustGet(t, s, "y", "20")
}

func TestRecordRoundtrip(t *testing.T) {
	rec := record{gen: 7, version: 3, key: "hello", value: []byte("world")}
	got, n, err := decodeRecord(encodeRecord(rec))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(encodeRecord(rec)) || got.gen != 7 || got.version != 3 ||
		got.key != "hello" || !bytes.Equal(got.value, []byte("world")) {
		t.Fatalf("roundtrip mismatch: %+v n=%d", got, n)
	}
	buf := encodeRecord(rec)
	buf[len(buf)-1] ^= 0xff
	if _, _, err := decodeRecord(buf); err != ErrCorrupt {
		t.Fatalf("corrupt crc: err=%v, want ErrCorrupt", err)
	}
	if _, _, err := decodeRecord(buf[:len(buf)/2]); err != ErrCorrupt {
		t.Fatalf("truncated: err=%v, want ErrCorrupt", err)
	}
}
