package leasecache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// resource is a fake pooled object (e.g. a connection) with an ID so tests
// can tell instances apart.
type resource struct{ id int }

// harness builds a cache with counting loader/destroyer and a fake clock.
type harness struct {
	cache    *Cache[string, *resource]
	now      *fakeClock
	loads    atomic.Int32
	destroys atomic.Int32
	loadErr  atomic.Pointer[error]
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time          { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *fakeClock) Advance(d time.Duration) { f.mu.Lock(); defer f.mu.Unlock(); f.now = f.now.Add(d) }

func newHarness(t *testing.T, ttl time.Duration) *harness {
	t.Helper()
	h := &harness{now: &fakeClock{now: time.Unix(1_700_000_000, 0)}}
	var ids atomic.Int32
	h.cache = New(Options[string, *resource]{
		Loader: func(ctx context.Context, key string) (*resource, error) {
			if errp := h.loadErr.Load(); errp != nil {
				return nil, *errp
			}
			h.loads.Add(1)
			return &resource{id: int(ids.Add(1))}, nil
		},
		Destroy: func(r *resource) { h.destroys.Add(1) },
		TTL:     ttl,
		Now:     h.now.Now,
	})
	return h
}

func mustGet(t *testing.T, c *Cache[string, *resource], key string) *Lease[string, *resource] {
	t.Helper()
	l, err := c.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q) failed: %v", key, err)
	}
	return l
}

