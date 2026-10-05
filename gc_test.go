package blockgc

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
)

type fixture struct {
	dir      string
	store    *Store
	manifest *Manifest
	gc       *Collector
}

func newFixture(t *testing.T) *fixture {
	dir, err := ioutil.TempDir("", "blockgc-test-")
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := OpenManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{
		dir:      dir,
		store:    store,
		manifest: manifest,
		gc:       NewCollector(store, manifest),
	}
}

func (f *fixture) close() { os.RemoveAll(f.dir) }

func (f *fixture) put(t *testing.T, id string, refs ...string) {
	t.Helper()
	if err := f.store.Put(Block{ID: id, Refs: refs}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) collect(t *testing.T) Stats {
	t.Helper()
	st, err := f.gc.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (f *fixture) assertLive(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := f.store.Get(id); err != nil {
			t.Errorf("block %q: want live, got %v", id, err)
		}
	}
}

func (f *fixture) assertGone(t *testing.T, ids ...string) {
	t.Helper()
	live, err := f.store.List()
	if err != nil {
		t.Fatal(err)
	}
	quar, err := f.store.ListQuarantined()
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, id := range append(live, quar...) {
		present[id] = true
	}
	for _, id := range ids {
		if present[id] {
			t.Errorf("block %q: want deleted, still present", id)
		}
	}
}

// Shared blocks: a block referenced from two roots must survive revocation
// of one of the roots.
func TestSharedBlocksSurvivePartialRevocation(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	// r1 -> a -> {shared, onlyA}; r2 -> b -> shared
	f.put(t, "r1", "a")
	f.put(t, "a", "shared", "onlyA")
	f.put(t, "r2", "b")
	f.put(t, "b", "shared")
	f.put(t, "shared")
	f.put(t, "onlyA")

	if _, err := f.manifest.Publish([]string{"r1", "r2"}); err != nil {
		t.Fatal(err)
	}
	// Revoke r2: shared stays reachable via r1, b becomes garbage.
	if _, err := f.manifest.Publish([]string{"r1"}); err != nil {
		t.Fatal(err)
	}

	st := f.collect(t)
	if st.Deleted != 2 {
		t.Errorf("want 2 deletions (r2, b), got %+v", st)
	}
	f.assertLive(t, "r1", "a", "shared", "onlyA")
	f.assertGone(t, "r2", "b")
}

// Cyclic and dangling ("bad") references: a reachable cycle must be kept,
// an unreachable cycle collected, and dangling refs must not crash the GC.
func TestCyclicAndBadReferences(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	// Reachable cycle: root -> x <-> y
	f.put(t, "root", "x")
	f.put(t, "x", "y")
	f.put(t, "y", "x")
	// Unreachable cycle: p <-> q
	f.put(t, "p", "q")
	f.put(t, "q", "p")
	// Dangling refs to blocks that never existed.
	f.put(t, "root2", "ghost1", "ghost2")

	if _, err := f.manifest.Publish([]string{"root", "root2"}); err != nil {
		t.Fatal(err)
	}

	st := f.collect(t)
	if st.Deleted != 2 {
		t.Errorf("want 2 deletions (p, q), got %+v", st)
	}
	f.assertLive(t, "root", "root2", "x", "y")
	f.assertGone(t, "p", "q")
}

// Root revocation: removing a root from the manifest makes its whole
// subgraph collectable.
func TestRootRevocation(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	f.put(t, "root", "child")
	f.put(t, "child", "leaf")
	f.put(t, "leaf")

	if _, err := f.manifest.Publish([]string{"root"}); err != nil {
		t.Fatal(err)
	}
	if st := f.collect(t); st.Deleted != 0 {
		t.Fatalf("reachable blocks deleted: %+v", st)
	}

	// Revoke everything.
	if _, err := f.manifest.Publish(nil); err != nil {
		t.Fatal(err)
	}
	st := f.collect(t)
	if st.Deleted != 3 {
		t.Errorf("want 3 deletions, got %+v", st)
	}
	f.assertGone(t, "root", "child", "leaf")
}

// Readers holding an old root: a pinned old manifest version protects roots
// that the current version has already revoked.
func TestReaderHoldingOldRoot(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	f.put(t, "oldRoot", "oldData")
	f.put(t, "oldData")
	if _, err := f.manifest.Publish([]string{"oldRoot"}); err != nil {
		t.Fatal(err)
	}

	// A reader pins v1, then the writer moves on to v2 without oldRoot.
	f.manifest.Pin("reader-1")
	f.put(t, "newRoot")
	if _, err := f.manifest.Publish([]string{"newRoot"}); err != nil {
		t.Fatal(err)
	}

	st := f.collect(t)
	if st.Deleted != 0 {
		t.Errorf("pinned roots must survive, got %+v", st)
	}
	f.assertLive(t, "oldRoot", "oldData", "newRoot")

	// Reader leaves; now the old subgraph is collectable.
	f.manifest.Unpin("reader-1")
	st = f.collect(t)
	if st.Deleted != 2 {
		t.Errorf("want 2 deletions after unpin, got %+v", st)
	}
	f.assertLive(t, "newRoot")
	f.assertGone(t, "oldRoot", "oldData")
}

// New root published during the mark phase must be picked up before sweep.
func TestRootPublishedDuringMark(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	f.put(t, "lateRoot", "lateData")
	f.put(t, "lateData")
	if _, err := f.manifest.Publish(nil); err != nil {
		t.Fatal(err)
	}

	f.gc.Hooks.AfterMarkPass = func(pass int) {
		if pass == 0 {
			if _, err := f.manifest.Publish([]string{"lateRoot"}); err != nil {
				t.Error(err)
			}
		}
	}

	st := f.collect(t)
	if st.Deleted != 0 || st.Quarantined != 0 {
		t.Errorf("late root's blocks must not be swept, got %+v", st)
	}
	f.assertLive(t, "lateRoot", "lateData")
}

// Reference added after the scan: a block quarantined as garbage becomes
// reachable via a brand-new root before deletion. The independent
// verification traversal must restore it.
func TestReferenceAddedAfterScan(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	f.put(t, "orphan", "orphanChild")
	f.put(t, "orphanChild")
	f.put(t, "realGarbage")
	if _, err := f.manifest.Publish(nil); err != nil {
		t.Fatal(err)
	}

	f.gc.Hooks.AfterQuarantine = func(ids []string) {
		// Concurrent writer adopts the orphan after the scan completed.
		f.put(t, "adopter", "orphan")
		if _, err := f.manifest.Publish([]string{"adopter"}); err != nil {
			t.Error(err)
		}
	}

	st := f.collect(t)
	if st.Restored != 2 {
		t.Errorf("want 2 restorations (orphan, orphanChild), got %+v", st)
	}
	if st.Deleted != 1 {
		t.Errorf("want 1 deletion (realGarbage), got %+v", st)
	}
	f.assertLive(t, "adopter", "orphan", "orphanChild")
	f.assertGone(t, "realGarbage")
}

// Crash recovery: blocks left in quarantine in various journal states must
// be reconciled correctly by Recover.
func TestCrashRecovery(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	f.put(t, "root", "keep")
	f.put(t, "keep")
	f.put(t, "confirmedGarbage")   // quarantined + confirmed, crash before unlink
	f.put(t, "unconfirmedGarbage") // quarantined, crash before confirm
	f.put(t, "rescued")            // quarantined, but reachable again now
	if _, err := f.manifest.Publish([]string{"root"}); err != nil {
		t.Fatal(err)
	}

	// Simulate the crash: quarantine blocks with partial journal state.
	epoch, err := f.store.nextEpoch()
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.store.openJournal(epoch)
	if err != nil {
		t.Fatal(err)
	}
	crash := func(id, lastOp string) {
		if err := f.store.Quarantine(id); err != nil {
			t.Fatal(err)
		}
		if err := j.append(epoch, id, "quarantine"); err != nil {
			t.Fatal(err)
		}
		if lastOp == "confirm" {
			if err := j.append(epoch, id, "confirm"); err != nil {
				t.Fatal(err)
			}
		}
	}
	crash("confirmedGarbage", "confirm")
	crash("unconfirmedGarbage", "quarantine")
	crash("rescued", "quarantine")
	j.close()

	// While the GC was down, a new root adopted "rescued".
	f.put(t, "adopter", "rescued")
	if _, err := f.manifest.Publish([]string{"root", "adopter"}); err != nil {
		t.Fatal(err)
	}

	st, err := f.gc.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Deleted != 2 || st.Restored != 1 {
		t.Errorf("want 2 deletions + 1 restoration, got %+v", st)
	}
	f.assertLive(t, "root", "keep", "adopter", "rescued")
	f.assertGone(t, "confirmedGarbage", "unconfirmedGarbage")

	// Recover is idempotent.
	st, err = f.gc.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st != (Stats{}) {
		t.Errorf("second Recover should be a no-op, got %+v", st)
	}
}

// Crash between the quarantine rename and its journal record: the block is
// in quarantine with no record at all. Recover must re-verify rather than
// blindly delete.
func TestCrashBetweenRenameAndJournal(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	f.put(t, "root", "victim")
	f.put(t, "victim")
	if _, err := f.manifest.Publish([]string{"root"}); err != nil {
		t.Fatal(err)
	}

	// Torn quarantine: renamed, never journaled.
	if err := f.store.Quarantine("victim"); err != nil {
		t.Fatal(err)
	}

	st, err := f.gc.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Restored != 1 || st.Deleted != 0 {
		t.Errorf("reachable block must be restored, got %+v", st)
	}
	f.assertLive(t, "root", "victim")
}

// Manifest survives a restart: roots published before a crash still protect
// their blocks afterwards.
func TestManifestPersistenceAcrossRestart(t *testing.T) {
	f := newFixture(t)
	defer f.close()

	f.put(t, "root", "data")
	f.put(t, "data")
	f.put(t, "garbage")
	if _, err := f.manifest.Publish([]string{"root"}); err != nil {
		t.Fatal(err)
	}

	// Reopen manifest from disk, as after a process restart.
	manifest2, err := OpenManifest(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	gc2 := NewCollector(f.store, manifest2)
	st, err := gc2.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Deleted != 1 {
		t.Errorf("want 1 deletion, got %+v", st)
	}
	f.assertLive(t, "root", "data")
	f.assertGone(t, "garbage")
}
