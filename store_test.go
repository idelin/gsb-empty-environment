package pathindex

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempStore(t *testing.T) (*Store, string, func()) {
	t.Helper()
	dir, err := ioutil.TempDir("", "pathindex-test-")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return s, dir, func() { os.RemoveAll(dir) }
}

func mustCreate(t *testing.T, s *Store, id, parent, name string) {
	t.Helper()
	if err := s.Create(id, parent, name); err != nil {
		t.Fatalf("Create(%q,%q,%q): %v", id, parent, name, err)
	}
}

func mustPath(t *testing.T, s *Store, id, want string) {
	t.Helper()
	got, err := s.Path(id)
	if err != nil {
		t.Fatalf("Path(%q): %v", id, err)
	}
	if got != want {
		t.Fatalf("Path(%q) = %q, want %q", id, got, want)
	}
}

// build /a (A) with /a/b (B) and /a/b/c (C), plus /other (O).
func buildTree(t *testing.T, s *Store) {
	t.Helper()
	mustCreate(t, s, "A", rootID, "a")
	mustCreate(t, s, "B", "A", "b")
	mustCreate(t, s, "C", "B", "c")
	mustCreate(t, s, "O", rootID, "other")
}

func TestCreateAndPath(t *testing.T) {
	s, _, cleanup := tempStore(t)
	defer cleanup()
	buildTree(t, s)
	mustPath(t, s, "A", "/a")
	mustPath(t, s, "B", "/a/b")
	mustPath(t, s, "C", "/a/b/c")
}

func TestMoveSubtreeUpdatesAllDescendantPaths(t *testing.T) {
	s, _, cleanup := tempStore(t)
	defer cleanup()
	buildTree(t, s)
	if err := s.Move("A", "O"); err != nil {
		t.Fatal(err)
	}
	mustPath(t, s, "A", "/other/a")
	mustPath(t, s, "B", "/other/a/b")
	mustPath(t, s, "C", "/other/a/b/c")
}

func TestMoveIntoOwnDescendantRejected(t *testing.T) {
	s, _, cleanup := tempStore(t)
	defer cleanup()
	buildTree(t, s)
	v := s.Version()
	if err := s.Move("A", "B"); err == nil || !strings.Contains(err.Error(), ErrCycle.Error()) {
		t.Fatalf("expected cycle error, got %v", err)
	}
	// Moving A directly under itself is also a cycle.
	if err := s.Move("A", "A"); err == nil {
		t.Fatal("expected cycle error for self-move")
	}
	if s.Version() != v {
		t.Fatal("version changed after rejected move")
	}
	mustPath(t, s, "A", "/a")
	mustPath(t, s, "B", "/a/b")
	mustPath(t, s, "C", "/a/b/c")
}

func TestBatchCycleAcrossOpsRejected(t *testing.T) {
	s, _, cleanup := tempStore(t)
	defer cleanup()
	buildTree(t, s)
	// Neither op alone is a cycle, but together A->B and B->A are.
	err := s.MoveBatch([]MoveOp{
		{ID: "A", NewParent: "O"},
		{ID: "O", NewParent: "A"},
	})
	if err == nil || !strings.Contains(err.Error(), ErrCycle.Error()) {
		t.Fatalf("expected cycle error, got %v", err)
	}
	mustPath(t, s, "A", "/a")
	mustPath(t, s, "O", "/other")
}

func TestSiblingConflictDetectedRegardlessOfBatchOrder(t *testing.T) {
	s, _, cleanup := tempStore(t)
	defer cleanup()
	mustCreate(t, s, "D1", rootID, "d1")
	mustCreate(t, s, "D2", rootID, "d2")
	mustCreate(t, s, "X", "D1", "x")
	mustCreate(t, s, "Y", "D2", "same")

	// Direct collision: moving Y into D1 under X's name.
	err := s.MoveBatch([]MoveOp{{ID: "Y", NewParent: "D1", NewName: "x"}})
	if err == nil || !strings.Contains(err.Error(), ErrConflict.Error()) {
		t.Fatalf("expected conflict, got %v", err)
	}

	// The conflict only exists in the FINAL state of the batch
	// (X renamed to "same" AND Y moved into D1). Both op orders
	// must be rejected; validation must not depend on order.
	err = s.MoveBatch([]MoveOp{
		{ID: "X", NewParent: "D1", NewName: "same"},
		{ID: "Y", NewParent: "D1"},
	})
	if err == nil || !strings.Contains(err.Error(), ErrConflict.Error()) {
		t.Fatalf("rename-first order: expected conflict, got %v", err)
	}
	err = s.MoveBatch([]MoveOp{
		{ID: "Y", NewParent: "D1"},
		{ID: "X", NewParent: "D1", NewName: "same"},
	})
	if err == nil || !strings.Contains(err.Error(), ErrConflict.Error()) {
		t.Fatalf("move-first order: expected conflict, got %v", err)
	}
	mustPath(t, s, "X", "/d1/x")
	mustPath(t, s, "Y", "/d2/same")
}

