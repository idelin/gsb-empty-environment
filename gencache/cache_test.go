package gencache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// blockingLoader blocks each load on a per-call gate so tests can interleave
// invalidation, completion and cancellation deterministically.
type blockingLoader struct {
	mu       sync.Mutex
	calls    int
	gates    map[int]chan struct{}
	ctxs     map[int]context.Context
	started  chan int // receives the 1-based index of each started call
	returned chan int // receives the index of each finished call
}

func newBlockingLoader() *blockingLoader {
	return &blockingLoader{
		gates:    make(map[int]chan struct{}),
		ctxs:     make(map[int]context.Context),
		started:  make(chan int, 16),
		returned: make(chan int, 16),
	}
}

func (l *blockingLoader) load(ctx context.Context, key string) (string, error) {
	l.mu.Lock()
	l.calls++
	n := l.calls
	gate := make(chan struct{})
	l.gates[n] = gate
	l.ctxs[n] = ctx
	l.mu.Unlock()

	l.started <- n
	defer func() { l.returned <- n }()

	select {
	case <-gate:
		return fmt.Sprintf("gen%d", n), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (l *blockingLoader) release(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	close(l.gates[n])
}

func (l *blockingLoader) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

// ctxErr reports whether the load context of call n has been cancelled.
func (l *blockingLoader) ctxErr(n int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ctxs[n].Err()
}

func waitForWaiters(t *testing.T, c *Cache[string, string], key string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := 0
		if e, ok := c.items[key]; ok {
			n = e.waiters
		}
		c.mu.Unlock()
		if n == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d waiters on key %q", want, key)
}

func getAsync(c *Cache[string, string], ctx context.Context, key string) <-chan struct {
	val string
	err error
} {
	ch := make(chan struct {
		val string
		err error
	}, 1)
	go func() {
		v, err := c.Get(ctx, key)
		ch <- struct {
			val string
			err error
		}{v, err}
	}()
	return ch
}

// Concurrent Gets on the same key in the same generation share one load.
func TestSameGenerationLoadsOnce(t *testing.T) {
	l := newBlockingLoader()
	c := New[string, string](l.load)

	r1 := getAsync(c, context.Background(), "k")
	if n := <-l.started; n != 1 {
		t.Fatalf("expected call 1 to start, got %d", n)
	}
	r2 := getAsync(c, context.Background(), "k")
	waitForWaiters(t, c, "k", 2)

	l.release(1)
	for i, r := range []<-chan struct {
		val string
		err error
	}{r1, r2} {
		res := <-r
		if res.err != nil || res.val != "gen1" {
			t.Fatalf("waiter %d got %q, %v; want gen1, nil", i, res.val, res.err)
		}
	}
	if got := l.callCount(); got != 1 {
		t.Fatalf("loader called %d times, want 1", got)
	}

	// Completed value is cached: no further loader call.
	res := <-getAsync(c, context.Background(), "k")
	if res.err != nil || res.val != "gen1" {
		t.Fatalf("cached get got %q, %v; want gen1, nil", res.val, res.err)
	}
	if got := l.callCount(); got != 1 {
		t.Fatalf("loader called %d times after cache hit, want 1", got)
	}
}

// Invalidation during a load: the old load still serves its original waiters
// but must not be published over the new generation.
func TestInvalidateDuringLoad(t *testing.T) {
	l := newBlockingLoader()
	c := New[string, string](l.load)

	oldWaiter := getAsync(c, context.Background(), "k")
	if n := <-l.started; n != 1 {
		t.Fatalf("expected call 1 to start, got %d", n)
	}

	c.Invalidate("k")

	newWaiter := getAsync(c, context.Background(), "k")
	if n := <-l.started; n != 2 {
		t.Fatalf("expected call 2 after invalidate, got %d", n)
	}

	// Complete the new generation first.
	l.release(2)
	if res := <-newWaiter; res.err != nil || res.val != "gen2" {
		t.Fatalf("new waiter got %q, %v; want gen2, nil", res.val, res.err)
	}

	// The retired load still delivers to its original waiter.
	l.release(1)
	if res := <-oldWaiter; res.err != nil || res.val != "gen1" {
		t.Fatalf("old waiter got %q, %v; want gen1, nil", res.val, res.err)
	}

	// The old result must not overwrite the new generation.
	res := <-getAsync(c, context.Background(), "k")
	if res.err != nil || res.val != "gen2" {
		t.Fatalf("post-invalidation get got %q, %v; want gen2, nil", res.val, res.err)
	}
	if got := l.callCount(); got != 2 {
		t.Fatalf("loader called %d times, want 2", got)
	}
}

// Cancelling one waiter leaves the load and the other waiters untouched.
func TestOneWaiterCancelKeepsLoad(t *testing.T) {
	l := newBlockingLoader()
	c := New[string, string](l.load)

	ctxA, cancelA := context.WithCancel(context.Background())
	cancelled := getAsync(c, ctxA, "k")
	<-l.started
	survivor := getAsync(c, context.Background(), "k")
	waitForWaiters(t, c, "k", 2)

	cancelA()
	if res := <-cancelled; res.err != context.Canceled {
		t.Fatalf("cancelled waiter got err %v, want context.Canceled", res.err)
	}
	if err := l.ctxErr(1); err != nil {
		t.Fatalf("load context cancelled while a waiter remained: %v", err)
	}

	l.release(1)
	if res := <-survivor; res.err != nil || res.val != "gen1" {
		t.Fatalf("surviving waiter got %q, %v; want gen1, nil", res.val, res.err)
	}
	<-l.returned

	// Result is cached despite the cancellation.
	res := <-getAsync(c, context.Background(), "k")
	if res.err != nil || res.val != "gen1" {
		t.Fatalf("cached get got %q, %v; want gen1, nil", res.val, res.err)
	}
	if got := l.callCount(); got != 1 {
		t.Fatalf("loader called %d times, want 1", got)
	}
}

// The load is cancelled only once its last waiter cancels, and the retired
// entry is dropped so the next Get reloads.
func TestAllWaitersCancelCancelsLoad(t *testing.T) {
	l := newBlockingLoader()
	c := New[string, string](l.load)

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	ra := getAsync(c, ctxA, "k")
	<-l.started
	rb := getAsync(c, ctxB, "k")
	waitForWaiters(t, c, "k", 2)

	cancelA()
	if res := <-ra; res.err != context.Canceled {
		t.Fatalf("waiter A got err %v, want context.Canceled", res.err)
	}
	if err := l.ctxErr(1); err != nil {
		t.Fatalf("load cancelled while waiter B remained: %v", err)
	}

	cancelB()
	if res := <-rb; res.err != context.Canceled {
		t.Fatalf("waiter B got err %v, want context.Canceled", res.err)
	}
	if n := <-l.returned; n != 1 {
		t.Fatalf("expected cancelled call 1 to return, got %d", n)
	}

	// Entry was retired: the next Get triggers a fresh load.
	r := getAsync(c, context.Background(), "k")
	if n := <-l.started; n != 2 {
		t.Fatalf("expected call 2 after all waiters cancelled, got %d", n)
	}
	l.release(2)
	if res := <-r; res.err != nil || res.val != "gen2" {
		t.Fatalf("reload got %q, %v; want gen2, nil", res.val, res.err)
	}
	if got := l.callCount(); got != 2 {
		t.Fatalf("loader called %d times, want 2", got)
	}
}

// Cancelling a waiter of a retired generation must not disturb the new
// generation's entry.
func TestCancelOldWaiterAfterInvalidate(t *testing.T) {
	l := newBlockingLoader()
	c := New[string, string](l.load)

	ctxOld, cancelOld := context.WithCancel(context.Background())
	oldWaiter := getAsync(c, ctxOld, "k")
	<-l.started

	c.Invalidate("k")

	newWaiter := getAsync(c, context.Background(), "k")
	if n := <-l.started; n != 2 {
		t.Fatalf("expected call 2 after invalidate, got %d", n)
	}

	cancelOld()
	if res := <-oldWaiter; res.err != context.Canceled {
		t.Fatalf("old waiter got err %v, want context.Canceled", res.err)
	}
	// The orphaned old load is cancelled (no waiters left)...
	if n := <-l.returned; n != 1 {
		t.Fatalf("expected old call 1 to return, got %d", n)
	}
	// ...but the new generation is unaffected.
	if err := l.ctxErr(2); err != nil {
		t.Fatalf("new generation load context cancelled: %v", err)
	}
	l.release(2)
	if res := <-newWaiter; res.err != nil || res.val != "gen2" {
		t.Fatalf("new waiter got %q, %v; want gen2, nil", res.val, res.err)
	}
	if got := l.callCount(); got != 2 {
		t.Fatalf("loader called %d times, want 2", got)
	}
}

// Invalidating a completed entry forces a reload on the next Get.
func TestInvalidateAfterComplete(t *testing.T) {
	l := newBlockingLoader()
	c := New[string, string](l.load)

	r := getAsync(c, context.Background(), "k")
	<-l.started
	l.release(1)
	if res := <-r; res.err != nil || res.val != "gen1" {
		t.Fatalf("first get got %q, %v; want gen1, nil", res.val, res.err)
	}

	c.Invalidate("k")

	r2 := getAsync(c, context.Background(), "k")
	if n := <-l.started; n != 2 {
		t.Fatalf("expected call 2 after invalidate, got %d", n)
	}
	l.release(2)
	if res := <-r2; res.err != nil || res.val != "gen2" {
		t.Fatalf("second get got %q, %v; want gen2, nil", res.val, res.err)
	}
	if got := l.callCount(); got != 2 {
		t.Fatalf("loader called %d times, want 2", got)
	}
}
