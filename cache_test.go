package hotcache

import (
	"context"
	"errors"
	"testing"
	"time"
)

// loadHandle is one blocked loader invocation. The test releases it with
// the desired result, or observes its context cancellation.
type loadHandle struct {
	ctx     context.Context
	release chan struct{}
	result  string
	err     error
}

func (h *loadHandle) complete(result string, err error) {
	h.result, h.err = result, err
	close(h.release)
}

// blockingLoader blocks every call until the test completes the handle.
type blockingLoader struct {
	handles chan *loadHandle
}

func newBlockingLoader() *blockingLoader {
	return &blockingLoader{handles: make(chan *loadHandle, 16)}
}

func (b *blockingLoader) load(ctx context.Context, key string) (string, error) {
	h := &loadHandle{ctx: ctx, release: make(chan struct{})}
	b.handles <- h
	select {
	case <-h.release:
		return h.result, h.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (b *blockingLoader) next(t *testing.T) *loadHandle {
	t.Helper()
	select {
	case h := <-b.handles:
		return h
	case <-time.After(2 * time.Second):
		t.Fatal("expected a loader call")
		return nil
	}
}

func (b *blockingLoader) expectIdle(t *testing.T) {
	t.Helper()
	select {
	case <-b.handles:
		t.Fatal("unexpected extra loader call")
	case <-time.After(50 * time.Millisecond):
	}
}

func (b *blockingLoader) calls() int {
	n := 0
	for {
		select {
		case <-b.handles:
			n++
		default:
			return n
		}
	}
}

type getResult struct {
	val string
	err error
}

func asyncGet(c *Cache[string, string], ctx context.Context, key string) chan getResult {
	ch := make(chan getResult, 1)
	go func() {
		v, err := c.Get(ctx, key)
		ch <- getResult{v, err}
	}()
	return ch
}

func await(t *testing.T, ch chan getResult) getResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not return")
		return getResult{}
	}
}

func waitForWaiters(t *testing.T, c *Cache[string, string], key string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		e := c.entries[key]
		got := 0
		if e != nil && e.inflight != nil {
			got = e.inflight.waiters
		}
		c.mu.Unlock()
		if got == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d waiters on %q", n, key)
}

// Completion: concurrent same-key, same-generation Gets share one load.
func TestSingleFlightSameGeneration(t *testing.T) {
	loader := newBlockingLoader()
	c := New[string, string](loader.load)

	r1 := asyncGet(c, context.Background(), "k")
	r2 := asyncGet(c, context.Background(), "k")
	r3 := asyncGet(c, context.Background(), "k")

	h := loader.next(t)
	waitForWaiters(t, c, "k", 3)
	h.complete("v1", nil)

	for i, r := range []chan getResult{r1, r2, r3} {
		if got := await(t, r); got.val != "v1" || got.err != nil {
			t.Fatalf("waiter %d got %v, want v1", i, got)
		}
	}
	if n := loader.calls(); n != 0 {
		t.Fatalf("loader called %d extra times, want 0", n)
	}

	// Cached value is served without a new load.
	if v, err := c.Get(context.Background(), "k"); v != "v1" || err != nil {
		t.Fatalf("cached Get got %v, %v", v, err)
	}
	loader.expectIdle(t)
}

// Invalidation during a load: the stale load still serves its own waiters
// but must not be written into the new generation.
func TestInvalidateDuringLoad(t *testing.T) {
	loader := newBlockingLoader()
	c := New[string, string](loader.load)

	oldWaiter := asyncGet(c, context.Background(), "k")
	oldLoad := loader.next(t)
	waitForWaiters(t, c, "k", 1)

	c.Invalidate("k")

	// New generation: a new Get must start a second load, not join the old one.
	newWaiter := asyncGet(c, context.Background(), "k")
	newLoad := loader.next(t)

	// The old load finishes after the invalidation.
	oldLoad.complete("stale", nil)
	if got := await(t, oldWaiter); got.val != "stale" || got.err != nil {
		t.Fatalf("old waiter got %v, want stale", got)
	}

	newLoad.complete("fresh", nil)
	if got := await(t, newWaiter); got.val != "fresh" || got.err != nil {
		t.Fatalf("new waiter got %v, want fresh", got)
	}

	// Generation isolation: the cache holds the new generation's value.
	if v, err := c.Get(context.Background(), "k"); v != "fresh" || err != nil {
		t.Fatalf("cached Get got %v, %v; stale load must not be stored", v, err)
	}
	if n := loader.calls(); n != 0 {
		t.Fatalf("loader called %d extra times, want 0 (total exactly 2)", n)
	}
	loader.expectIdle(t)
}

