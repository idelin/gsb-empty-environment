package objstore

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "objstore-crash")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCrashRestartPersistsCommittedState simulates a crash by abandoning
// the store without Close (every commit is fsynced, so it must survive)
// and reopening the same directory.
func TestCrashRestartPersistsCommittedState(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := s.Begin()
	a := tx.Put([]byte("a"), nil)
	b := tx.Put([]byte("b"), []ID{a})
	tx.SetRoot("r1", b)
	tx.SetRoot("r2", a)
	mustCommit(t, tx)
	tx = s.Begin()
	tx.DeleteRoot("r2")
	tx.SetRoot("r3", a)
	mustCommit(t, tx)
	// No Close: simulate power loss.

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	if id, ok := s2.Root("r1"); !ok || id != b {
		t.Fatalf("r1 = %v,%v want %v,true", id, ok, b)
	}
	if _, ok := s2.Root("r2"); ok {
		t.Fatal("r2 should have been deleted")
	}
	if id, ok := s2.Root("r3"); !ok || id != a {
		t.Fatalf("r3 = %v,%v want %v,true", id, ok, a)
	}
	o, ok := s2.Get(b)
	if !ok || len(o.Refs) != 1 || o.Refs[0] != a {
		t.Fatalf("object b not recovered correctly: %+v", o)
	}
}

// TestPartialWALRecordDiscarded appends a torn record (as a crash during a
// commit write would leave behind) and verifies recovery ignores it while
// keeping all fully committed transactions.
func TestPartialWALRecordDiscarded(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := s.Begin()
	a := tx.Put([]byte("a"), nil)
	tx.SetRoot("r1", a)
	tx.SetRoot("r2", a)
	mustCommit(t, tx)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Torn tail: valid magic, then a record cut off mid-payload.
	f, err := os.OpenFile(filepath.Join(dir, walFileName), os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("OBJ1\x00\x00\x00\x40garbage-payload-that-never-f")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if id, ok := s2.Root("r1"); !ok || id != a {
		t.Fatalf("committed root lost after torn tail: %v %v", id, ok)
	}
	if id, ok := s2.Root("r2"); !ok || id != a {
		t.Fatalf("committed root lost after torn tail: %v %v", id, ok)
	}

	// The store must still accept commits after truncating the torn tail.
	tx = s2.Begin()
	b := tx.Put([]byte("b"), nil)
	tx.SetRoot("r3", b)
	mustCommit(t, tx)
	s2.Close()

	s3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	if id, ok := s3.Root("r3"); !ok || id != b {
		t.Fatalf("post-recovery commit not durable: %v %v", id, ok)
	}
}

// TestGCDeletionsDurableAcrossRestart verifies swept objects stay gone
// after a crash and reopen.
func TestGCDeletionsDurableAcrossRestart(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := s.Begin()
	live := tx.Put([]byte("live"), nil)
	dead := tx.Put([]byte("dead"), nil)
	tx.SetRoot("keep", live)
	tx.SetRoot("gone", dead)
	mustCommit(t, tx)
	tx = s.Begin()
	tx.DeleteRoot("gone")
	mustCommit(t, tx)
	n, err := s.GC()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("GC collected %d, want 1", n)
	}
	// Crash without Close.

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, ok := s2.Get(dead); ok {
		t.Fatal("swept object resurrected after restart")
	}
	if _, ok := s2.Get(live); !ok {
		t.Fatal("live object lost after restart")
	}
}

// TestCrashDuringMark simulates a power loss in the middle of the mark
// phase: while GC is between mark and sweep, a second store is opened on
// the same directory (the "post-crash" process) and must see exactly the
// last committed state.
func TestCrashDuringMark(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := s.Begin()
	a := tx.Put([]byte("a"), nil)
	garbage := tx.Put([]byte("garbage"), nil)
	tx.SetRoot("r1", a)
	mustCommit(t, tx)

	var once sync.Once
	s.markHook = func(drained bool) {
		if !drained {
			return
		}
		once.Do(func() {
			// "Crash" here: recover from disk in a fresh handle.
			s2, err := Open(dir)
			if err != nil {
				t.Errorf("reopen during mark: %v", err)
				return
			}
			defer s2.Close()
			if id, ok := s2.Root("r1"); !ok || id != a {
				t.Errorf("recovered root mismatch: %v %v", id, ok)
			}
			if _, ok := s2.Get(garbage); !ok {
				t.Errorf("uncommitted sweep must not be visible after crash")
			}
		})
	}
	if _, err := s.GC(); err != nil {
		t.Fatal(err)
	}
	s.markHook = nil
	if _, ok := s.Get(garbage); ok {
		t.Fatal("garbage survived completed GC")
	}
}

// TestSnapshotAndRestart covers the snapshot + WAL-truncate path.
func TestSnapshotAndRestart(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := s.Begin()
	a := tx.Put([]byte("a"), nil)
	tx.SetRoot("r1", a)
	mustCommit(t, tx)
	if err := s.Snapshot(); err != nil {
		t.Fatal(err)
	}
	// More commits after the snapshot: must be replayed from the WAL.
	tx = s.Begin()
	b := tx.Put([]byte("b"), nil)
	tx.SetRoot("r2", b)
	mustCommit(t, tx)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if id, ok := s2.Root("r1"); !ok || id != a {
		t.Fatalf("snapshot state lost: %v %v", id, ok)
	}
	if id, ok := s2.Root("r2"); !ok || id != b {
		t.Fatalf("post-snapshot WAL state lost: %v %v", id, ok)
	}
}

// TestAtomicCommitAcrossCrash writes a multi-root transaction, then a torn
// second transaction, and verifies the first is fully applied and the
// second fully absent: multi-root commits are all-or-nothing.
func TestAtomicCommitAcrossCrash(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx := s.Begin()
	x := tx.Put([]byte("x"), nil)
	y := tx.Put([]byte("y"), nil)
	tx.SetRoot("ra", x)
	tx.SetRoot("rb", y)
	tx.SetRoot("rc", x)
	mustCommit(t, tx)
	s.Close()

	// Simulate a crash mid-commit of a second multi-root transaction by
	// truncating its WAL record: write it, then cut the file back.
	walPath := filepath.Join(dir, walFileName)
	fi, err := os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	goodLen := fi.Size()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tx = s2.Begin()
	tx.SetRoot("ra", y)
	tx.SetRoot("rb", x)
	mustCommit(t, tx)
	s2.Close()
	if err := os.Truncate(walPath, goodLen+7); err != nil { // torn record
		t.Fatal(err)
	}

	s3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	// Only the first transaction survived, with all three roots intact.
	if id, _ := s3.Root("ra"); id != x {
		t.Fatalf("ra = %v, want %v", id, x)
	}
	if id, _ := s3.Root("rb"); id != y {
		t.Fatalf("rb = %v, want %v", id, y)
	}
	if id, _ := s3.Root("rc"); id != x {
		t.Fatalf("rc = %v, want %v", id, x)
	}
}
