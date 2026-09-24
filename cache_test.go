package leasecache

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// resource is a stand-in for a pooled object that must be returned.
type resource struct {
	id  int
	key string
}

// harness wires injectable loader/recycler/clock and records everything
// so tests can verify that every loaded resource is recycled exactly once.
type harness struct {
	mu         sync.Mutex
	nextID     int
	loadErr    error       // injected failure
	loadCalls  int         // data-source hits
	recycled   []int       // ids handed to the Recycler, in order
	recycleSet map[int]int // id -> times recycled (must stay 1)
	now        time.Time   // fake clock
}

func newHarness() *harness {
	return &harness{recycleSet: make(map[int]int), now: time.Unix(1_700_000_000, 0)}
}

func (h *harness) loader(key string) (*resource, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loadCalls++
	if h.loadErr != nil {
		return nil, h.loadErr
	}
	h.nextID++
	return &resource{id: h.nextID, key: key}, nil
}

func (h *harness) recycler(r *resource) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recycled = append(h.recycled, r.id)
	h.recycleSet[r.id]++
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

// assertBalanced verifies the core invariant: every loaded resource was
// recycled exactly once — nothing leaked, nothing double-freed.
func (h *harness) assertBalanced(t *testing.T, c *Cache[string, *resource]) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	s := c.Stats()
	if s.Loaded != s.Recycled {
		t.Fatalf("leak: loaded %d resources but recycled %d", s.Loaded, s.Recycled)
	}
	if s.Loaded != h.nextID {
		t.Fatalf("stats say %d loaded, loader produced %d", s.Loaded, h.nextID)
	}
	for id, n := range h.recycleSet {
		if n != 1 {
			t.Fatalf("resource %d recycled %d times, want exactly 1", id, n)
		}
	}
}

func TestAcquireSharesLiveEntry(t *testing.T) {
	h := newHarness()
	c := New[string, *resource](h.loader, h.recycler, Config{})

	l1, err := c.Acquire("a")
	if err != nil {
		t.Fatal(err)
	}
	l2, err := c.Acquire("a")
	if err != nil {
		t.Fatal(err)
	}
	if l1.Value() != l2.Value() {
		t.Fatal("two borrowers of a live entry must share the same object")
	}
	if h.loadCalls != 1 {
		t.Fatalf("loader called %d times, want 1 (cache hit)", h.loadCalls)
	}
	mustRelease(t, l1)
	mustRelease(t, l2)

	// Still cached: nothing recycled yet, next acquire reuses the object.
	if got := len(h.recycled); got != 0 {
		t.Fatalf("recycled %d resources while entry still cached", got)
	}
	l3, _ := c.Acquire("a")
	if l3.Value() != l1.Value() {
		t.Fatal("idle cached object should be reused")
	}
	mustRelease(t, l3)

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	h.assertBalanced(t, c)
}

func TestEvictKeepsBorrowersAndForbidsReuse(t *testing.T) {
	h := newHarness()
	c := New[string, *resource](h.loader, h.recycler, Config{})

	old, err := c.Acquire("k")
	if err != nil {
		t.Fatal(err)
	}
	c.Evict("k")

	// Eviction only cancels the cache's hold: the borrower is untouched
	// and the object is not recycled while still on loan.
	if got := len(h.recycled); got != 0 {
		t.Fatalf("borrowed object recycled %d times after evict", got)
	}
	if old.Value().key != "k" {
		t.Fatal("borrower lost its object after evict")
	}

	// A new caller must get a fresh object, never the evicted one.
	fresh, err := c.Acquire("k")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Value() == old.Value() {
		t.Fatal("evicted-but-borrowed object was reused for another caller")
	}
	if h.loadCalls != 2 {
		t.Fatalf("loader called %d times, want 2", h.loadCalls)
	}

	// Returning the old lease now recycles the old object exactly once.
	mustRelease(t, old)
	if got := len(h.recycled); got != 1 {
		t.Fatalf("recycled %d after last release of evicted object, want 1", got)
	}
	mustRelease(t, fresh)

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	h.assertBalanced(t, c)
}