// Invalidation with no new waiter: the stale result is dropped entirely.
func TestInvalidateDropsStaleResult(t *testing.T) {
	loader := newBlockingLoader()
	c := New[string, string](loader.load)

	waiter := asyncGet(c, context.Background(), "k")
	oldLoad := loader.next(t)
	waitForWaiters(t, c, "k", 1)

	c.Invalidate("k")
	oldLoad.complete("stale", nil)

	if got := await(t, waiter); got.val != "stale" || got.err != nil {
		t.Fatalf("waiter got %v, want stale", got)
	}

	// The stale value was not cached: the next Get triggers a fresh load.
	retry := asyncGet(c, context.Background(), "k")
	second := loader.next(t)
	second.complete("fresh", nil)
	if got := await(t, retry); got.val != "fresh" || got.err != nil {
		t.Fatalf("retry got %v, want fresh", got)
	}
	loader.expectIdle(t)
}

// Cancellation of one waiter leaves the shared load running for the rest.
func TestWaiterCancelDoesNotAffectOthers(t *testing.T) {
	loader := newBlockingLoader()
	c := New[string, string](loader.load)

	ctx1, cancel1 := context.WithCancel(context.Background())
	r1 := asyncGet(c, ctx1, "k")
	r2 := asyncGet(c, context.Background(), "k")

	h := loader.next(t)
	waitForWaiters(t, c, "k", 2)

	cancel1()
	if got := await(t, r1); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled waiter got %v, want context.Canceled", got)
	}

	// The load's context must still be alive for the remaining waiter.
	select {
	case <-h.ctx.Done():
		t.Fatal("load cancelled while a waiter remains")
	default:
	}

	h.complete("v", nil)
	if got := await(t, r2); got.val != "v" || got.err != nil {
		t.Fatalf("remaining waiter got %v, want v", got)
	}
	if n := loader.calls(); n != 0 {
		t.Fatalf("loader called %d extra times, want 0 (total exactly 1)", n)
	}
}

// The load is cancelled only after every waiter has cancelled, and its
// cancelled result is not cached.
func TestAllWaitersCancelCancelsLoad(t *testing.T) {
	loader := newBlockingLoader()
	c := New[string, string](loader.load)

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	r1 := asyncGet(c, ctx1, "k")
	r2 := asyncGet(c, ctx2, "k")

	h := loader.next(t)
	waitForWaiters(t, c, "k", 2)

	cancel1()
	select {
	case <-h.ctx.Done():
		t.Fatal("load cancelled before all waiters cancelled")
	case <-time.After(50 * time.Millisecond):
	}

	cancel2()
	for _, r := range []chan getResult{r1, r2} {
		if got := await(t, r); !errors.Is(got.err, context.Canceled) {
			t.Fatalf("waiter got %v, want context.Canceled", got)
		}
	}

	// The loader observes the cancellation once the last waiter leaves.
	select {
	case <-h.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("load context not cancelled after all waiters cancelled")
	}

	// The cancelled load is not cached: a new Get starts a fresh load.
	retry := asyncGet(c, context.Background(), "k")
	second := loader.next(t)
	second.complete("v2", nil)
	if got := await(t, retry); got.val != "v2" || got.err != nil {
		t.Fatalf("retry got %v, want v2", got)
	}
	loader.expectIdle(t)
}

// Loader errors are delivered to waiters but not cached.
func TestLoaderErrorNotCached(t *testing.T) {
	loader := newBlockingLoader()
	c := New[string, string](loader.load)

	r := asyncGet(c, context.Background(), "k")
	h := loader.next(t)
	boom := errors.New("boom")
	h.complete("", boom)
	if got := await(t, r); !errors.Is(got.err, boom) {
		t.Fatalf("waiter got %v, want boom", got)
	}

	retry := asyncGet(c, context.Background(), "k")
	second := loader.next(t)
	second.complete("ok", nil)
	if got := await(t, retry); got.val != "ok" || got.err != nil {
		t.Fatalf("retry got %v, want ok", got)
	}
	loader.expectIdle(t)
}
