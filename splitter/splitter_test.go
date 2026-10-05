package splitter_test

import (
	"errors"
	"path/filepath"
	"testing"

	"splitter/splitter"
)

// crash is panicked from fault hooks to simulate an abrupt process kill.
// The crashed Splitter instance is discarded and reopened afterwards.
type crash struct{ where string }

var errInjected = errors.New("injected fault")

type harness struct {
	t       *testing.T
	dir     string
	online  *splitter.FaultAdapter
	archive *splitter.FaultAdapter
	hooks   *splitter.Hooks
	sp      *splitter.Splitter
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	h := &harness{t: t, dir: dir, hooks: &splitter.Hooks{}}
	h.online = &splitter.FaultAdapter{Inner: mustAdapter(t, filepath.Join(dir, "online"))}
	h.archive = &splitter.FaultAdapter{Inner: mustAdapter(t, filepath.Join(dir, "archive"))}
	h.reopen()
	return h
}

func mustAdapter(t *testing.T, dir string) *splitter.FileAdapter {
	t.Helper()
	st, err := splitter.NewFileAdapter(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// reopen simulates a process restart over the same on-disk state.
func (h *harness) reopen() {
	h.t.Helper()
	sp, err := splitter.Open(splitter.Config{
		Dir: h.dir,
		Stores: map[splitter.Location]splitter.BlobStore{
			splitter.LocationOnline:  h.online,
			splitter.LocationArchive: h.archive,
		},
		Hooks: h.hooks,
	})
	if err != nil {
		h.t.Fatalf("open: %v", err)
	}
	h.sp = sp
}

func ev(key string, version uint64, content string, deleted bool) splitter.Event {
	return splitter.Event{Key: key, Version: version, Content: []byte(content), IsDeleted: deleted}
}

func (h *harness) apply(e splitter.Event) splitter.ApplyResult {
	h.t.Helper()
	res, err := h.sp.Apply(e)
	if err != nil {
		h.t.Fatalf("apply %+v: %v", e, err)
	}
	return res
}

// applyCrash applies e expecting a simulated crash from a fault hook.
func (h *harness) applyCrash(e splitter.Event) {
	h.t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			h.t.Fatal("expected simulated crash, apply returned normally")
		}
		if _, ok := r.(crash); !ok {
			panic(r)
		}
	}()
	_, _ = h.sp.Apply(e)
}

func (h *harness) mustGet(key string) *splitter.Record {
	h.t.Helper()
	rec, err := h.sp.Get(key)
	if err != nil {
		h.t.Fatalf("get %q: %v", key, err)
	}
	return rec
}

func (h *harness) mustBlobs(loc *splitter.FaultAdapter) []string {
	h.t.Helper()
	names, err := loc.Inner.List()
	if err != nil {
		h.t.Fatal(err)
	}
	return names
}

func (h *harness) assertFinal(key string, version uint64, content string, loc splitter.Location, deleted bool) {
	h.t.Helper()
	rec := h.mustGet(key)
	if rec.Pointer.Version != version || string(rec.Content) != content ||
		rec.Pointer.Location != loc || rec.Pointer.Deleted != deleted {
		h.t.Fatalf("final state mismatch: got %+v content=%q, want v%d %q at %s deleted=%v",
			rec.Pointer, rec.Content, version, content, loc, deleted)
	}
}

func TestRoutingAndScan(t *testing.T) {
	h := newHarness(t)
	if res := h.apply(ev("a", 1, "live", false)); res != splitter.ResultApplied {
		t.Fatalf("got %v", res)
	}
	if res := h.apply(ev("b", 1, "gone", true)); res != splitter.ResultApplied {
		t.Fatalf("got %v", res)
	}
	h.assertFinal("a", 1, "live", splitter.LocationOnline, false)
	h.assertFinal("b", 1, "gone", splitter.LocationArchive, true)
	if got := h.mustBlobs(h.online); len(got) != 1 {
		t.Fatalf("online blobs: %v", got)
	}
	if got := h.mustBlobs(h.archive); len(got) != 1 {
		t.Fatalf("archive blobs: %v", got)
	}
	ptrs := h.sp.ScanAll()
	if len(ptrs) != 2 || ptrs[0].Key != "a" || ptrs[1].Key != "b" {
		t.Fatalf("scan: %+v", ptrs)
	}
}

