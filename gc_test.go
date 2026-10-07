package objstore

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func mustCommit(t *testing.T, s *Store, fn func(tx *Tx)) {
	t.Helper()
	tx := s.Begin()
	fn(tx)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func mustGet(t *testing.T, s *Store, id string) *Object {
	t.Helper()
	obj, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get(%q): %v", id, err)
	}
	return obj
}

func mustMissing(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.Get(id); err != ErrNotFound {
		t.Fatalf("Get(%q) = %v, want ErrNotFound", id, err)
	}
}

// reachable returns every object ID reachable from the current roots.
func reachable(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		obj, err := s.Get(id)
		if err != nil {
			t.Fatalf("root-reachable object %q missing: %v", id, err)
		}
		seen[id] = true
		for _, ref := range obj.Refs {
			walk(ref)
		}
	}
	for _, id := range s.Roots() {
		walk(id)
	}
	return seen
}

func TestTransactionAtomicMultiRoot(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "shared", Data: []byte("v")})
		tx.Put(&Object{ID: "other"})
		tx.SetRoot("rootA", "shared")
		tx.SetRoot("rootB", "shared")
		tx.SetRoot("rootC", "other")
	})

	for _, name := range []string{"rootA", "rootB"} {
		id, err := s.Root(name)
		if err != nil || id != "shared" {
			t.Fatalf("Root(%q) = %q, %v; want shared", name, id, err)
		}
	}
	mustGet(t, s, "shared")

	// One object referenced by two roots survives while either root lives.
	mustCommit(t, s, func(tx *Tx) { tx.DeleteRoot("rootA") })
	if err := s.RunGC(); err != nil {
		t.Fatal(err)
	}
	mustGet(t, s, "shared")

	mustCommit(t, s, func(tx *Tx) { tx.DeleteRoot("rootB") })
	if err := s.RunGC(); err != nil {
		t.Fatal(err)
	}
	mustMissing(t, s, "shared")
	mustGet(t, s, "other")
}

func TestRunGCKeepsTransitiveClosure(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "a", Refs: []string{"b"}})
		tx.Put(&Object{ID: "b", Refs: []string{"c"}})
		tx.Put(&Object{ID: "c"})
		tx.Put(&Object{ID: "garbage"})
		tx.SetRoot("r", "a")
	})
	if err := s.RunGC(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		mustGet(t, s, id)
	}
	mustMissing(t, s, "garbage")
}

// A root created while the collector is marking must retain its target.
func TestWriteBarrierRootAddedDuringMark(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "anchored"})
		tx.Put(&Object{ID: "late"})
		tx.SetRoot("r1", "anchored")
	})

	markStarted := make(chan struct{})
	resume := make(chan struct{})
	s.hooks.afterMarkStart = func() {
		close(markStarted)
		<-resume
	}

	done := make(chan error, 1)
	go func() { done <- s.RunGC() }()
	<-markStarted

	mustCommit(t, s, func(tx *Tx) { tx.SetRoot("r2", "late") })
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.hooks.afterMarkStart = nil

	mustGet(t, s, "late")
	mustGet(t, s, "anchored")
}

// An object-to-object edge created mid-mark must pull its white target into
// the retained set.
func TestWriteBarrierObjectRefAddedDuringMark(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "orphan"})
	})

	markStarted := make(chan struct{})
	resume := make(chan struct{})
	s.hooks.afterMarkStart = func() {
		close(markStarted)
		<-resume
	}

	done := make(chan error, 1)
	go func() { done <- s.RunGC() }()
	<-markStarted

	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "parent", Refs: []string{"orphan"}})
		tx.SetRoot("r", "parent")
	})
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.hooks.afterMarkStart = nil

	mustGet(t, s, "parent")
	mustGet(t, s, "orphan")
}

// A reference committed while the sweeper is running must not be deleted
// out from under the mutator.
func TestWriteBarrierRootAddedDuringSweep(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "rescued"})
	})

	sweeping := make(chan struct{})
	resume := make(chan struct{})
	s.hooks.beforeSweep = func() {
		close(sweeping)
		<-resume
	}

	done := make(chan error, 1)
	go func() { done <- s.RunGC() }()
	<-sweeping

	mustCommit(t, s, func(tx *Tx) { tx.SetRoot("r", "rescued") })
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.hooks.beforeSweep = nil

	mustGet(t, s, "rescued")
}

// Moving a root mid-mark drops the old subgraph but retains the new one.
func TestRootMovedDuringMark(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mustCommit(t, s, func(tx *Tx) {
		tx.Put(&Object{ID: "old", Refs: []string{"oldChild"}})
		tx.Put(&Object{ID: "oldChild"})
		tx.Put(&Object{ID: "new"})
		tx.SetRoot("r", "old")
	})

	markStarted := make(chan struct{})
	resume := make(chan struct{})
	s.hooks.afterMarkStart = func() {
		close(markStarted)
		<-resume
	}

	done := make(chan error, 1)
	go func() { done <- s.RunGC() }()
	<-markStarted

	mustCommit(t, s, func(tx *Tx) { tx.SetRoot("r", "new") })
	close(resume)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.hooks.afterMarkStart = nil

	mustGet(t, s, "new")
	// "old" was already gray when the root moved, so it is floating garbage:
	// retained by this cycle, reclaimed by the next one.
	if err := s.RunGC(); err != nil {
		t.Fatal(err)
	}
	mustMissing(t, s, "old")
	mustMissing(t, s, "oldChild")
}

// Randomized interleaving of multi-root transactions with repeated GC
// cycles: at every quiescent point the full root closure must be intact.
func TestConcurrentTransactionsAndGC(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wal"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mustCommit(t, s, func(tx *Tx) {
		refs := make([]string, 0, 10)
		for i := 0; i < 10; i++ {
			id := fmt.Sprintf("seed-%d", i)
			tx.Put(&Object{ID: id})
			refs = append(refs, id)
		}
		// The anchor keeps every seed reachable, so worker commits may
		// always reference them.
		tx.Put(&Object{ID: "anchor", Refs: refs})
		tx.SetRoot("anchor", "anchor")
	})

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				tx := s.Begin()
				id := fmt.Sprintf("w%d-%d", w, i)
				tx.Put(&Object{ID: id, Refs: []string{fmt.Sprintf("seed-%d", i%10)}})
				tx.SetRoot(fmt.Sprintf("root-%d", i%6), id)
				if i%3 == 0 {
					tx.DeleteRoot(fmt.Sprintf("root-%d", (i+1)%6))
				}
				if err := tx.Commit(); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
			}
		}(w)
	}

	for cycle := 0; cycle < 6; cycle++ {
		if err := s.RunGC(); err != nil {
			t.Fatalf("RunGC: %v", err)
		}
		reachable(t, s)
	}
	wg.Wait()
	if err := s.RunGC(); err != nil {
		t.Fatal(err)
	}
	reachable(t, s)
}
