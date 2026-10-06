package keydir

import (
	"errors"
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

func mustPut(t *testing.T, s *Store, key, val string) {
	t.Helper()
	if err := s.Put(key, []byte(val)); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func mustGet(t *testing.T, s *Store, key, want string) {
	t.Helper()
	got, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, got, want)
	}
}

func TestPutGetAndReopen(t *testing.T) {
	fs := newMemFS()
	s := mustOpen(t, fs)
	mustPut(t, s, "a", "1")
	mustPut(t, s, "b", "2")
	mustGet(t, s, "a", "1")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2 := mustOpen(t, fs)
	mustGet(t, s2, "a", "1")
	mustGet(t, s2, "b", "2")
	if _, err := s2.Get("missing"); err != ErrNotFound {
		t.Fatalf("Get(missing) err = %v, want ErrNotFound", err)
	}
}

// A torn tail (partial record made durable by a crash mid-append) must
// be ignored on rebuild, and later writes must still be reachable.
func TestRebuildIgnoresIncompleteTail(t *testing.T) {
	fs := newMemFS()
	s := mustOpen(t, fs)
	mustPut(t, s, "a", "1")
	mustPut(t, s, "b", "2")

	// Crash lands half of a third record on disk.
	torn, err := encodeRecord(record{version: 3, key: "c", value: []byte("3")})
	if err != nil {
		t.Fatal(err)
	}
	fs.appendRaw("000001.data", torn[:len(torn)/2], true)
	fs.Crash()

	s2 := mustOpen(t, fs)
	mustGet(t, s2, "a", "1")
	mustGet(t, s2, "b", "2")
	if _, err := s2.Get("c"); err != ErrNotFound {
		t.Fatalf("Get(c) err = %v, want ErrNotFound", err)
	}

	// The torn tail was truncated on recovery, so new appends are
	// reachable after another restart.
	mustPut(t, s2, "c", "3")
	fs.Crash()
	s3 := mustOpen(t, fs)
	mustGet(t, s3, "a", "1")
	mustGet(t, s3, "c", "3")
}

// A record whose bytes were bit-flipped on disk must be ignored on
// rebuild instead of poisoning the directory.
func TestRebuildIgnoresCorruptRecord(t *testing.T) {
	fs := newMemFS()
	s := mustOpen(t, fs)
	mustPut(t, s, "a", "1")
	mustPut(t, s, "b", "2")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Damage the payload of the second record in the data file.
	f := fs.files["000001.data"]
	fs.Corrupt("000001.data", int64(len(f.data)-1))
	if err := fs.Remove(dirFileName); err != nil {
		t.Fatal(err)
	}

	s2 := mustOpen(t, fs)
	mustGet(t, s2, "a", "1")
	if _, err := s2.Get("b"); err != ErrNotFound {
		t.Fatalf("Get(b) err = %v, want ErrNotFound", err)
	}
}

// Crash after the data append is durable but before the directory
// checkpoint: recovery replays the log past the checkpoint watermark.
func TestCrashBetweenDataAppendAndDirCheckpoint(t *testing.T) {
	fs := newMemFS()
	s := mustOpen(t, fs)
	mustPut(t, s, "a", "1")
	if err := s.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	// Data append + sync complete; the checkpoint never happens.
	mustPut(t, s, "b", "2")
	fs.Crash()

	s2 := mustOpen(t, fs)
	mustGet(t, s2, "a", "1")
	mustGet(t, s2, "b", "2") // recovered from the log, not the directory
}

// A damaged directory checkpoint must not lose data: the log is the
// source of truth and the directory is rebuilt from scratch.
func TestCorruptDirectoryCheckpoint(t *testing.T) {
	fs := newMemFS()
	s := mustOpen(t, fs)
	mustPut(t, s, "a", "1")
	mustPut(t, s, "b", "2")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	fs.Corrupt(dirFileName, 10)

	s2 := mustOpen(t, fs)
	mustGet(t, s2, "a", "1")
	mustGet(t, s2, "b", "2")
}