func TestDoubleReleaseRejected(t *testing.T) {
	h := newHarness()
	c := New[string, *resource](h.loader, h.recycler, Config{})

	l, err := c.Acquire("x")
	if err != nil {
		t.Fatal(err)
	}
	c.Evict("x") // detached: the single release below triggers recycling

	if err := l.Release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := l.Release(); !errors.Is(err, ErrReleased) {
		t.Fatalf("second release = %v, want ErrReleased", err)
	}
	if got := len(h.recycled); got != 1 {
		t.Fatalf("recycled %d times after double release attempt, want 1", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	h.assertBalanced(t, c)
}

func TestLoadFailureLeaksNothing(t *testing.T) {
	h := newHarness()
	c := New[string, *resource](h.loader, h.recycler, Config{})

	boom := errors.New("data source down")
	h.mu.Lock()
	h.loadErr = boom
	h.mu.Unlock()

	if _, err := c.Acquire("bad"); !errors.Is(err, boom) {
		t.Fatalf("Acquire error = %v, want injected %v", err, boom)
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("cache holds %d entries after failed load", got)
	}
	if s := c.Stats(); s.Loaded != 0 || s.Recycled != 0 {
		t.Fatalf("stats after failure = %+v, want zeros (no pool object leaked)", s)
	}

	// Data source recovers: the same key loads fine afterwards.
	h.mu.Lock()
	h.loadErr = nil
	h.mu.Unlock()
	l, err := c.Acquire("bad")
	if err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	mustRelease(t, l)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	h.assertBalanced(t, c)
}

func TestCloseRecyclesIdleImmediatelyAndBorrowedLazily(t *testing.T) {
	h := newHarness()
	c := New[string, *resource](h.loader, h.recycler, Config{})

	idle, err := c.Acquire("idle")
	if err != nil {
		t.Fatal(err)
	}
	borrowed, err := c.Acquire("busy")
	if err != nil {
		t.Fatal(err)
	}
	mustRelease(t, idle) // idle object stays cached

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// Close recycles the idle entry now; the borrowed one is delayed.
	if got := len(h.recycled); got != 1 {
		t.Fatalf("recycled %d at Close, want 1 (only the idle object)", got)
	}
	if h.recycled[0] != idle.Value().id {
		t.Fatal("Close recycled the borrowed object instead of the idle one")
	}
	if _, err := c.Acquire("idle"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Acquire after Close = %v, want ErrClosed", err)
	}

	// The delayed object is reclaimed when its last lease comes back.
	mustRelease(t, borrowed)
	if got := len(h.recycled); got != 2 {
		t.Fatalf("recycled %d after delayed release, want 2", got)
	}
	if err := c.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Close = %v, want ErrClosed", err)
	}
	h.assertBalanced(t, c)
}

func TestTTLExpiryWithInjectedClock(t *testing.T) {
	h := newHarness()
	c := New[string, *resource](h.loader, h.recycler, Config{
		TTL:   time.Minute,
		Clock: h.clock,
	})

	l1, err := c.Acquire("ttl")
	if err != nil {
		t.Fatal(err)
	}
	mustRelease(t, l1)

	h.advance(30 * time.Second) // still fresh: cache hit
	l2, _ := c.Acquire("ttl")
	if l2.Value() != l1.Value() {
		t.Fatal("entry expired before TTL elapsed")
	}
	mustRelease(t, l2)

	h.advance(31 * time.Second) // now expired: lazy evict + reload
	l3, err := c.Acquire("ttl")
	if err != nil {
		t.Fatal(err)
	}
	if l3.Value() == l1.Value() {
		t.Fatal("expired object was reused")
	}
	if got := len(h.recycled); got != 1 {
		t.Fatalf("recycled %d after expiry, want 1 (the stale object)", got)
	}
	mustRelease(t, l3)

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	h.assertBalanced(t, c)
}

func TestConcurrentBorrowers(t *testing.T) {
	h := newHarness()
	c := New[string, *resource](h.loader, h.recycler, Config{})

	const workers = 32
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", w%4)
			for i := 0; i < rounds; i++ {
				l, err := c.Acquire(key)
				if err != nil {
					t.Errorf("acquire: %v", err)
					return
				}
				_ = l.Value().id
				if i%7 == 0 {
					c.Evict(key) // churn: evict while others borrow
				}
				if err := l.Release(); err != nil {
					t.Errorf("release: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	h.assertBalanced(t, c)
}

func mustRelease[K comparable, V any](t *testing.T, l *Lease[K, V]) {
	t.Helper()
	if err := l.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}