func TestBatchSlotFreedByEarlierOrLaterOpIsAllowed(t *testing.T) {
	s, _, cleanup := tempStore(t)
	defer cleanup()
	mustCreate(t, s, "D1", rootID, "d1")
	mustCreate(t, s, "D2", rootID, "d2")
	mustCreate(t, s, "X", "D1", "n")
	mustCreate(t, s, "Y", "D2", "n")

	// Final state is a pure swap of names' homes; no order of
	// sequential validation would see a free slot for both, but the
	// end state is conflict-free and must be accepted.
	err := s.MoveBatch([]MoveOp{
		{ID: "X", NewParent: "D2"},
		{ID: "Y", NewParent: "D1"},
	})
	if err != nil {
		t.Fatalf("swap should be allowed, got %v", err)
	}
	mustPath(t, s, "X", "/d2/n")
	mustPath(t, s, "Y", "/d1/n")
}

func TestFailedBatchLeavesEverythingUnchanged(t *testing.T) {
	s, _, cleanup := tempStore(t)
	defer cleanup()
	buildTree(t, s)
	mustCreate(t, s, "X", "O", "x")
	v := s.Version()

	// First op is fine, second creates a cycle: whole batch must roll back.
	err := s.MoveBatch([]MoveOp{
		{ID: "X", NewParent: "A"},
		{ID: "A", NewParent: "B"},
	})
	if err == nil {
		t.Fatal("expected batch to fail")
	}
	if s.Version() != v {
		t.Fatal("version changed after failed batch")
	}
	mustPath(t, s, "X", "/other/x")
	mustPath(t, s, "A", "/a")
	mustPath(t, s, "C", "/a/b/c")
}

func TestInjectedFailureKeepsStateAndPaths(t *testing.T) {
	for _, fp := range []FailPoint{FailBeforeCommit, FailAfterTmpWrite} {
		s, dir, cleanup := tempStore(t)
		buildTree(t, s)
		v := s.Version()
		s.FailAt = fp
		if err := s.Move("A", "O"); err != ErrInjected {
			t.Fatalf("%v: expected ErrInjected, got %v", fp, err)
		}
		s.FailAt = FailOff
		if s.Version() != v {
			t.Fatalf("%v: version changed after injected failure", fp)
		}
		mustPath(t, s, "A", "/a")
		mustPath(t, s, "C", "/a/b/c")

		// Reopen: recovery must show the old committed state.
		s2, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if s2.Version() != v {
			t.Fatalf("%v: recovered version %d, want %d", fp, s2.Version(), v)
		}
		mustPath(t, s2, "A", "/a")
		mustPath(t, s2, "C", "/a/b/c")
		cleanup()
	}
}

func TestCrashAfterRenameRecoversNewState(t *testing.T) {
	s, dir, cleanup := tempStore(t)
	defer cleanup()
	buildTree(t, s)
	s.FailAt = FailAfterRename
	if err := s.Move("A", "O"); err != ErrInjected {
		t.Fatalf("expected ErrInjected, got %v", err)
	}
	// The rename already happened: recovery observes the new state.
	// Atomicity means old-or-new, never torn; here it is the new one.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mustPath(t, s2, "A", "/other/a")
	mustPath(t, s2, "C", "/other/a/b/c")
}

func TestStaleTempFileRemovedOnOpen(t *testing.T) {
	s, dir, cleanup := tempStore(t)
	defer cleanup()
	buildTree(t, s)
	v := s.Version()
	// Simulate a crash leftover: garbage temp file next to state.json.
	if err := ioutil.WriteFile(filepath.Join(dir, tmpFileName), []byte("garbage"), 0644); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, tmpFileName)); !os.IsNotExist(err) {
		t.Fatal("stale temp file not cleaned up")
	}
	if s2.Version() != v {
		t.Fatalf("version %d, want %d", s2.Version(), v)
	}
	mustPath(t, s2, "C", "/a/b/c")
}

func TestRenameKeepsIDAndInvalidatesCache(t *testing.T) {
	s, _, cleanup := tempStore(t)
	defer cleanup()
	buildTree(t, s)
	mustPath(t, s, "A", "/a") // populate cache
	if err := s.Rename("A", "a2"); err != nil {
		t.Fatal(err)
	}
	// Same ID, new path; the cached "/a" must not be served.
	mustPath(t, s, "A", "/a2")
	mustPath(t, s, "B", "/a2/b")
	mustPath(t, s, "C", "/a2/b/c")
	kids, err := s.Children("A")
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0] != "B" {
		t.Fatalf("children of A = %v, want [B]", kids)
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	s, dir, cleanup := tempStore(t)
	defer cleanup()
	buildTree(t, s)
	if err := s.Move("A", "O"); err != nil {
		t.Fatal(err)
	}
	v := s.Version()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Version() != v {
		t.Fatalf("version %d, want %d", s2.Version(), v)
	}
	mustPath(t, s2, "C", "/other/a/b/c")
}

func TestCreateRejectsDuplicates(t *testing.T) {
	s, _, cleanup := tempStore(t)
	defer cleanup()
	mustCreate(t, s, "A", rootID, "a")
	if err := s.Create("A", rootID, "a"); err == nil {
		t.Fatal("expected duplicate id error")
	}
	if err := s.Create("B", rootID, "a"); err == nil ||
		!strings.Contains(err.Error(), ErrConflict.Error()) {
		t.Fatalf("expected sibling name conflict, got %v", err)
	}
	if err := s.Create("C", "missing", "x"); err == nil {
		t.Fatal("expected missing parent error")
	}
	if err := s.Create("D", rootID, "a/b"); err == nil {
		t.Fatal("expected invalid name error")
	}
}
