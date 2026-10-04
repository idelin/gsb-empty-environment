package splitter

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
)

// errCrash is panicked by crash hooks to simulate an abrupt process exit.
type crashSim struct{}

func mustCrash(fn func()) (crashed bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(crashSim); ok {
				crashed = true
				return
			}
			panic(r)
		}
	}()
	fn()
	return false
}

func ev(key string, version uint64, content string, deleted bool) Event {
	return Event{Key: key, Version: version, Content: content, IsDeleted: deleted}
}

func open(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func mustGet(t *testing.T, s *Store, key string) Record {
	t.Helper()
	rec, err := s.Get(key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	return rec
}

func TestBasicRouting(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)

	if err := s.Apply(ev("a", 1, "hello", false)); err != nil {
		t.Fatal(err)
	}
	if rec := mustGet(t, s, "a"); rec.Content != "hello" {
		t.Fatalf("got %q", rec.Content)
	}
	if p, _ := s.Pointer("a"); p.Location != Online {
		t.Fatalf("live version must be online, got %s", p.Location)
	}

	if err := s.Apply(ev("a", 2, "hello", true)); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Pointer("a"); p.Location != Archive {
		t.Fatalf("deleted version must be archived, got %s", p.Location)
	}
	rec := mustGet(t, s, "a")
	if !rec.IsDeleted || rec.Version != 2 {
		t.Fatalf("pointer view = %+v", rec)
	}

	// Restore: back online at v3.
	if err := s.Apply(ev("a", 3, "hello again", false)); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Pointer("a"); p.Location != Online || p.Version != 3 {
		t.Fatalf("restore pointer = %+v", p)
	}
}

func TestIdempotentAndConflict(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)

	if err := s.Apply(ev("k", 1, "v1", false)); err != nil {
		t.Fatal(err)
	}
	// Same version, same content: idempotent no-op.
	if err := s.Apply(ev("k", 1, "v1", false)); err != nil {
		t.Fatalf("idempotent replay must succeed: %v", err)
	}
	// Same version, different content: conflict.
	if err := s.Apply(ev("k", 1, "OTHER", false)); err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	// Same version, same content but flipped delete flag: conflict.
	if err := s.Apply(ev("k", 1, "v1", true)); err != ErrConflict {
		t.Fatalf("want ErrConflict on delete-flag flip, got %v", err)
	}
	// Lower version: stale.
	if err := s.Apply(ev("k", 0, "old", false)); err != ErrStale {
		t.Fatalf("want ErrStale, got %v", err)
	}
	if rec := mustGet(t, s, "k"); rec.Content != "v1" {
		t.Fatalf("state changed by rejected events: %+v", rec)
	}
}

// Crash after the intent is journaled but before the target write.
func TestCrashAfterIntent(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	s.Hooks.AfterIntentPersisted = func() { panic(crashSim{}) }
	if !mustCrash(func() { s.Apply(ev("a", 1, "x", false)) }) {
		t.Fatal("expected crash")
	}

	s2 := open(t, dir) // Recover runs inside Open
	rec := mustGet(t, s2, "a")
	if rec.Version != 1 || rec.Content != "x" {
		t.Fatalf("recovery must roll intent forward, got %+v", rec)
	}
	if p, _ := s2.Pointer("a"); p.Location != Online {
		t.Fatalf("pointer location = %s", p.Location)
	}
}

