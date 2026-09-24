package mvcc

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// manualClock is an injectable, test-controlled Clock.
type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newManualClock() *manualClock {
	return &manualClock{now: time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func mustCommit(t *testing.T, s *Store, ops ...Op) uint64 {
	t.Helper()
	v, err := s.Commit(ops...)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return v
}

func mustGet(t *testing.T, snap *Snapshot, keys ...string) map[string]string {
	t.Helper()
	got, err := snap.Get(keys...)
	if err != nil {
		t.Fatalf("snapshot get: %v", err)
	}
	return got
}

// A multi-key read inside one snapshot must be a single consistent
// view: observing old A together with new B is not allowed.
func TestMultiKeyConsistentSnapshot(t *testing.T) {
	s := NewStore(Options{})
	mustCommit(t, s, Put("A", "a1")) // v1
	mustCommit(t, s, Put("B", "b1")) // v2
	snap, err := s.Begin()           // pinned at v2
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer snap.Close()
	mustCommit(t, s, Put("A", "a2"), Put("B", "b2")) // v3

	got := mustGet(t, snap, "A", "B")
	if got["A"] != "a1" || got["B"] != "b1" {
		t.Fatalf("snapshot must see old A and old B, got %v", got)
	}
	if got["A"] == "a1" && got["B"] == "b2" {
		t.Fatal("old A with new B must never appear in one snapshot")
	}

	latest, err := s.GetLatest("A", "B")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest["A"] != "a2" || latest["B"] != "b2" {
		t.Fatalf("latest read must see new values, got %v", latest)
	}
}

// A batch commits atomically: all keys appear at the same version, and
// an injected failure leaves the store completely untouched.
func TestAtomicBatchCommit(t *testing.T) {
	s := NewStore(Options{})
	v := mustCommit(t, s, Put("x", "1"), Put("y", "1"), Put("z", "1"))
	got, err := s.GetLatest("x", "y", "z")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("batch must publish all keys together, got %v", got)
	}
	if s.Version() != v {
		t.Fatalf("version = %d, want %d", s.Version(), v)
	}

	// Inject a commit failure: nothing may be applied.
	boom := errors.New("injected commit failure")
	armed := false
	broken := NewStore(Options{Fail: func(op string) error {
		if armed && op == "commit" {
			return boom
		}
		return nil
	}})
	mustCommit(t, broken, Put("x", "1"))
	armed = true
	if _, err := broken.Commit(Put("x", "2"), Put("y", "2")); !errors.Is(err, boom) {
		t.Fatalf("commit error = %v, want %v", err, boom)
	}
	after, err := broken.GetLatest("x", "y")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if after["x"] != "1" || len(after) != 1 {
		t.Fatalf("failed commit must be all-or-nothing, got %v", after)
	}
	if broken.Version() != 1 {
		t.Fatalf("failed commit must not advance version, got %d", broken.Version())
	}
}

// A deleted key stays visible to snapshots pinned before the delete.
func TestDeleteVisibleInOldSnapshot(t *testing.T) {
	s := NewStore(Options{})
	mustCommit(t, s, Put("k", "v1")) // v1
	old, err := s.Begin()            // pinned at v1
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer old.Close()
	mustCommit(t, s, Del("k")) // v2 tombstone

	if got := mustGet(t, old, "k"); got["k"] != "v1" {
		t.Fatalf("old snapshot must still see deleted key, got %v", got)
	}
	latest, err := s.GetLatest("k")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if _, ok := latest["k"]; ok {
		t.Fatalf("latest read must not see deleted key, got %v", latest)
	}
	fresh, err := s.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer fresh.Close()
	if got := mustGet(t, fresh, "k"); len(got) != 0 {
		t.Fatalf("new snapshot must not see deleted key, got %v", got)
	}
}

// GC reclaims versions below the minimum active snapshot version, but
// keeps everything an active snapshot can still read.
func TestGCBoundedByMinActiveSnapshot(t *testing.T) {
	s := NewStore(Options{})
	mustCommit(t, s, Put("k", "v1")) // v1
	mustCommit(t, s, Put("k", "v2")) // v2
	snap, err := s.Begin()           // pins v2
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	mustCommit(t, s, Put("k", "v3")) // v3
	mustCommit(t, s, Put("k", "v4")) // v4

	if n := s.GC(); n != 1 {
		t.Fatalf("GC reclaimed %d versions, want 1 (only v1)", n)
	}
	if got := mustGet(t, snap, "k"); got["k"] != "v2" {
		t.Fatalf("snapshot pinned at v2 must still read v2, got %v", got)
	}

	// Releasing the snapshot moves the floor to the latest version.
	if err := snap.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if n := s.GC(); n != 2 {
		t.Fatalf("GC reclaimed %d versions, want 2 (v2, v3)", n)
	}
	latest, err := s.GetLatest("k")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest["k"] != "v4" {
		t.Fatalf("latest version must survive GC, got %v", latest)
	}
}

// Expiry policy: a snapshot that outlives SnapshotTTL fails reads with
// ErrSnapshotExpired, is released automatically, and stops pinning GC.
func TestSnapshotExpiryPolicy(t *testing.T) {
	clock := newManualClock()
	s := NewStore(Options{Now: clock.Now, SnapshotTTL: time.Minute})
	mustCommit(t, s, Put("k", "v1"))
	snap, err := s.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	mustCommit(t, s, Put("k", "v2"))

	clock.Advance(2 * time.Minute) // past the TTL

	if _, err := snap.Get("k"); !errors.Is(err, ErrSnapshotExpired) {
		t.Fatalf("expired snapshot get = %v, want ErrSnapshotExpired", err)
	}
	if got := s.ActiveSnapshots(); got != 0 {
		t.Fatalf("expired snapshot must be auto-released, active = %d", got)
	}
	// The expired snapshot no longer pins GC: v1 can be reclaimed.
	if n := s.GC(); n != 1 {
		t.Fatalf("GC reclaimed %d versions, want 1", n)
	}
	// Close after expiry stays harmless.
	if err := snap.Close(); err != nil {
		t.Fatalf("close after expiry: %v", err)
	}
}

// Resource release: every Begin must be matched by exactly one release,
// via Close or expiry, and Close is idempotent.
func TestResourceRelease(t *testing.T) {
	s := NewStore(Options{})
	if got := s.ActiveSnapshots(); got != 0 {
		t.Fatalf("active = %d, want 0", got)
	}
	const n = 3
	snaps := make([]*Snapshot, 0, n)
	for i := 0; i < n; i++ {
		snap, err := s.Begin()
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		snaps = append(snaps, snap)
	}
	if got := s.ActiveSnapshots(); got != n {
		t.Fatalf("active = %d, want %d", got, n)
	}
	for _, snap := range snaps {
		if err := snap.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	if got := s.ActiveSnapshots(); got != 0 {
		t.Fatalf("all snapshots closed, active = %d, want 0", got)
	}
	// Double close must not corrupt the registry.
	if err := snaps[0].Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := snaps[0].Get("k"); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatalf("closed snapshot get = %v, want ErrSnapshotClosed", err)
	}
	if got := s.ActiveSnapshots(); got != 0 {
		t.Fatalf("active = %d, want 0", got)
	}
}

// The Loader backs keys the store has never seen, without any remote
// cache in between.
func TestLoaderFallback(t *testing.T) {
	calls := 0
	s := NewStore(Options{Load: func(key string) (string, bool, error) {
		calls++
		if key == "ext" {
			return "from-source", true, nil
		}
		return "", false, nil
	}})
	got, err := s.GetLatest("ext", "missing")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got["ext"] != "from-source" {
		t.Fatalf("loader value missing, got %v", got)
	}
	if _, ok := got["missing"]; ok {
		t.Fatalf("unknown key must stay absent, got %v", got)
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2", calls)
	}
	// An in-memory version wins over the data source.
	mustCommit(t, s, Put("ext", "local"))
	got, err = s.GetLatest("ext")
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got["ext"] != "local" {
		t.Fatalf("committed version must override loader, got %v", got)
	}
}

// FailHook injection on reads and Begin surfaces the injected error.
func TestFailHookInjection(t *testing.T) {
	boom := errors.New("injected read failure")
	failGet := NewStore(Options{Fail: func(op string) error {
		if op == "get" {
			return boom
		}
		return nil
	}})
	if _, err := failGet.GetLatest("k"); !errors.Is(err, boom) {
		t.Fatalf("get latest = %v, want %v", err, boom)
	}
	snap, err := failGet.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer snap.Close()
	if _, err := snap.Get("k"); !errors.Is(err, boom) {
		t.Fatalf("snapshot get = %v, want %v", err, boom)
	}

	failBegin := NewStore(Options{Fail: func(op string) error {
		if op == "begin" {
			return boom
		}
		return nil
	}})
	if _, err := failBegin.Begin(); !errors.Is(err, boom) {
		t.Fatalf("begin = %v, want %v", err, boom)
	}
	if got := failBegin.ActiveSnapshots(); got != 0 {
		t.Fatalf("failed begin must not register a snapshot, active = %d", got)
	}
}

// Concurrent snapshots, commits, and GC must stay race-free and keep
// every snapshot internally consistent.
func TestConcurrentConsistency(t *testing.T) {
	s := NewStore(Options{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				v := fmt.Sprintf("w%d-%d", w, i)
				if _, err := s.Commit(Put("A", v), Put("B", v)); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				snap, err := s.Begin()
				if err != nil {
					t.Errorf("begin: %v", err)
					return
				}
				got, err := snap.Get("A", "B")
				if err != nil {
					t.Errorf("get: %v", err)
					return
				}
				if got["A"] != got["B"] {
					t.Errorf("inconsistent snapshot: %v", got)
				}
				if err := snap.Close(); err != nil {
					t.Errorf("close: %v", err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			s.GC()
		}
	}()
	wg.Wait()
	if got := s.ActiveSnapshots(); got != 0 {
		t.Fatalf("all snapshots released, active = %d, want 0", got)
	}
}
