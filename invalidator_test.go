package coalesce

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// manualClock is an injectable clock: After registers a waiter that only
// fires when the test calls Advance.
type manualClock struct {
	mu      sync.Mutex
	waiters []chan time.Time
}

func (c *manualClock) After(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.waiters = append(c.waiters, ch)
	c.mu.Unlock()
	return ch
}

func (c *manualClock) Advance() {
	c.mu.Lock()
	w := c.waiters
	c.waiters = nil
	c.mu.Unlock()
	for _, ch := range w {
		ch <- time.Now()
	}
}

func (c *manualClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

// loadRecorder counts and orders Load calls.
type loadRecorder struct {
	mu    sync.Mutex
	calls []string
	fn    func(key string) (any, error)
}

func (r *loadRecorder) load(_ context.Context, key string) (any, error) {
	r.mu.Lock()
	r.calls = append(r.calls, key)
	fn := r.fn
	r.mu.Unlock()
	if fn != nil {
		return fn(key)
	}
	return "v-" + key, nil
}

func (r *loadRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *loadRecorder) order() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// 重复失效千次不千次加载：1000 次失效合并为一次加载。
func TestCoalescesThousandInvalidationsIntoOneLoad(t *testing.T) {
	clock := &manualClock{}
	rec := &loadRecorder{}
	iv := New(Options{
		Load:     rec.load,
		After:    clock.After,
		Interval: time.Millisecond,
	})
	defer iv.Close()

	for v := uint64(1); v <= 1000; v++ {
		iv.Invalidate("hot", v)
	}
	eventually(t, "worker waiting on rate limiter", func() bool { return clock.Pending() == 1 })
	clock.Advance()

	eventually(t, "value refreshed", func() bool {
		_, ok := iv.Get("hot")
		return ok
	})
	// Drain any trailing queue activity, then assert exactly one load.
	eventually(t, "queues drained", func() bool { return iv.Pending() == 0 })
	if got := rec.count(); got != 1 {
		t.Fatalf("1000 invalidations caused %d loads, want 1", got)
	}
	st := iv.Inspect("hot")
	if !st.Valid || st.ValidVersion != 1000 || st.Dirty {
		t.Fatalf("unexpected state: %+v", st)
	}
}

// 队满不能把旧值继续标有效：溢出键必须保守标失效，并在有空位后补刷新。
func TestQueueFullConservativelyInvalidates(t *testing.T) {
	clock := &manualClock{}
	rec := &loadRecorder{}
	iv := New(Options{
		Load:     rec.load,
		After:    clock.After,
		Interval: time.Millisecond,
		Capacity: 2,
	})
	defer iv.Close()

	// Give "old" a valid cached value first.
	iv.Invalidate("old", 1)
	eventually(t, "rate limiter armed", func() bool { return clock.Pending() == 1 })
	clock.Advance()
	eventually(t, "old value cached", func() bool {
		v, ok := iv.Get("old")
		return ok && v == "v-old"
	})

	// Occupy the worker with "a", then fill the queue with "b" and "c".
	iv.Invalidate("a", 1)
	eventually(t, "worker busy on a", func() bool { return clock.Pending() == 1 })
	iv.Invalidate("b", 1)
	iv.Invalidate("c", 1)
	eventually(t, "queue full", func() bool { return iv.Pending() == 2 })

	// Queue is full: invalidating "old" must overflow and the stale value
	// must stop being served immediately.
	iv.Invalidate("old", 2)
	if _, ok := iv.Get("old"); ok {
		t.Fatal("overflowed key still served its old value as valid")
	}
	if st := iv.Inspect("old"); !st.Overflowed || !st.Dirty {
		t.Fatalf("expected overflowed+dirty, got %+v", st)
	}

	// Drain everything; "old" must be refreshed eventually.
	for i := 0; i < 8 && rec.count() < 5; i++ {
		clock.Advance()
		time.Sleep(2 * time.Millisecond)
	}
	eventually(t, "old refreshed after overflow", func() bool {
		_, ok := iv.Get("old")
		return ok
	})
	st := iv.Inspect("old")
	if !st.Valid || st.ValidVersion != 2 || st.Overflowed {
		t.Fatalf("unexpected state after drain: %+v", st)
	}
}

// 刷新成功只确认其实际覆盖的代际：加载期间到达的更新失效不被确认。
func TestRefreshConfirmsOnlyCoveredGenerations(t *testing.T) {
	release := make(chan struct{}, 2)
	started := make(chan int, 4)

	var mu sync.Mutex
	var n int
	iv := New(Options{
		Load: func(ctx context.Context, key string) (any, error) {
			mu.Lock()
			n++
			cur := n
			mu.Unlock()
			started <- cur
			<-release // hold the load so the test can invalidate mid-flight
			return cur, nil
		},
	})
	defer iv.Close()

	iv.Invalidate("k", 1)
	if <-started != 1 {
		t.Fatal("first load did not start")
	}
	// A newer invalidation arrives while the first load is in flight.
	iv.Invalidate("k", 2)
	release <- struct{}{}

	// The first refresh must NOT confirm generation 2: key stays dirty and
	// a second load is scheduled.
	if <-started != 2 {
		t.Fatal("second load did not start")
	}
	if _, ok := iv.Get("k"); ok {
		t.Fatal("value served as valid although generation 2 is uncovered")
	}
	if st := iv.Inspect("k"); !st.Dirty {
		t.Fatalf("key should still be dirty, got %+v", st)
	}

	release <- struct{}{}
	eventually(t, "generation 2 confirmed", func() bool {
		st := iv.Inspect("k")
		return st.Valid && st.ValidVersion == 2
	})
	if v, ok := iv.Get("k"); !ok || v != 2 {
		t.Fatalf("got (%v,%v), want (2,true)", v, ok)
	}
}

// 关键键可优先：critical 键插队刷新。
func TestCriticalKeysArePrioritized(t *testing.T) {
	clock := &manualClock{}
	rec := &loadRecorder{}
	iv := New(Options{
		Load:       rec.load,
		After:      clock.After,
		Interval:   time.Millisecond,
		IsCritical: func(k string) bool { return k == "critical" },
	})
	defer iv.Close()

	iv.Invalidate("n1", 1) // worker picks this up and blocks on the limiter
	eventually(t, "limiter armed", func() bool { return clock.Pending() == 1 })
	iv.Invalidate("n2", 1)
	iv.Invalidate("n3", 1)
	iv.Invalidate("critical", 1)

	for i := 0; i < 4; i++ {
		clock.Advance()
		eventually(t, "next load", func() bool { return rec.count() >= i+1 })
	}
	want := []string{"n1", "critical", "n2", "n3"}
	got := rec.order()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("load order = %v, want %v", got, want)
		}
	}
}

