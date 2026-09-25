package softcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is the injectable, controllable clock used to explore states.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(0, 0)} }

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	f.mu.Unlock()
}

// waitIdle blocks until no refresh goroutine is running. Test-only.
func (c *Cache) waitIdle() { c.wg.Wait() }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

var bg = context.Background()

// 软期十读只一次刷新：soft window 内 10 个并发读者都拿到旧值，
// 且只触发一次合并的后台刷新。
func TestSoftWindowTenReadsSingleRefresh(t *testing.T) {
	clock := newFakeClock()
	var calls int32
	gate := make(chan struct{})
	src := func(ctx context.Context, key string) (interface{}, uint64, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return "v1", 1, nil
		}
		<-gate // hold the refresh open so all 10 readers share it
		return "v2", 2, nil
	}
	c := New(Config{Source: src, Now: clock.Now, SoftTTL: 10 * time.Second, HardTTL: time.Minute})
	defer c.Close()

	res, err := c.Get(bg, "k")
	if err != nil || res.Value != "v1" || res.Stale {
		t.Fatalf("initial load: %+v, %v", res, err)
	}
	clock.Advance(15 * time.Second) // soft-expired, still hard-valid

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := c.Get(bg, "k")
			if err != nil {
				t.Errorf("stale read: %v", err)
				return
			}
			if !r.Stale || r.Value != "v1" || r.Version != 1 {
				t.Errorf("want stale v1@1, got %+v", r)
			}
			wantUntil := clock.Now().Add(45 * time.Second)
			if !r.UsableUntil.Equal(wantUntil) {
				t.Errorf("UsableUntil = %v, want %v", r.UsableUntil, wantUntil)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(gate) // let the single coalesced refresh land

	waitFor(t, func() bool {
		r, err := c.Get(bg, "k")
		return err == nil && !r.Stale && r.Version == 2 && r.Value == "v2"
	})
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("source called %d times, want 2 (initial + one coalesced refresh)", got)
	}
}

// 硬期不得继续无限用旧值：硬过期后读者等待新值；源失败时返回错误
// 而不是旧值；失败退避抑制重复尝试，但退避不延长硬过期。
func TestHardExpiryNeverServesStaleIndefinitely(t *testing.T) {
	clock := newFakeClock()
	var calls int32
	var fail int32
	src := func(ctx context.Context, key string) (interface{}, uint64, error) {
		n := atomic.AddInt32(&calls, 1)
		if atomic.LoadInt32(&fail) == 1 {
			return nil, 0, errors.New("boom")
		}
		return fmt.Sprintf("v%d", n), uint64(n), nil
	}
	c := New(Config{
		Source:  src,
		Now:     clock.Now,
		SoftTTL: 10 * time.Second,
		HardTTL: 20 * time.Second,
		Backoff: func(int) time.Duration { return 30 * time.Second },
	})
	defer c.Close()

	if r, err := c.Get(bg, "k"); err != nil || r.Value != "v1" {
		t.Fatalf("initial: %+v, %v", r, err)
	}

	clock.Advance(25 * time.Second) // past hard expiry
	r, err := c.Get(bg, "k")
	if err != nil || r.Value != "v2" || r.Stale {
		t.Fatalf("hard path must wait for and return the new value: %+v, %v", r, err)
	}

	atomic.StoreInt32(&fail, 1)
	clock.Advance(25 * time.Second) // past hard expiry again
	before := atomic.LoadInt32(&calls)
	if _, err := c.Get(bg, "k"); err == nil {
		t.Fatal("hard-expired entry with failing source must return an error, not the old value")
	}
	if got := atomic.LoadInt32(&calls); got != before+1 {
		t.Fatalf("first hard miss attempted one refresh, calls = %d, want %d", got, before+1)
	}

	// Backoff window open: further hard misses fail without new attempts
	// and crucially without serving the stale value.
	if _, err := c.Get(bg, "k"); err == nil {
		t.Fatal("backoff must not fall back to the stale value")
	}
	clock.Advance(10 * time.Second) // still inside the 30s backoff
	if _, err := c.Get(bg, "k"); err == nil {
		t.Fatal("still in backoff: must not serve stale")
	}
	if got := atomic.LoadInt32(&calls); got != before+1 {
		t.Fatalf("backoff suppressed retries, calls = %d, want %d", got, before+1)
	}

	// Backoff elapsed: a new attempt is allowed (and fails again).
	clock.Advance(25 * time.Second)
	if _, err := c.Get(bg, "k"); err == nil {
		t.Fatal("source still failing: must not serve stale")
	}
	if got := atomic.LoadInt32(&calls); got != before+2 {
		t.Fatalf("after backoff one retry expected, calls = %d, want %d", got, before+2)
	}
}

