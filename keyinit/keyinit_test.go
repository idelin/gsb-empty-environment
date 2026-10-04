package keyinit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the timeout elapses.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// recvResult receives one result with a timeout guard against deadlocks.
func recvResult(t *testing.T, what string, ch <-chan result) result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return result{}
	}
}

type result struct {
	val interface{}
	err error
}

func doAsync(m *Manager, ctx context.Context, key interface{}, fn InitFunc) <-chan result {
	ch := make(chan result, 1)
	go func() {
		v, err := m.Do(ctx, key, fn)
		ch <- result{v, err}
	}()
	return ch
}

// Calls in the same round share one execution and one result.
func TestSameRoundSharesResult(t *testing.T) {
	m := NewManager()
	release := make(chan struct{})
	var calls int32
	fn := func(ctx context.Context) (interface{}, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return "shared", nil
	}

	const n = 8
	results := make([]<-chan result, n)
	for i := 0; i < n; i++ {
		results[i] = doAsync(m, context.Background(), "k", fn)
	}
	waitFor(t, "init to start", func() bool { return atomic.LoadInt32(&calls) == 1 })
	close(release)

	for i, ch := range results {
		r := recvResult(t, "caller", ch)
		if r.err != nil || r.val != "shared" {
			t.Fatalf("caller %d got (%v, %v), want (shared, nil)", i, r.val, r.err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("init ran %d times, want 1", got)
	}
}

// A successful result is cached; later calls do not re-run fn.
func TestSuccessIsCached(t *testing.T) {
	m := NewManager()
	var calls int32
	fn := func(ctx context.Context) (interface{}, error) {
		return atomic.AddInt32(&calls, 1), nil
	}
	for i := 0; i < 3; i++ {
		v, err := m.Do(context.Background(), "k", fn)
		if err != nil || v.(int32) != 1 {
			t.Fatalf("call %d got (%v, %v), want (1, nil)", i, v, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("init ran %d times, want 1", got)
	}
}

// A failure is broadcast to every waiter of the round, nothing is cached,
// and the next call retries with a fresh round.
func TestFailureBroadcastAndRetry(t *testing.T) {
	m := NewManager()
	boom := errors.New("boom")
	release := make(chan struct{})
	var calls int32
	fn := func(ctx context.Context) (interface{}, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			<-release
			return nil, boom
		}
		return "recovered", nil
	}

	const n = 6
	results := make([]<-chan result, n)
	for i := 0; i < n; i++ {
		results[i] = doAsync(m, context.Background(), "k", fn)
	}
	waitFor(t, "init to start", func() bool { return atomic.LoadInt32(&calls) == 1 })
	// Give the remaining callers a moment to join the in-flight round.
	time.Sleep(20 * time.Millisecond)
	close(release)

	for i, ch := range results {
		r := recvResult(t, "waiter", ch)
		if r.err != boom {
			t.Fatalf("waiter %d got err %v, want broadcast %v", i, r.err, boom)
		}
	}

	v, err := m.Do(context.Background(), "k", fn)
	if err != nil || v != "recovered" {
		t.Fatalf("retry got (%v, %v), want (recovered, nil)", v, err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("init ran %d times, want 2", got)
	}
}

// A waiter whose context is canceled stops waiting with ctx.Err(); the
// round itself is unaffected and completes for the other callers.
func TestWaiterCancellation(t *testing.T) {
	m := NewManager()
	started := make(chan struct{})
	release := make(chan struct{})
	fn := func(ctx context.Context) (interface{}, error) {
		close(started)
		<-release
		return "v", nil
	}

	initiator := doAsync(m, context.Background(), "k", fn)
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	waiter := doAsync(m, ctx, "k", fn)
	cancel()
	if r := recvResult(t, "canceled waiter", waiter); r.err != context.Canceled {
		t.Fatalf("canceled waiter got err %v, want context.Canceled", r.err)
	}

	close(release)
	if r := recvResult(t, "initiator", initiator); r.err != nil || r.val != "v" {
		t.Fatalf("initiator got (%v, %v), want (v, nil)", r.val, r.err)
	}
	// The successful round is cached despite the canceled waiter.
	if v, err := m.Do(context.Background(), "k", fn); err != nil || v != "v" {
		t.Fatalf("cached call got (%v, %v), want (v, nil)", v, err)
	}
}

// Canceling the caller that started the round cancels the context passed to
// fn; the resulting failure is broadcast to all waiters and the key can be
// retried.
func TestInitCancellationBroadcastsFailure(t *testing.T) {
	m := NewManager()
	started := make(chan struct{})
	fn := func(ctx context.Context) (interface{}, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	initiator := doAsync(m, ctx, "k", fn)
	<-started

	waiter := doAsync(m, context.Background(), "k", fn)
	time.Sleep(20 * time.Millisecond) // let the waiter join the round
	cancel()

	for name, ch := range map[string]<-chan result{"initiator": initiator, "waiter": waiter} {
		if r := recvResult(t, name, ch); r.err != context.Canceled {
			t.Fatalf("%s got err %v, want context.Canceled", name, r.err)
		}
	}

	var calls int32
	v, err := m.Do(context.Background(), "k", func(ctx context.Context) (interface{}, error) {
		atomic.AddInt32(&calls, 1)
		return "after-cancel", nil
	})
	if err != nil || v != "after-cancel" {
		t.Fatalf("retry got (%v, %v), want (after-cancel, nil)", v, err)
	}
}

// Invalidate drops the cached value so the next call re-initializes.
func TestInvalidateDropsCache(t *testing.T) {
	m := NewManager()
	var calls int32
	fn := func(ctx context.Context) (interface{}, error) {
		return atomic.AddInt32(&calls, 1), nil
	}

	v, err := m.Do(context.Background(), "k", fn)
	if err != nil || v.(int32) != 1 {
		t.Fatalf("first call got (%v, %v), want (1, nil)", v, err)
	}
	m.Invalidate("k")
	v, err = m.Do(context.Background(), "k", fn)
	if err != nil || v.(int32) != 2 {
		t.Fatalf("after Invalidate got (%v, %v), want (2, nil)", v, err)
	}
}

// A round that completes after being invalidated is late: its result goes
// to its own waiters but must not overwrite the newer round's cached value.
func TestLateRoundDoesNotPolluteNewRound(t *testing.T) {
	m := NewManager()
	started := make(chan struct{})
	release := make(chan struct{})
	oldFn := func(ctx context.Context) (interface{}, error) {
		close(started)
		<-release
		return "old", nil
	}

	oldRound := doAsync(m, context.Background(), "k", oldFn)
	<-started
	m.Invalidate("k")

	v, err := m.Do(context.Background(), "k", func(ctx context.Context) (interface{}, error) {
		return "new", nil
	})
	if err != nil || v != "new" {
		t.Fatalf("new round got (%v, %v), want (new, nil)", v, err)
	}

	close(release) // the old round completes late
	if r := recvResult(t, "old round waiter", oldRound); r.err != nil || r.val != "old" {
		t.Fatalf("old round waiter got (%v, %v), want (old, nil)", r.val, r.err)
	}

	// The late result must not have replaced the new round's cache.
	calls := 0
	v, err = m.Do(context.Background(), "k", func(ctx context.Context) (interface{}, error) {
		calls++
		return "unexpected", nil
	})
	if err != nil || v != "new" || calls != 0 {
		t.Fatalf("cache got (%v, %v) with %d init runs, want (new, nil) with 0", v, err, calls)
	}
}

// An init function calling Do for its own key is detected instead of
// deadlocking, and the key stays usable afterwards.
func TestSameKeyRecursionDetected(t *testing.T) {
	m := NewManager()
	var innerErr error
	fn := func(ctx context.Context) (interface{}, error) {
		_, innerErr = m.Do(ctx, "k", func(context.Context) (interface{}, error) {
			return "unreachable", nil
		})
		if innerErr != nil {
			return nil, innerErr
		}
		return "unreachable", nil
	}

	done := doAsync(m, context.Background(), "k", fn)
	if r := recvResult(t, "recursive round", done); r.err != ErrRecursiveInit {
		t.Fatalf("recursive round got err %v, want ErrRecursiveInit", r.err)
	}
	if innerErr != ErrRecursiveInit {
		t.Fatalf("inner Do got err %v, want ErrRecursiveInit", innerErr)
	}

	v, err := m.Do(context.Background(), "k", func(ctx context.Context) (interface{}, error) {
		return "healthy", nil
	})
	if err != nil || v != "healthy" {
		t.Fatalf("after recursion got (%v, %v), want (healthy, nil)", v, err)
	}
}

// An init function may initialize other keys via the provided context.
func TestCrossKeyInitAllowed(t *testing.T) {
	m := NewManager()
	fnB := func(ctx context.Context) (interface{}, error) { return "b", nil }
	fnA := func(ctx context.Context) (interface{}, error) {
		v, err := m.Do(ctx, "b", fnB)
		if err != nil {
			return nil, err
		}
		return "a+" + v.(string), nil
	}

	v, err := m.Do(context.Background(), "a", fnA)
	if err != nil || v != "a+b" {
		t.Fatalf("got (%v, %v), want (a+b, nil)", v, err)
	}
	// Both keys are now cached.
	if v, err := m.Do(context.Background(), "b", fnB); err != nil || v != "b" {
		t.Fatalf("cached b got (%v, %v), want (b, nil)", v, err)
	}
}

// A cross-key cycle (a -> b -> a) within one call chain is detected.
func TestCrossKeyCycleDetected(t *testing.T) {
	m := NewManager()
	var fnA, fnB InitFunc
	fnA = func(ctx context.Context) (interface{}, error) {
		return m.Do(ctx, "b", fnB)
	}
	fnB = func(ctx context.Context) (interface{}, error) {
		return m.Do(ctx, "a", fnA)
	}

	done := doAsync(m, context.Background(), "a", fnA)
	if r := recvResult(t, "cyclic round", done); r.err != ErrRecursiveInit {
		t.Fatalf("cyclic round got err %v, want ErrRecursiveInit", r.err)
	}
}

// A panic in fn fails the round (releasing waiters) and re-panics in the
// caller that started the round; the key remains retryable.
func TestPanicReleasesWaiters(t *testing.T) {
	m := NewManager()
	started := make(chan struct{})
	release := make(chan struct{})
	fn := func(ctx context.Context) (interface{}, error) {
		close(started)
		<-release
		panic("kaboom")
	}

	panicked := make(chan interface{}, 1)
	go func() {
		defer func() { panicked <- recover() }()
		m.Do(context.Background(), "k", fn)
	}()
	<-started

	waiter := doAsync(m, context.Background(), "k", fn)
	time.Sleep(20 * time.Millisecond) // let the waiter join the round
	close(release)
	if r := recvResult(t, "waiter of panicking round", waiter); r.err == nil {
		t.Fatal("waiter of panicking round got nil error, want panic error")
	}
	select {
	case p := <-panicked:
		if p != "kaboom" {
			t.Fatalf("initiator recovered %v, want kaboom", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initiator did not re-panic")
	}

	v, err := m.Do(context.Background(), "k", func(ctx context.Context) (interface{}, error) {
		return "recovered", nil
	})
	if err != nil || v != "recovered" {
		t.Fatalf("retry after panic got (%v, %v), want (recovered, nil)", v, err)
	}
}

// Different keys initialize independently and concurrently.
func TestKeysAreIndependent(t *testing.T) {
	m := NewManager()
	release := make(chan struct{})
	slow := doAsync(m, context.Background(), "slow", func(ctx context.Context) (interface{}, error) {
		<-release
		return "slow", nil
	})
	v, err := m.Do(context.Background(), "fast", func(ctx context.Context) (interface{}, error) {
		return "fast", nil
	})
	if err != nil || v != "fast" {
		t.Fatalf("fast key got (%v, %v), want (fast, nil)", v, err)
	}
	close(release)
	if r := recvResult(t, "slow key", slow); r.err != nil || r.val != "slow" {
		t.Fatalf("slow key got (%v, %v), want (slow, nil)", r.val, r.err)
	}
}

// Concurrent Do and Invalidate calls must not corrupt state or deadlock.
func TestConcurrentDoAndInvalidate(t *testing.T) {
	m := NewManager()
	var calls int32
	fn := func(ctx context.Context) (interface{}, error) {
		return atomic.AddInt32(&calls, 1), nil
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, err := m.Do(context.Background(), "k", fn); err != nil {
						t.Errorf("Do failed: %v", err)
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		m.Invalidate("k")
	}
	close(stop)
	wg.Wait()
}
