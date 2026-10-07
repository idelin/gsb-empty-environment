package objstore

import (
	"errors"
	"path/filepath"
	"testing"
)

// Committed state survives a crash (no clean Close) and is replayed on Open.
func TestCrashRestartReplaysWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "a", Refs: []string{"b"}})
		tx.Put(&Object{ID: "b"})
		tx.SetRoot("r1", "a")
		tx.SetRoot("r2", "b")
	})
	// No Close: simulate a crash. Every commit was fsynced, so the WAL on
	// disk is complete.

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	for _, name := range []string{"r1", "r2"} {
		if _, err := s2.Root(name); err != nil {
			t.Fatalf("Root(%q) after restart: %v", name, err)
		}
	}
	mustGet(t, s2, "a")
	mustGet(t, s2, "b")
	reachable(t, s2)
}

// A crash in the middle of the sweep phase must neither resurrect swept
// objects nor lose survivors: logged deletions replay, un-swept garbage
// simply lives until the next cycle.
func TestCrashDuringSweep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "keep"})
		tx.Put(&Object{ID: "garb1"})
		tx.Put(&Object{ID: "garb2"})
		tx.SetRoot("r", "keep")
	})

	sweptOne := make(chan struct{})
	crash := make(chan struct{})
	var once bool
	s.hooks.sweepStep = func() {
		if once {
			return
		}
		once = true
		close(sweptOne)
		<-crash // freeze the sweeper forever: the "crash"
	}
	t.Cleanup(func() { close(crash) })

	gcDone := make(chan error, 1)
	go func() { gcDone <- s.RunGC() }()
	<-sweptOne

	// Reopen from the same WAL without any shutdown of the first store.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	mustGet(t, s2, "keep")
	survivors := 0
	for _, id := range []string{"garb1", "garb2"} {
		if _, err := s2.Get(id); err == nil {
			survivors++
		} else if err != ErrNotFound {
			t.Fatalf("Get(%q): %v", id, err)
		}
	}
	if survivors != 1 {
		t.Fatalf("want exactly one un-swept garbage object after crash, got %d", survivors)
	}

	// A fresh GC cycle on the recovered store finishes the job.
	if err := s2.RunGC(); err != nil {
		t.Fatal(err)
	}
	mustGet(t, s2, "keep")
	mustMissing(t, s2, "garb1")
	mustMissing(t, s2, "garb2")
}

// A transaction is all-or-nothing across many roots and objects.
func TestTransactionAtomicityOnRollback(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "x"})
		tx.SetRoot("r", "x")
	})

	tx := s.Begin()
	tx.Put(&Object{ID: "y"})
	tx.SetRoot("r", "y")
	tx.SetRoot("r2", "y")
	tx.Rollback()

	mustMissing(t, s, "y")
	if id, _ := s.Root("r"); id != "x" {
		t.Fatalf("root r = %q after rollback, want x", id)
	}
	if _, err := s.Root("r2"); err != ErrNotFound {
		t.Fatalf("root r2 should not exist after rollback")
	}
}

// A commit that would create an edge to a non-existent object — for example
// one the concurrent sweeper just reclaimed — is rejected, never silently
// dangling.
func TestCommitRejectsDanglingReference(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mustCommit(t, s, func(tx *Tx) { tx.Put(&Object{ID: "real"}) })

	tx := s.Begin()
	tx.SetRoot("bad", "ghost")
	if err := tx.Commit(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("commit root to ghost = %v, want ErrNotFound", err)
	}

	tx = s.Begin()
	tx.Put(&Object{ID: "p", Refs: []string{"ghost"}})
	if err := tx.Commit(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("commit ref to ghost = %v, want ErrNotFound", err)
	}

	// References satisfied within the same transaction are fine.
	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "child"})
		tx.Put(&Object{ID: "parent", Refs: []string{"child", "real"}})
		tx.SetRoot("r", "parent")
	})
	reachable(t, s)
}
