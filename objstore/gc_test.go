package objstore

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

// TestWriteBarrierRootMoveDuringMark moves an object from a soon-to-be-
// deleted root to a new root exactly when the mark worklist has drained,
// i.e. in the window right before the sweep. Without the insertion barrier
// the object would still be white and would be reclaimed while reachable.
func TestWriteBarrierRootMoveDuringMark(t *testing.T) {
	s, dir := tempStore(t)
	defer os.RemoveAll(dir)
	defer s.Close()

	tx := s.Begin()
	a := tx.Put([]byte("a"), nil)
	c := tx.Put([]byte("c"), nil)
	tx.SetRoot("keep", a)
	tx.SetRoot("tmp", c)
	mustCommit(t, tx)

	var once sync.Once
	s.markHook = func(drained bool) {
		if !drained {
			return
		}
		once.Do(func() {
			tx := s.Begin()
			tx.DeleteRoot("tmp")
			tx.SetRoot("rescued", c)
			if err := tx.Commit(); err != nil {
				t.Errorf("commit in mark hook: %v", err)
			}
		})
	}
	n, err := s.GC()
	s.markHook = nil
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if n != 0 {
		t.Fatalf("GC swept %d live objects", n)
	}
	if _, ok := s.Resolve("rescued"); !ok {
		t.Fatal("object moved to a new root during mark was swept")
	}
	if _, ok := s.Root("tmp"); ok {
		t.Fatal("deleted root still present")
	}
}

// TestWriteBarrierNewObjectDuringMark creates a brand new object and roots
// it while the mark phase is active. The allocation barrier must retain it.
func TestWriteBarrierNewObjectDuringMark(t *testing.T) {
	s, dir := tempStore(t)
	defer os.RemoveAll(dir)
	defer s.Close()

	tx := s.Begin()
	a := tx.Put([]byte("a"), nil)
	tx.SetRoot("keep", a)
	mustCommit(t, tx)

	var fresh ID
	var once sync.Once
	s.markHook = func(drained bool) {
		if !drained {
			return
		}
		once.Do(func() {
			tx := s.Begin()
			fresh = tx.Put([]byte("fresh"), nil)
			tx.SetRoot("fresh-root", fresh)
			if err := tx.Commit(); err != nil {
				t.Errorf("commit in mark hook: %v", err)
			}
		})
	}
	n, err := s.GC()
	s.markHook = nil
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if n != 0 {
		t.Fatalf("GC swept %d live objects", n)
	}
	if _, ok := s.Get(fresh); !ok {
		t.Fatal("object created and rooted during mark was swept")
	}
}

// TestWriteBarrierNewObjectWithRefsDuringMark creates an object graph (new
// root object referencing a pre-existing unrooted object) during the mark
// phase. Both the new object and its now-referenced child must survive.
func TestWriteBarrierNewObjectWithRefsDuringMark(t *testing.T) {
	s, dir := tempStore(t)
	defer os.RemoveAll(dir)
	defer s.Close()

	tx := s.Begin()
	child := tx.Put([]byte("child"), nil)
	tx.SetRoot("tmp", child)
	mustCommit(t, tx)

	var parent ID
	var once sync.Once
	s.markHook = func(drained bool) {
		if !drained {
			return
		}
		once.Do(func() {
			tx := s.Begin()
			parent = tx.Put([]byte("parent"), []ID{child})
			tx.DeleteRoot("tmp")
			tx.SetRoot("parent-root", parent)
			if err := tx.Commit(); err != nil {
				t.Errorf("commit in mark hook: %v", err)
			}
		})
	}
	if _, err := s.GC(); err != nil {
		t.Fatalf("GC: %v", err)
	}
	s.markHook = nil

	for _, id := range []ID{parent, child} {
		if _, ok := s.Get(id); !ok {
			t.Fatalf("object %s reachable via graph created during mark was swept", id)
		}
	}
}

// TestConcurrentGCAndCommits hammers the store with committing goroutines
// while GC cycles run, then verifies the reachable closure is intact.
// Run with -race to also validate the synchronization.
func TestConcurrentGCAndCommits(t *testing.T) {
	s, dir := tempStore(t)
	defer os.RemoveAll(dir)
	defer s.Close()

	const writers = 4
	const commitsPerWriter = 100

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < commitsPerWriter; i++ {
				tx := s.Begin()
				leaf := tx.Put([]byte(fmt.Sprintf("w%d-%d-leaf", w, i)), nil)
				head := tx.Put([]byte(fmt.Sprintf("w%d-%d-head", w, i)), []ID{leaf})
				tx.SetRoot(fmt.Sprintf("root-%d", w), head)
				if err := tx.Commit(); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
			}
		}(w)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	for {
		select {
		case <-done:
			goto finished
		default:
			if _, err := s.GC(); err != nil {
				t.Fatalf("GC: %v", err)
			}
		}
	}
finished:
	if _, err := s.GC(); err != nil {
		t.Fatalf("final GC: %v", err)
	}

	// Every root must resolve and its whole reference closure must exist.
	for name, id := range s.Roots() {
		if _, ok := s.Get(id); !ok {
			t.Fatalf("root %q points at swept object %s", name, id)
		}
		assertClosureExists(t, s, id)
	}
}

func assertClosureExists(t *testing.T, s *Store, start ID) {
	t.Helper()
	seen := map[ID]bool{}
	queue := []ID{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		o, ok := s.Get(id)
		if !ok {
			t.Fatalf("reachable object %s missing after GC", id)
		}
		queue = append(queue, o.Refs...)
	}
}
