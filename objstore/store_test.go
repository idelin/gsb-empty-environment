package objstore

import (
	"io/ioutil"
	"os"
	"testing"
)

func tempStore(t *testing.T) (*Store, string) {
	dir, err := ioutil.TempDir("", "objstore-test")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return s, dir
}

func mustCommit(t *testing.T, tx *Tx) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestMultiRootShareObject(t *testing.T) {
	s, dir := tempStore(t)
	defer os.RemoveAll(dir)
	defer s.Close()

	tx := s.Begin()
	o := tx.Put([]byte("shared"), nil)
	tx.SetRoot("a", o)
	tx.SetRoot("b", o)
	mustCommit(t, tx)

	// Removing one of several roots must not reclaim the object.
	tx = s.Begin()
	tx.DeleteRoot("a")
	mustCommit(t, tx)
	if n, err := s.GC(); err != nil || n != 0 {
		t.Fatalf("GC collected %d objects, want 0 (err=%v)", n, err)
	}
	if _, ok := s.Get(o); !ok {
		t.Fatal("object referenced by root b was swept")
	}

	// Removing the last root makes it collectable.
	tx = s.Begin()
	tx.DeleteRoot("b")
	mustCommit(t, tx)
	if n, err := s.GC(); err != nil || n != 1 {
		t.Fatalf("GC collected %d objects, want 1 (err=%v)", n, err)
	}
	if _, ok := s.Get(o); ok {
		t.Fatal("unreferenced object survived GC")
	}
}

func TestTransactionAtomicMultiRoot(t *testing.T) {
	s, dir := tempStore(t)
	defer os.RemoveAll(dir)
	defer s.Close()

	tx := s.Begin()
	x := tx.Put([]byte("x"), nil)
	y := tx.Put([]byte("y"), nil)
	tx.SetRoot("r1", x)
	tx.SetRoot("r2", y)
	mustCommit(t, tx)

	// A single commit moves both roots atomically.
	tx = s.Begin()
	tx.SetRoot("r1", y)
	tx.SetRoot("r2", x)
	mustCommit(t, tx)

	if id, _ := s.Root("r1"); id != y {
		t.Fatalf("r1 = %v, want %v", id, y)
	}
	if id, _ := s.Root("r2"); id != x {
		t.Fatalf("r2 = %v, want %v", id, x)
	}

	// A failing commit applies nothing at all.
	tx = s.Begin()
	tx.SetRoot("r1", ID("does-not-exist"))
	tx.SetRoot("r2", y)
	if err := tx.Commit(); err == nil {
		t.Fatal("commit with dangling root target succeeded")
	}
	if id, _ := s.Root("r1"); id != y {
		t.Fatalf("failed commit leaked root change: r1 = %v, want %v", id, y)
	}
	if id, _ := s.Root("r2"); id != x {
		t.Fatalf("failed commit leaked root change: r2 = %v, want %v", id, x)
	}
}

func TestCommitRejectsDanglingRef(t *testing.T) {
	s, dir := tempStore(t)
	defer os.RemoveAll(dir)
	defer s.Close()

	tx := s.Begin()
	tx.Put([]byte("bad"), []ID{ID("missing")})
	if err := tx.Commit(); err == nil {
		t.Fatal("commit with dangling ref succeeded")
	}
}

func TestReachabilityTraversal(t *testing.T) {
	s, dir := tempStore(t)
	defer os.RemoveAll(dir)
	defer s.Close()

	tx := s.Begin()
	c := tx.Put([]byte("c"), nil)
	b := tx.Put([]byte("b"), []ID{c})
	a := tx.Put([]byte("a"), []ID{b})
	tx.SetRoot("root", a)
	mustCommit(t, tx)

	if n, err := s.GC(); err != nil || n != 0 {
		t.Fatalf("GC collected %d, want 0 (err=%v)", n, err)
	}
	for _, id := range []ID{a, b, c} {
		if _, ok := s.Get(id); !ok {
			t.Fatalf("reachable object %s swept", id)
		}
	}

	tx = s.Begin()
	tx.DeleteRoot("root")
	mustCommit(t, tx)
	if n, err := s.GC(); err != nil || n != 3 {
		t.Fatalf("GC collected %d, want 3 (err=%v)", n, err)
	}
}

func TestUnreachableObjectsSwept(t *testing.T) {
	s, dir := tempStore(t)
	defer os.RemoveAll(dir)
	defer s.Close()

	// Objects created without any root in the same commit are garbage.
	tx := s.Begin()
	tx.Put([]byte("garbage1"), nil)
	tx.Put([]byte("garbage2"), nil)
	mustCommit(t, tx)

	if n, err := s.GC(); err != nil || n != 2 {
		t.Fatalf("GC collected %d, want 2 (err=%v)", n, err)
	}
}
