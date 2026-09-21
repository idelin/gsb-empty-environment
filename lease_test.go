package lease

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock is an injected, test-controlled time source.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(0, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	at := c.now.Add(d)
	if !at.After(c.now) {
		ch <- c.now
		return ch
	}
	c.timers = append(c.timers, &fakeTimer{at: at, ch: ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var fire []*fakeTimer
	var keep []*fakeTimer
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			fire = append(fire, t)
		} else {
			keep = append(keep, t)
		}
	}
	c.timers = keep
	now := c.now
	c.mu.Unlock()
	for _, t := range fire {
		t.ch <- now
	}
}

func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func queueLen(l *LeaseLock) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.queue)
}

func readerCount(l *LeaseLock) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readers
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A queued writer must eventually get the lock even under a continuous
// stream of readers, because new readers are blocked once a writer queues.
func TestWriterSurvivesContinuousReaderStream(t *testing.T) {
	l := New()
	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if tok, err := l.AcquireRead(ctx); err == nil {
					time.Sleep(time.Millisecond) // hold briefly, then churn
					_ = l.Release(tok)
				}
			}
		}()
	}
	time.Sleep(20 * time.Millisecond) // let the reader stream warm up

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	wt, err := l.AcquireWrite(wctx)
	if err != nil {
		t.Fatalf("writer starved by reader stream: %v", err)
	}
	close(stop)
	if err := l.Release(wt); err != nil {
		t.Fatalf("release writer: %v", err)
	}
	wg.Wait()
}

// Once a writer is queued, new readers must block (anti-starvation gate).
func TestQueuedWriterBlocksNewReaders(t *testing.T) {
	l := New()
	ctx := context.Background()
	hold, err := l.AcquireRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	granted := make(chan *Token, 1)
	go func() {
		if wt, err := l.AcquireWrite(ctx); err == nil {
			granted <- wt
		}
	}()
	waitFor(t, "writer to enqueue", func() bool { return queueLen(l) == 1 })

	rctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := l.AcquireRead(rctx); err == nil {
		t.Fatal("new reader barged ahead of queued writer")
	}

	if err := l.Release(hold); err != nil {
		t.Fatal(err)
	}
	select {
	case wt := <-granted:
		_ = l.Release(wt)
	case <-time.After(2 * time.Second):
		t.Fatal("queued writer was never granted")
	}
}

// Two readers upgrading at the same time must not wait on each other
// forever: both upgrades complete, one after the other.
func TestTwoReadersUpgradeWithoutDeadlock(t *testing.T) {
	l := New()
	ctx := context.Background()
	r1, err := l.AcquireRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := l.AcquireRead(ctx)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 2)
	upgrade := func(rt *Token) {
		uctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		wt, err := l.Upgrade(uctx, rt)
		if err != nil {
			done <- err
			return
		}
		done <- l.Release(wt)
	}
	go upgrade(r1)
	go upgrade(r2)

	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("upgrade failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("readers deadlocked while upgrading")
		}
	}
	if n := readerCount(l); n != 0 {
		t.Fatalf("readers leaked: %d", n)
	}
}

// A failed (timed-out) upgrade must keep the original read lease alive.
func TestFailedUpgradeKeepsReadLease(t *testing.T) {
	l := New()
	ctx := context.Background()
	hold, err := l.AcquireRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := l.AcquireRead(ctx)
	if err != nil {
		t.Fatal(err)
	}

	uctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err = l.Upgrade(uctx, rt) // blocks: "hold" keeps reading
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected upgrade timeout, got %v", err)
	}
	if n := readerCount(l); n != 2 {
		t.Fatalf("read lease lost on failed upgrade: readers=%d", n)
	}
	if queueLen(l) != 0 {
		t.Fatal("failed upgrade left a queue node behind")
	}
	// The original read token is still valid and releases cleanly.
	if err := l.Release(rt); err != nil {
		t.Fatalf("read lease lost after failed upgrade: %v", err)
	}
	if err := l.Release(hold); err != nil {
		t.Fatal(err)
	}
	// Lock state is fully consistent again.
	wctx, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	wt, err := l.AcquireWrite(wctx)
	if err != nil {
		t.Fatalf("lock corrupted after failed upgrade: %v", err)
	}
	_ = l.Release(wt)
}

// A token cannot be released twice.
func TestDoubleReleaseRejected(t *testing.T) {
	l := New()
	tok, err := l.AcquireRead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(tok); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(tok); !errors.Is(err, ErrDoubleRelease) {
		t.Fatalf("expected ErrDoubleRelease, got %v", err)
	}
	if n := readerCount(l); n != 0 {
		t.Fatalf("double release corrupted counters: readers=%d", n)
	}
}