// Target write durable but acknowledgement lost: Apply reports failure,
// yet recovery must confirm the write and commit the pointer.
func TestAckLost(t *testing.T) {
	dir := t.TempDir()
	online := &FaultTarget{Inner: &FileTarget{Dir: filepath.Join(dir, "online")}}
	archive := &FileTarget{Dir: filepath.Join(dir, "archive")}
	armed := true
	online.Hooks.AfterPutBeforeAck = func(rec Record) error {
		if armed {
			armed = false
			return fmt.Errorf("simulated lost ack")
		}
		return nil
	}
	s, err := NewStore(dir, online, archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(ev("a", 1, "payload", false)); err == nil {
		t.Fatal("expected ack-lost error")
	}

	s2 := open(t, dir)
	rec := mustGet(t, s2, "a")
	if rec.Content != "payload" || rec.Version != 1 {
		t.Fatalf("ack-lost write must be confirmed by recovery, got %+v", rec)
	}
}

// Crash after the destination write, before the pointer commit: the copy
// exists on one side only; recovery must commit the pointer to it.
func TestCrashBeforePointerCommit(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := s.Apply(ev("a", 1, "v1", false)); err != nil {
		t.Fatal(err)
	}
	s.Hooks.AfterTargetWrite = func() { panic(crashSim{}) }
	if !mustCrash(func() { s.Apply(ev("a", 2, "v1", true)) }) {
		t.Fatal("expected crash")
	}

	s2 := open(t, dir)
	p, _ := s2.Pointer("a")
	if p.Version != 2 || p.Location != Archive {
		t.Fatalf("recovery must commit pointer to archive v2, got %+v", p)
	}
	if rec := mustGet(t, s2, "a"); !rec.IsDeleted {
		t.Fatalf("deleted record expected, got %+v", rec)
	}
}

// Crash after pointer commit but before the journal entry closes:
// recovery must recognize the committed pointer and not redo anything.
func TestCrashAfterPointerCommit(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	s.Hooks.AfterPointerCommitted = func() { panic(crashSim{}) }
	if !mustCrash(func() { s.Apply(ev("a", 1, "x", false)) }) {
		t.Fatal("expected crash")
	}
	s2 := open(t, dir)
	if rec := mustGet(t, s2, "a"); rec.Version != 1 {
		t.Fatalf("got %+v", rec)
	}
	// Journal must be clean after recovery.
	entries, err := s2.journal.load()
	if err != nil || len(entries) != 0 {
		t.Fatalf("journal not drained: %v %v", entries, err)
	}
}

// Recovery itself may be interrupted and re-run any number of times.
func TestDuplicateRecovery(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	s.Hooks.AfterTargetWrite = func() { panic(crashSim{}) }
	mustCrash(func() { s.Apply(ev("a", 1, "x", false)) })

	for i := 0; i < 3; i++ {
		s2 := open(t, dir)
		if err := s2.Recover(); err != nil {
			t.Fatalf("recover run %d: %v", i, err)
		}
		if rec := mustGet(t, s2, "a"); rec.Version != 1 || rec.Content != "x" {
			t.Fatalf("run %d: got %+v", i, rec)
		}
	}
}

// A delayed delete must never resurrect an old archived copy after a restore.
func TestDelayedDeleteNoResurrect(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	must := func(e Event) {
		t.Helper()
		if err := s.Apply(e); err != nil {
			t.Fatalf("apply %+v: %v", e, err)
		}
	}
	must(ev("a", 1, "live", false))
	must(ev("a", 2, "live", true))  // delete -> archive
	must(ev("a", 3, "back", false)) // restore -> online
	// The v2 delete is re-delivered late (out-of-order correction).
	if err := s.Apply(ev("a", 2, "live", true)); err != ErrStale {
		t.Fatalf("delayed delete must be rejected as stale, got %v", err)
	}
	p, _ := s.Pointer("a")
	if p.Version != 3 || p.Location != Online {
		t.Fatalf("pointer regressed: %+v", p)
	}
	if rec := mustGet(t, s, "a"); rec.Content != "back" || rec.IsDeleted {
		t.Fatalf("resurrected archive: %+v", rec)
	}
	// Restart and re-deliver again: still no resurrection.
	s2 := open(t, dir)
	if err := s2.Apply(ev("a", 2, "live", true)); err != ErrStale {
		t.Fatalf("post-restart delayed delete must be stale, got %v", err)
	}
	if rec := mustGet(t, s2, "a"); rec.Content != "back" {
		t.Fatalf("post-restart view = %+v", rec)
	}
}

// Duplicate restore (same version re-applied) is an idempotent no-op.
func TestDuplicateRestore(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	for _, e := range []Event{
		ev("a", 1, "x", false),
		ev("a", 2, "x", true),
		ev("a", 3, "y", false),
	} {
		if err := s.Apply(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Apply(ev("a", 3, "y", false)); err != nil {
		t.Fatalf("duplicate restore must be idempotent: %v", err)
	}
	if rec := mustGet(t, s, "a"); rec.Version != 3 || rec.Content != "y" {
		t.Fatalf("got %+v", rec)
	}
}

// GC removes only copies referenced by neither a pointer nor a snapshot.
func TestGCRespectsPointersAndSnapshots(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	must := func(e Event) {
		t.Helper()
		if err := s.Apply(e); err != nil {
			t.Fatalf("apply %+v: %v", e, err)
		}
	}
	must(ev("a", 1, "v1", false))
	must(ev("a", 2, "v2", false))

	snapID, err := s.BeginSnapshot() // pins a@2 online
	if err != nil {
		t.Fatal(err)
	}
	must(ev("a", 3, "v3", false))
	must(ev("a", 4, "v3", true)) // delete -> archive; online a@3 now orphaned

	removed, err := s.GC()
	if err != nil {
		t.Fatal(err)
	}
	// a@1 (superseded, unpinned) and online a@3 (superseded by archive a@4)
	// are collectible; a@2 is pinned by the snapshot; archive a@4 is pointed.
	assertAbsent := func(key string, ver uint64, loc Location) {
		t.Helper()
		if _, err := s.target(loc).Get(key, ver); err != ErrNotFound {
			t.Fatalf("%s@%d in %s should be gone", key, ver, loc)
		}
	}
	assertPresent := func(key string, ver uint64, loc Location) {
		t.Helper()
		if _, err := s.target(loc).Get(key, ver); err != nil {
			t.Fatalf("%s@%d in %s should survive: %v", key, ver, loc, err)
		}
	}
	_ = removed
	assertAbsent("a", 1, Online)
	assertAbsent("a", 3, Online)
	assertPresent("a", 2, Online)  // snapshot pin
	assertPresent("a", 4, Archive) // current pointer

	if rec := mustGet(t, s, "a"); rec.Version != 4 || !rec.IsDeleted {
		t.Fatalf("GC changed the read view: %+v", rec)
	}

	// Release the snapshot: a@2 becomes collectible.
	if err := s.ReleaseSnapshot(snapID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GC(); err != nil {
		t.Fatal(err)
	}
	assertAbsent("a", 2, Online)
	assertPresent("a", 4, Archive)
}

// Randomized cross-check: after every batch (with random restarts), the
// committed pointer view must equal the full-scan highest-version reference.
func TestPointerViewMatchesScanReference(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(42))
	keys := []string{"a", "b", "c", "d"}
	versions := map[string]uint64{}
	deletedFlags := map[string]map[uint64]bool{}
	s := open(t, dir)

	for round := 0; round < 400; round++ {
		key := keys[rng.Intn(len(keys))]
		// Mostly the next version; sometimes an out-of-order/duplicate one.
		v := versions[key] + 1
		if rng.Intn(4) == 0 && versions[key] > 0 {
			v = uint64(rng.Intn(int(versions[key]))) + 1
		}
		deleted := rng.Intn(5) == 0
		if v <= versions[key] {
			deleted = deletedFlags[key][v] // replay the committed event faithfully
		}
		content := fmt.Sprintf("content-%s-%d", key, v)
		err := s.Apply(ev(key, v, content, deleted))
		if v <= versions[key] {
			// Replay of the committed tip is idempotent; anything older is stale.
			if v == versions[key] && err != nil {
				t.Fatalf("tip replay: %v", err)
			}
			if v < versions[key] && err != ErrStale {
				t.Fatalf("older replay: want stale, got %v", err)
			}
		} else {
			if err != nil {
				t.Fatalf("apply %s@%d: %v", key, v, err)
			}
			versions[key] = v
			if deletedFlags[key] == nil {
				deletedFlags[key] = map[uint64]bool{}
			}
			deletedFlags[key][v] = deleted
		}

		if round%50 == 49 {
			s = open(t, dir) // restart
		}
		if round%25 == 24 {
			if _, err := s.GC(); err != nil {
				t.Fatal(err)
			}
		}
	}

	s = open(t, dir) // final restart
	view, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	ref, err := ScanReference(s.Online, s.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(view) != len(ref) {
		t.Fatalf("view has %d keys, reference %d", len(view), len(ref))
	}
	for _, rec := range view {
		r := ref[rec.Key]
		if r.Version != rec.Version || r.Hash != rec.Hash || r.IsDeleted != rec.IsDeleted {
			t.Fatalf("key %s: pointer view %+v != reference %+v", rec.Key, rec, r)
		}
	}
}

// One-sided state after a crash: the delete landed in the archive but the
// online copy was never cleaned. The pointer must still expose exactly one
// location, and GC (not the commit path) reclaims the other side.
func TestOneSidedWritePointerIsSingleSource(t *testing.T) {
	dir := t.TempDir()
	s := open(t, dir)
	if err := s.Apply(ev("a", 1, "x", false)); err != nil {
		t.Fatal(err)
	}
	s.Hooks.AfterTargetWrite = func() { panic(crashSim{}) }
	mustCrash(func() { s.Apply(ev("a", 2, "x", true)) })

	s2 := open(t, dir)
	// Both sides physically hold a copy now (online a@1, archive a@2).
	if _, err := s2.Online.Get("a", 1); err != nil {
		t.Fatalf("online a@1 should still exist pre-GC: %v", err)
	}
	if _, err := s2.Archive.Get("a", 2); err != nil {
		t.Fatalf("archive a@2 missing: %v", err)
	}
	// The read view sees only the confirmed location.
	rec := mustGet(t, s2, "a")
	if rec.Version != 2 || !rec.IsDeleted {
		t.Fatalf("pointer view = %+v", rec)
	}
	if _, err := s2.GC(); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Online.Get("a", 1); err != ErrNotFound {
		t.Fatalf("GC must reclaim unreferenced online copy")
	}
	if rec := mustGet(t, s2, "a"); rec.Version != 2 {
		t.Fatalf("post-GC view = %+v", rec)
	}
}