func TestVersionRules(t *testing.T) {
	h := newHarness(t)
	h.apply(ev("k", 5, "v5", false))
	if res := h.apply(ev("k", 4, "v4", false)); res != splitter.ResultStale {
		t.Fatalf("lower version: got %v", res)
	}
	if res := h.apply(ev("k", 5, "v5", false)); res != splitter.ResultIdempotent {
		t.Fatalf("same version+content: got %v", res)
	}
	if _, err := h.sp.Apply(ev("k", 5, "DIFFERENT", false)); !errors.Is(err, splitter.ErrConflict) {
		t.Fatalf("same version different content: got %v", err)
	}
	if _, err := h.sp.Apply(ev("k", 5, "v5", true)); !errors.Is(err, splitter.ErrConflict) {
		t.Fatalf("same version different deleted flag: got %v", err)
	}
	h.assertFinal("k", 5, "v5", splitter.LocationOnline, false)
}

func TestDeleteMovesToArchiveAndRestore(t *testing.T) {
	h := newHarness(t)
	h.apply(ev("k", 1, "one", false))
	h.apply(ev("k", 2, "two", true)) // soft delete -> archive
	h.assertFinal("k", 2, "two", splitter.LocationArchive, true)
	if got := h.mustBlobs(h.online); len(got) != 0 {
		t.Fatalf("old online copy not cleaned: %v", got)
	}
	h.apply(ev("k", 3, "three", false)) // restore -> online
	h.assertFinal("k", 3, "three", splitter.LocationOnline, false)
	if got := h.mustBlobs(h.archive); len(got) != 0 {
		t.Fatalf("old archive copy not cleaned: %v", got)
	}
}

func TestDelayedDeleteDoesNotResurrect(t *testing.T) {
	h := newHarness(t)
	h.apply(ev("k", 1, "one", false))
	h.apply(ev("k", 2, "two", true))    // deleted -> archive
	h.apply(ev("k", 3, "three", false)) // restored -> online
	// A delayed duplicate of the old delete arrives out of order.
	if res := h.apply(ev("k", 2, "two", true)); res != splitter.ResultStale {
		t.Fatalf("delayed delete: got %v", res)
	}
	if res := h.apply(ev("k", 1, "one", false)); res != splitter.ResultStale {
		t.Fatalf("delayed put: got %v", res)
	}
	h.assertFinal("k", 3, "three", splitter.LocationOnline, false)
	if got := h.mustBlobs(h.archive); len(got) != 0 {
		t.Fatalf("archive resurrected: %v", got)
	}
}

// Crash after the target write succeeded but before the pointer commit
// (ack lost / process died): recovery must finish the migration.
func TestCrashAfterBlobWriteBeforeCommit(t *testing.T) {
	h := newHarness(t)
	h.apply(ev("k", 1, "one", false))
	armed := true
	h.online.Hook = func(op, phase, name string) error {
		if armed && op == "put" && phase == "after" {
			panic(crash{"after blob write"})
		}
		return nil
	}
	h.applyCrash(ev("k", 2, "two", false))
	armed = false
	h.online.Hook = nil
	h.reopen() // recovery runs on open
	h.assertFinal("k", 2, "two", splitter.LocationOnline, false)
	// Replaying the same event after recovery is an idempotent no-op.
	if res := h.apply(ev("k", 2, "two", false)); res != splitter.ResultIdempotent {
		t.Fatalf("replay after recovery: got %v", res)
	}
}

// Crash after the pointer commit but before the journal catches up:
// both copies may exist; recovery must converge without double-applying.
func TestCrashAfterPointerCommit(t *testing.T) {
	h := newHarness(t)
	h.apply(ev("k", 1, "one", false))
	armed := true
	h.hooks.AfterPointerCommit = func() {
		if armed {
			panic(crash{"after pointer commit"})
		}
	}
	h.applyCrash(ev("k", 2, "two", true)) // move online -> archive
	armed = false
	h.hooks.AfterPointerCommit = nil
	h.reopen()
	h.assertFinal("k", 2, "two", splitter.LocationArchive, true)
	if got := h.mustBlobs(h.online); len(got) != 0 {
		t.Fatalf("stale online copy survived recovery: %v", got)
	}
}

// Crash with only the new side written and the old side not yet deleted.
func TestCrashBeforeCleanup(t *testing.T) {
	h := newHarness(t)
	h.apply(ev("k", 1, "one", false))
	armed := true
	h.hooks.BeforeCleanup = func() {
		if armed {
			panic(crash{"before cleanup"})
		}
	}
	h.applyCrash(ev("k", 2, "two", true))
	// Both copies exist on disk right after the crash.
	if got := h.mustBlobs(h.online); len(got) != 1 {
		t.Fatalf("expected old online copy before recovery: %v", got)
	}
	if got := h.mustBlobs(h.archive); len(got) != 1 {
		t.Fatalf("expected new archive copy before recovery: %v", got)
	}
	armed = false
	h.hooks.BeforeCleanup = nil
	h.reopen()
	h.assertFinal("k", 2, "two", splitter.LocationArchive, true)
	if got := h.mustBlobs(h.online); len(got) != 0 {
		t.Fatalf("cleanup not finished by recovery: %v", got)
	}
}