// 借用期间淘汰只取消缓存持有，不破坏借用者；归还后才回收。
func TestEvictKeepsBorrowerAlive(t *testing.T) {
	h := newHarness(t, 0)
	defer h.cache.Close()

	lease := mustGet(t, h.cache, "a")
	obj := lease.Value()

	h.cache.Evict("a")
	if got := h.destroys.Load(); got != 0 {
		t.Fatalf("evict destroyed a borrowed object, destroys=%d", got)
	}
	if lease.Value() != obj {
		t.Fatal("borrower's object changed after evict")
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if got := h.destroys.Load(); got != 1 {
		t.Fatalf("object not recycled after last release, destroys=%d", got)
	}
}

// 淘汰后仍被借用的对象不可复用给其他调用：新调用必须拿到新对象。
func TestEvictedBorrowedObjectNotReused(t *testing.T) {
	h := newHarness(t, 0)
	defer h.cache.Close()

	first := mustGet(t, h.cache, "a")
	h.cache.Evict("a")

	second := mustGet(t, h.cache, "a")
	if second.Value() == first.Value() {
		t.Fatal("evicted borrowed object was reused for another caller")
	}
	if h.loads.Load() != 2 {
		t.Fatalf("expected 2 loads, got %d", h.loads.Load())
	}
	_ = first.Release()
	_ = second.Release()
	h.cache.Evict("a") // drop the cache's hold on the second object
	if h.destroys.Load() != 2 {
		t.Fatalf("expected 2 destroys, got %d", h.destroys.Load())
	}
}

// 重复归还必须被拒绝，且不会重复回收。
func TestDoubleReleaseRejected(t *testing.T) {
	h := newHarness(t, 0)
	defer h.cache.Close()

	lease := mustGet(t, h.cache, "a")
	if err := lease.Release(); err != nil {
		t.Fatalf("first Release failed: %v", err)
	}
	if err := lease.Release(); !errors.Is(err, ErrDoubleRelease) {
		t.Fatalf("second Release = %v, want ErrDoubleRelease", err)
	}
	h.cache.Evict("a")
	if got := h.destroys.Load(); got != 1 {
		t.Fatalf("double release caused extra recycle, destroys=%d", got)
	}
}

// 加载失败不泄漏池对象：缓存为空、无对象被创建，恢复后可正常加载。
func TestLoadFailureNoLeak(t *testing.T) {
	h := newHarness(t, 0)
	defer h.cache.Close()

	boom := errors.New("backend down")
	h.loadErr.Store(&boom)
	if _, err := h.cache.Get(context.Background(), "a"); !errors.Is(err, boom) {
		t.Fatalf("Get error = %v, want %v", err, boom)
	}
	if got := h.cache.Len(); got != 0 {
		t.Fatalf("failed load left pool object behind, len=%d", got)
	}
	if h.loads.Load() != 0 || h.destroys.Load() != 0 {
		t.Fatalf("failed load created/recycled objects: loads=%d destroys=%d",
			h.loads.Load(), h.destroys.Load())
	}

	h.loadErr.Store(nil)
	lease := mustGet(t, h.cache, "a")
	_ = lease.Release()
	if h.loads.Load() != 1 {
		t.Fatalf("expected retry to load once, loads=%d", h.loads.Load())
	}
}

// 注入式失败函数：不经过数据源即可模拟故障，且不触碰池。
func TestInjectableFailure(t *testing.T) {
	injected := errors.New("injected fault")
	fail := atomic.Bool{}
	fail.Store(true)
	c := New(Options[string, *resource]{
		Loader: func(ctx context.Context, key string) (*resource, error) {
			t.Fatal("loader must not be called when fault is injected")
			return nil, nil
		},
		Failer: func(op Op) error {
			if op == OpLoad && fail.Load() {
				return injected
			}
			return nil
		},
	})
	defer c.Close()

	if _, err := c.Get(context.Background(), "x"); !errors.Is(err, injected) {
		t.Fatalf("Get error = %v, want injected fault", err)
	}
	if c.Len() != 0 {
		t.Fatal("injected failure leaked a pool object")
	}
}

// TTL 过期使用注入时钟；过期对象在借用期间不被回收，新调用拿到新对象。
func TestTTLExpiryWithFakeClock(t *testing.T) {
	h := newHarness(t, time.Minute)
	defer h.cache.Close()

	old := mustGet(t, h.cache, "a")
	h.now.Advance(2 * time.Minute)

	fresh := mustGet(t, h.cache, "a") // lazily expires the old entry
	if fresh.Value() == old.Value() {
		t.Fatal("expired borrowed object was reused")
	}
	if got := h.destroys.Load(); got != 0 {
		t.Fatalf("expired but borrowed object destroyed early, destroys=%d", got)
	}
	_ = old.Release()
	if got := h.destroys.Load(); got != 1 {
		t.Fatalf("old object not recycled after release, destroys=%d", got)
	}
	_ = fresh.Release()
}

// 关闭时未借出的对象立即回收，仍借出的对象延迟到归还时回收。
func TestCloseRecyclesDelayedObjects(t *testing.T) {
	h := newHarness(t, 0)

	borrowed := mustGet(t, h.cache, "held")
	idle := mustGet(t, h.cache, "idle")
	_ = idle.Release() // returned to cache, still cached

	h.cache.Close()
	if got := h.destroys.Load(); got != 1 {
		t.Fatalf("Close should recycle only the idle object, destroys=%d", got)
	}
	if _, err := h.cache.Get(context.Background(), "x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close = %v, want ErrClosed", err)
	}
	if err := borrowed.Release(); err != nil {
		t.Fatalf("borrower release after Close failed: %v", err)
	}
	h.cache.Wait() // blocks until every delayed object is recycled
	if got := h.destroys.Load(); got != 2 {
		t.Fatalf("delayed object not recycled after release, destroys=%d", got)
	}
	if h.loads.Load() != h.destroys.Load() {
		t.Fatalf("resource leak: loads=%d destroys=%d", h.loads.Load(), h.destroys.Load())
	}
}

// 并发压力测试（配合 -race）：所有加载的对象最终都被回收，无泄漏。
func TestConcurrentNoLeak(t *testing.T) {
	h := newHarness(t, 0)

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", g%4)
			for i := 0; i < 200; i++ {
				lease, err := h.cache.Get(context.Background(), key)
				if err != nil {
					t.Errorf("Get failed: %v", err)
					return
				}
				if i%7 == 0 {
					h.cache.Evict(key)
				}
				if err := lease.Release(); err != nil {
					t.Errorf("Release failed: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	h.cache.Close()
	h.cache.Wait()

	if h.loads.Load() != h.destroys.Load() {
		t.Fatalf("resource leak under concurrency: loads=%d destroys=%d",
			h.loads.Load(), h.destroys.Load())
	}
}