// 旧刷新晚回不覆盖更新值：版本更小的迟到响应被丢弃。
func TestLateStaleRefreshDoesNotOverwrite(t *testing.T) {
	clock := newFakeClock()
	var calls int32
	var released int32
	gate := make(chan struct{})
	src := func(ctx context.Context, key string) (interface{}, uint64, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return "new", 2, nil
		}
		<-gate
		return "old", 1, nil // late response carrying an older version
	}
	c := New(Config{
		Source:  src,
		Now:     clock.Now,
		SoftTTL: 10 * time.Second,
		HardTTL: time.Minute,
		Release: func(interface{}) { atomic.AddInt32(&released, 1) },
	})
	defer c.Close()

	if r, err := c.Get(bg, "k"); err != nil || r.Version != 2 {
		t.Fatalf("initial: %+v, %v", r, err)
	}
	clock.Advance(15 * time.Second) // soft window
	r, err := c.Get(bg, "k")        // serves stale, starts background refresh
	if err != nil || !r.Stale || r.Version != 2 {
		t.Fatalf("soft hit: %+v, %v", r, err)
	}
	close(gate)
	c.waitIdle()

	r, err = c.Get(bg, "k")
	if err != nil || r.Value != "new" || r.Version != 2 {
		t.Fatalf("late older response must not overwrite: %+v, %v", r, err)
	}
	if got := atomic.LoadInt32(&released); got != 1 {
		t.Fatalf("discarded late value must be released exactly once, got %d", got)
	}
}