// 限速刷新：不推进时钟就不发生新的加载。
func TestRateLimitedRefresh(t *testing.T) {
	clock := &manualClock{}
	rec := &loadRecorder{}
	iv := New(Options{
		Load:     rec.load,
		After:    clock.After,
		Interval: 50 * time.Millisecond,
	})
	defer iv.Close()

	iv.Invalidate("a", 1)
	iv.Invalidate("b", 1)
	iv.Invalidate("c", 1)
	eventually(t, "limiter armed", func() bool { return clock.Pending() == 1 })
	if got := rec.count(); got != 0 {
		t.Fatalf("load happened before the interval elapsed: %d", got)
	}
	clock.Advance()
	eventually(t, "one load", func() bool { return rec.count() == 1 })
	if got := rec.count(); got != 1 {
		t.Fatalf("rate limiter allowed %d loads after one tick", got)
	}
	clock.Advance()
	eventually(t, "two loads", func() bool { return rec.count() == 2 })
}

// 失败可注入并重试：加载失败调用 OnError，键保持失效并稍后重试。
func TestLoadErrorIsReportedAndRetried(t *testing.T) {
	boom := errors.New("boom")
	var mu sync.Mutex
	var errs []error
	calls := 0

	iv := New(Options{
		Load: func(context.Context, string) (any, error) {
			mu.Lock()
			calls++
			fail := calls == 1
			mu.Unlock()
			if fail {
				return nil, boom
			}
			return "recovered", nil
		},
		OnError: func(_ string, err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		},
	})
	defer iv.Close()

	iv.Invalidate("k", 1)
	eventually(t, "recovered value", func() bool {
		v, ok := iv.Get("k")
		return ok && v == "recovered"
	})
	mu.Lock()
	defer mu.Unlock()
	if len(errs) != 1 || !errors.Is(errs[0], boom) {
		t.Fatalf("OnError got %v, want [boom]", errs)
	}
}

// 验证资源释放：Close 后后台 goroutine 退出。
func TestCloseReleasesResources(t *testing.T) {
	// Warm up so runtime noise settles.
	New(Options{Load: func(context.Context, string) (any, error) { return 1, nil }}).Close()
	time.Sleep(20 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	iv := New(Options{Load: func(context.Context, string) (any, error) { return 1, nil }})
	iv.Invalidate("x", 1)
	eventually(t, "refreshed", func() bool {
		_, ok := iv.Get("x")
		return ok
	})
	iv.Close()

	eventually(t, "goroutines released", func() bool {
		return runtime.NumGoroutine() <= baseline
	})
}

// 未知键与脏键都不可读。
func TestGetMissesUnknownAndDirtyKeys(t *testing.T) {
	iv := New(Options{Load: func(context.Context, string) (any, error) { return 1, nil }})
	defer iv.Close()
	if _, ok := iv.Get("nope"); ok {
		t.Fatal("unknown key served")
	}
	iv.Invalidate("dirty", 1)
	if _, ok := iv.Get("dirty"); ok {
		t.Fatal("dirty key served")
	}
}