// After compaction, directory entries captured before the switch point
// at reclaimed files and must be rejected, never misread.
func TestCompactionInvalidatesOldOffsets(t *testing.T) {
	fs := newMemFS()
	s := mustOpen(t, fs)
	mustPut(t, s, "a", "old-a")
	mustPut(t, s, "b", "1")
	mustPut(t, s, "a", "new-a")
	stale := s.dir["a"] // points into the pre-compaction file

	if err := s.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	mustGet(t, s, "a", "new-a")
	mustGet(t, s, "b", "1")

	if _, err := s.readEntry("a", stale); err != ErrStaleDir {
		t.Fatalf("readEntry(stale) err = %v, want ErrStaleDir", err)
	}
	for _, name := range mustList(t, fs) {
		if id, ok := parseDataFileID(name); ok && id == stale.fileID {
			t.Fatalf("old data file %q not reclaimed", name)
		}
	}

	// State survives a restart through the post-compaction checkpoint.
	fs.Crash()
	s2 := mustOpen(t, fs)
	mustGet(t, s2, "a", "new-a")
	mustGet(t, s2, "b", "1")
}

// A directory hit must validate the record's key and version at the
// stored offset: an offset that now lands on different bytes is a
// stale entry, not a valid read.
func TestDirectoryHitValidatesKeyAndVersion(t *testing.T) {
	fs := newMemFS()
	s := mustOpen(t, fs)
	mustPut(t, s, "a", "1")
	mustPut(t, s, "b", "2")

	ea, eb := s.dir["a"], s.dir["b"]

	// Same file, but the offset of a different key's record.
	wrongKey := dirEntry{fileID: ea.fileID, offset: eb.offset, size: eb.size, version: ea.version}
	if _, err := s.readEntry("a", wrongKey); err != ErrStaleDir {
		t.Fatalf("wrong-key read err = %v, want ErrStaleDir", err)
	}

	// Right offset, but a version that does not match the record.
	wrongVersion := dirEntry{fileID: ea.fileID, offset: ea.offset, size: ea.size, version: ea.version + 1}
	if _, err := s.readEntry("a", wrongVersion); err != ErrStaleDir {
		t.Fatalf("wrong-version read err = %v, want ErrStaleDir", err)
	}

	// Offset into reclaimed/unknown file.
	gone := dirEntry{fileID: 999, offset: 0, size: ea.size, version: ea.version}
	if _, err := s.readEntry("a", gone); err != ErrStaleDir {
		t.Fatalf("gone-file read err = %v, want ErrStaleDir", err)
	}
}

// removeFailFS fails the first Remove, crashing compaction mid-switch
// after the new file is durable but before old files are reclaimed.
type removeFailFS struct {
	FS
	failed bool
}

func (fs *removeFailFS) Remove(name string) error {
	if !fs.failed {
		fs.failed = true
		return errors.New("injected remove failure")
	}
	return fs.FS.Remove(name)
}

func TestCrashDuringCompactionSwitch(t *testing.T) {
	mfs := newMemFS()
	s := mustOpen(t, mfs)
	mustPut(t, s, "a", "1")
	mustPut(t, s, "b", "2")
	mustPut(t, s, "a", "3")

	fs := &removeFailFS{FS: mfs}
	s.fs = fs
	if err := s.Compact(); err == nil {
		t.Fatal("Compact: expected injected failure")
	}

	// Power is lost with both old and new data files on disk and a
	// pre-compaction directory checkpoint.
	mfs.Crash()

	s2 := mustOpen(t, mfs)
	mustGet(t, s2, "a", "3")
	mustGet(t, s2, "b", "2")
}

func mustList(t *testing.T, fs FS) []string {
	t.Helper()
	names, err := fs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return names
}