// 旧刷新回包不能复活被删除键：刷新飞行中 Invalidate，迟到的回包被丢弃。
func TestInvalidateDuringRefreshPreventsResurrection(t *testing.T) {
	clock := newFakeClock()
	var calls int32
	gate := make(chan struct{})
	src := func(ctx context.Context, key string) (interface{}, uint64, error) {
		n := atomic.AddInt32(&calls, 1)
		switch n {
		case 1:
			return "v1", 1, nil
		case 2:
			<-gate
			return "ghost", 1, nil // belongs to the deleted generation
		default:
			return "v3", 3, nil
		}
	}
	c := New(Config{Source: src, Now: clock.Now, SoftTTL: 10 * time.Second, HardTTL: 20 * time.Second})
	defer c.Close()

	if _, err := c.Get(bg, "k"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(25 * time.Second) // hard-expired

	errCh := make(chan error, 1)
	go func() {
		_, err := c.Get(bg, "k") // joins the doomed refresh
		errCh <- err
	}()
	waitFor(t, func() bool { return atomic.LoadInt32(&calls) == 2 })

	c.Invalidate("k") // delete while the refresh is in flight
	close(gate)

	if err := <-errCh; err != ErrInvalidated {
		t.Fatalf("waiter of a raced refresh: %v, want ErrInvalidated", err)
	}
	c.waitIdle()

	// The key must not have been resurrected by the late "ghost" value:
	// the next Get fetches from the source again.
	r, err := c.Get(bg, "k")
	if err != nil || r.Value != "v3" || r.Version != 3 {
		t.Fatalf("after invalidation a fresh fetch is required: %+v, %v", r, err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}

// 读者取消：硬路径上一个读者取消不影响共享刷新服务其他读者。
func TestReaderCancellationKeepsSharedRefresh(t *testing.T) {
	clock := newFakeClock()
	var calls int32
	gate := make(chan struct{})
	src := func(ctx context.Context, key string) (interface{}, uint64, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return "v1", 1, nil
		}
		<-gate
		return "v2", 2, nil
	}
	c := New(Config{Source: src, Now: clock.Now, SoftTTL: 10 * time.Second, HardTTL: 20 * time.Second})
	defer c.Close()

	if _, err := c.Get(bg, "k"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(25 * time.Second) // hard-expired

	ctx, cancel := context.WithCancel(bg)
	cancelled := make(chan error, 1)
	go func() {
		_, err := c.Get(ctx, "k")
		cancelled <- err
	}()
	served := make(chan Result, 1)
	go func() {
		r, err := c.Get(bg, "k")
		if err != nil {
			t.Errorf("surviving reader: %v", err)
		}
		served <- r
	}()
	waitFor(t, func() bool { return atomic.LoadInt32(&calls) == 2 })

	cancel()
	if err := <-cancelled; err != context.Canceled {
		t.Fatalf("cancelled reader: %v, want context.Canceled", err)
	}
	close(gate)
	if r := <-served; r.Value != "v2" || r.Version != 2 {
		t.Fatalf("surviving reader got %+v, want v2@2", r)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("shared refresh must run once, calls = %d", got)
	}
}

// 关闭并发：Close 释放所有存储值、等待飞行中的刷新并丢弃其迟到结果。
func TestCloseReleasesAndDrains(t *testing.T) {
	clock := newFakeClock()
	var mu sync.Mutex
	released := map[interface{}]int{}
	release := func(v interface{}) { mu.Lock(); released[v]++; mu.Unlock() }

	var calls int32
	gate := make(chan struct{})
	src := func(ctx context.Context, key string) (interface{}, uint64, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return "stored", 1, nil
		}
		<-gate
		return "late", 2, nil
	}
	c := New(Config{Source: src, Now: clock.Now, SoftTTL: 10 * time.Second, HardTTL: 20 * time.Second, Release: release})

	if _, err := c.Get(bg, "k"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(25 * time.Second) // hard-expired
	waiter := make(chan error, 1)
	go func() {
		_, err := c.Get(bg, "k") // starts an in-flight refresh
		waiter <- err
	}()
	waitFor(t, func() bool { return atomic.LoadInt32(&calls) == 2 })

	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	// Wait until Close has linearized (closed flag set) before letting the
	// in-flight source call return, so its late result must be discarded.
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.closed
	})
	close(gate)
	<-closed

	if err := <-waiter; err != ErrClosed {
		t.Fatalf("waiter racing Close: %v, want ErrClosed", err)
	}
	if _, err := c.Get(bg, "k"); err != ErrClosed {
		t.Fatalf("Get after Close: %v, want ErrClosed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if released["stored"] != 1 || released["late"] != 1 {
		t.Fatalf("every borrowed value must be released exactly once: %v", released)
	}
}

// 可控时钟状态探索：交替推进时钟穿越软/硬窗口，每步核验
// (1) 最多一次共享刷新（源内并发度 <= 1），
// (2) 过期不越界（陈旧结果的可用期限始终在未来），
// (3) 借用资源最终释放（Close 后 acquired == released 且每个恰好一次）。
func TestStateExplorationWithFakeClock(t *testing.T) {
	clock := newFakeClock()
	var inFlight, maxInFlight, acquired, released int32
	var relMu sync.Mutex
	releaseCounts := map[int]int{}

	src := func(ctx context.Context, key string) (interface{}, uint64, error) {
		cur := atomic.AddInt32(&inFlight, 1)
		defer atomic.AddInt32(&inFlight, -1)
		for {
			m := atomic.LoadInt32(&maxInFlight)
			if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
				break
			}
		}
		id := atomic.AddInt32(&acquired, 1)
		return int(id), uint64(id), nil
	}
	release := func(v interface{}) {
		atomic.AddInt32(&released, 1)
		relMu.Lock()
		releaseCounts[v.(int)]++
		relMu.Unlock()
	}
	c := New(Config{
		Source:  src,
		Now:     clock.Now,
		SoftTTL: 10 * time.Second,
		HardTTL: 30 * time.Second,
		Backoff: func(int) time.Duration { return 5 * time.Second },
		Release: release,
	})

	checkBounds := func(r Result) {
		if r.Stale && !clock.Now().Before(r.UsableUntil) {
			t.Errorf("stale value served at/after its usable-until: now=%v until=%v", clock.Now(), r.UsableUntil)
		}
		if r.UsableUntil.Sub(clock.Now()) > 30*time.Second {
			t.Errorf("usable-until exceeds hard TTL bound: %+v", r)
		}
	}

	// Explore: bursts of concurrent readers interleaved with clock
	// advances that alternately land in the soft and hard windows.
	for step := 0; step < 6; step++ {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r, err := c.Get(bg, "k"); err == nil {
					checkBounds(r)
				}
			}()
		}
		wg.Wait()
		c.waitIdle()
		clock.Advance(15 * time.Second)
	}
	c.Invalidate("k")
	c.Close()

	if m := atomic.LoadInt32(&maxInFlight); m > 1 {
		t.Fatalf("at most one shared refresh allowed, observed %d concurrent source calls", m)
	}
	a, r := atomic.LoadInt32(&acquired), atomic.LoadInt32(&released)
	if a != r {
		t.Fatalf("borrowed resources leaked: acquired %d, released %d", a, r)
	}
	relMu.Lock()
	for id, n := range releaseCounts {
		if n != 1 {
			t.Fatalf("value %d released %d times, want exactly 1", id, n)
		}
	}
	relMu.Unlock()
}