// Target write succeeded but the acknowledgement was lost: the caller
// retries the whole event and must reach the same final state.
func TestAckLostThenRetry(t *testing.T) {
	h := newHarness(t)
	failed := false
	h.online.Hook = func(op, phase, name string) error {
		if op == "put" && phase == "after" && !failed {
			failed = true
			return errInjected // write durable, ack lost
		}
		return nil
	}
	if _, err := h.sp.Apply(ev("k", 1, "one", false)); !errors.Is(err, errInjected) {
		t.Fatalf("first attempt: got %v", err)
	}
	h.online.Hook = nil
	if res := h.apply(ev("k", 1, "one", false)); res != splitter.ResultApplied {
		t.Fatalf("retry: got %v", res)
	}
	h.assertFinal("k", 1, "one", splitter.LocationOnline, false)
	if got := h.mustBlobs(h.online); len(got) != 1 {
		t.Fatalf("duplicate blobs after retry: %v", got)
	}
}

// Recovery itself must be idempotent: repeated restarts converge.
func TestDuplicateRecovery(t *testing.T) {
	h := newHarness(t)
	h.apply(ev("k", 1, "one", false))
	armed := true
	h.hooks.AfterPointerCommit = func() {
		if armed {
			panic(crash{"after pointer commit"})
		}
	}
	h.applyCrash(ev("k", 2, "two", true))
	armed = false
	h.hooks.AfterPointerCommit = nil
	h.reopen()
	if err := h.sp.Recover(); err != nil { // explicit extra recovery pass
		t.Fatal(err)
	}
	h.reopen() // and another full restart
	h.reopen()
	h.assertFinal("k", 2, "two", splitter.LocationArchive, true)
	if got := h.mustBlobs(h.online); len(got) != 0 {
		t.Fatalf("online not clean: %v", got)
	}
	if got := h.mustBlobs(h.archive); len(got) != 1 {
		t.Fatalf("archive: %v", got)
	}
}

// GC removes only copies no pointer and no active snapshot references.
func TestGCRespectsPointersAndSnapshots(t *testing.T) {
	h := newHarness(t)
	h.apply(ev("a", 1, "a1", false))
	h.apply(ev("a", 2, "a2", false)) // a@1 becomes garbage in the same target
	snapID, err := h.sp.BeginSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	h.apply(ev("a", 3, "a3", true)) // move to archive; a@2 pinned by snapshot
	removed, err := h.sp.GC()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "online/a@1" {
		t.Fatalf("gc with snapshot: removed %v", removed)
	}
	if got := h.mustBlobs(h.online); len(got) != 1 || got[0] != "a@2" {
		t.Fatalf("snapshot-pinned copy collected: %v", got)
	}
	if err := h.sp.EndSnapshot(snapID); err != nil {
		t.Fatal(err)
	}
	removed, err = h.sp.GC()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "online/a@2" {
		t.Fatalf("gc after snapshot release: removed %v", removed)
	}
	h.assertFinal("a", 3, "a3", splitter.LocationArchive, true)
}

// The pointer-based scan and the reference full scan must agree on the
// highest version per key, even across crashes and restarts.
func TestReferenceScanConsistency(t *testing.T) {
	h := newHarness(t)
	h.apply(ev("a", 1, "a1", false))
	h.apply(ev("a", 2, "a2", true))
	h.apply(ev("b", 1, "b1", false))
	armed := true
	h.hooks.BeforeCleanup = func() {
		if armed {
			panic(crash{"before cleanup"})
		}
	}
	h.applyCrash(ev("b", 2, "b2", true))
	armed = false
	h.hooks.BeforeCleanup = nil
	h.reopen()
	h.apply(ev("a", 3, "a3", false))

	ref, err := h.sp.ReferenceScan()
	if err != nil {
		t.Fatal(err)
	}
	ptrs := h.sp.ScanAll()
	if len(ptrs) != len(ref) {
		t.Fatalf("pointer scan %d keys vs reference %d", len(ptrs), len(ref))
	}
	for _, p := range ptrs {
		r, ok := ref[p.Key]
		if !ok {
			t.Fatalf("key %q missing from reference scan", p.Key)
		}
		if r.Version != p.Version || r.Location != p.Location {
			t.Fatalf("key %q: pointer=%+v reference=%+v", p.Key, p, r)
		}
	}
}