// A token from before a revocation cannot be released into the new
// generation (no cross-generation release).
func TestStaleTokenCannotReleaseAcrossGeneration(t *testing.T) {
	fc := newFakeClock()
	revoked := make(chan uint64, 1)
	l := New(
		WithClock(fc),
		WithWriteTTL(time.Minute),
		WithRevokeHook(func(gen uint64) error { revoked <- gen; return nil }),
	)
	ctx := context.Background()
	wt, err := l.AcquireWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gen := wt.Generation()

	waitFor(t, "TTL timer registration", func() bool { return fc.pending() == 1 })
	fc.Advance(2 * time.Minute) // write lease outlives its TTL
	select {
	case g := <-revoked:
		if g != gen+1 {
			t.Fatalf("revoked generation = %d, want %d", g, gen+1)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expired write lease was not revoked")
	}

	if err := l.Release(wt); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("expected ErrStaleToken, got %v", err)
	}
	// The lock is free for the next generation.
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	wt2, err := l.AcquireWrite(wctx)
	if err != nil {
		t.Fatalf("lock unusable after revoke: %v", err)
	}
	if wt2.Generation() != gen+1 {
		t.Fatalf("new token generation = %d, want %d", wt2.Generation(), gen+1)
	}
	_ = l.Release(wt2)
}

// The injected revocation task's failure is propagated to the caller.
func TestRevokeHookFailureIsInjected(t *testing.T) {
	want := errors.New("injected revoke failure")
	l := New(WithRevokeHook(func(gen uint64) error { return want }))
	rt, err := l.AcquireRead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Revoke(); !errors.Is(err, want) {
		t.Fatalf("expected injected failure, got %v", err)
	}
	if err := l.Release(rt); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("expected ErrStaleToken after revoke, got %v", err)
	}
	wt, err := l.AcquireWrite(context.Background())
	if err != nil {
		t.Fatalf("lock unusable after failed revoke hook: %v", err)
	}
	_ = l.Release(wt)
}

// A writer whose wait is cancelled by timeout is removed from the queue and
// is never granted later.
func TestTimeoutRemovesQueueNode(t *testing.T) {
	l := New()
	ctx := context.Background()
	hold, err := l.AcquireWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}

	wctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	_, err = l.AcquireWrite(wctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	waitFor(t, "queue node removal", func() bool { return queueLen(l) == 0 })

	// If the cancelled node were still queued it would be granted here and
	// leak the write lease; instead the next writer must acquire at once.
	if err := l.Release(hold); err != nil {
		t.Fatal(err)
	}
	wctx2, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2()
	wt, err := l.AcquireWrite(wctx2)
	if err != nil {
		t.Fatalf("cancelled queue node blocked the next writer: %v", err)
	}
	_ = l.Release(wt)
}

// A cancelled upgrader is removed from the queue as well.
func TestCancelledUpgradeRemovesQueueNode(t *testing.T) {
	l := New()
	ctx := context.Background()
	hold, err := l.AcquireRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := l.AcquireRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	uctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	if _, err := l.Upgrade(uctx, rt); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	cancel()
	waitFor(t, "queue node removal", func() bool { return queueLen(l) == 0 })
	if err := l.Release(rt); err != nil {
		t.Fatal(err)
	}
	_ = l.Release(hold)
}

// Hammer the lock from many goroutines to surface data races and deadlocks
// under `go test -race`.
func TestConcurrentStress(t *testing.T) {
	l := New()
	ctx := context.Background()
	var wg sync.WaitGroup
	deadline := time.Now().Add(300 * time.Millisecond)

	for i := 0; i < 9; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for time.Now().Before(deadline) {
				switch i % 3 {
				case 0: // readers
					if tok, err := l.AcquireRead(ctx); err == nil {
						_ = l.Release(tok)
					}
				case 1: // writers with a timeout
					wctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
					tok, err := l.AcquireWrite(wctx)
					cancel()
					if err == nil {
						_ = l.Release(tok)
					}
				case 2: // upgraders with a timeout
					rt, err := l.AcquireRead(ctx)
					if err != nil {
						continue
					}
					uctx, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
					wt, err := l.Upgrade(uctx, rt)
					cancel()
					if err == nil {
						_ = l.Release(wt)
					} else {
						_ = l.Release(rt) // read lease survives failure
					}
				}
			}
		}(i)
	}
	// Concurrent revocations invalidate in-flight leases.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			_ = l.Revoke()
			time.Sleep(10 * time.Millisecond)
		}
	}()
	wg.Wait()
}
