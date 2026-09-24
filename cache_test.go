package gencache

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 基本语义：首次加载，之后命中缓存，数据源只调用一次。
func TestGetCachesValue(t *testing.T) {
	var calls atomic.Int32
	c := New(Options[string, string]{
		Source: func(k string) (string, error) {
			calls.Add(1)
			return "val-" + k, nil
		},
	})
	for i := 0; i < 3; i++ {
		v, err := c.Get("a")
		if err != nil || v != "val-a" {
			t.Fatalf("Get = %q, %v", v, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("source calls = %d, want 1", calls.Load())
	}
}

// 同键同代的并发 Get 共享一次加载，所有等待者拿到相同结果。
func TestConcurrentWaitersShareSingleLoad(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c := New(Options[string, int]{
		Source: func(k string) (int, error) {
			calls.Add(1)
			<-release
			return 42, nil
		},
	})
	const n = 8
	var wg sync.WaitGroup
	results := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := c.Get("k")
			if err != nil {
				t.Error(err)
			}
			results[i] = v
		}(i)
	}
	// 等加载确实开始后再放行。
	for c.inflightCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("source calls = %d, want 1", calls.Load())
	}
	for i, v := range results {
		if v != 42 {
			t.Fatalf("waiter %d got %d, want 42", i, v)
		}
	}
}

// 加载中 Invalidate：旧结果不写入缓存（不复活）；旧加载的等待者仍拿到旧值；
// 新加载可在旧加载结束前启动并完成；旧加载后完成不得覆盖新值。
func TestInvalidateDuringLoad(t *testing.T) {
	started := make(chan int, 4)
	release := make(map[int]chan struct{})
	var mu sync.Mutex
	var calls atomic.Int32
	mkRelease := func(n int) chan struct{} {
		ch := make(chan struct{})
		mu.Lock()
		release[n] = ch
		mu.Unlock()
		return ch
	}
	c := New(Options[string, string]{
		Source: func(k string) (string, error) {
			n := int(calls.Add(1))
			started <- n
			<-mkRelease(n)
			return fmt.Sprintf("v%d", n), nil
		},
	})

	// 第一次加载（携带一个等待者）。
	oldResult := make(chan string, 2)
	go func() { v, _ := c.Get("k"); oldResult <- v }()
	if n := <-started; n != 1 {
		t.Fatalf("first load = %d", n)
	}
	go func() { v, _ := c.Get("k"); oldResult <- v }() // 等待者加入同一代加载
	time.Sleep(10 * time.Millisecond)

	c.Invalidate("k")

	// 失效后新加载立即启动，不等旧加载结束。
	newResult := make(chan string, 1)
	go func() { v, _ := c.Get("k"); newResult <- v }()
	if n := <-started; n != 2 {
		t.Fatalf("second load = %d", n)
	}
	// 新加载先完成并写入缓存。
	mu.Lock()
	close(release[2])
	mu.Unlock()
	if v := <-newResult; v != "v2" {
		t.Fatalf("new load got %q, want v2", v)
	}
	// 旧加载后完成：等待者仍收到旧值，但不得覆盖缓存。
	mu.Lock()
	close(release[1])
	mu.Unlock()
	for i := 0; i < 2; i++ {
		if v := <-oldResult; v != "v1" {
			t.Fatalf("stale waiter got %q, want v1", v)
		}
	}
	if v, _ := c.Get("k"); v != "v2" {
		t.Fatalf("cached = %q, want v2 (stale load must not resurrect)", v)
	}
	if calls.Load() != 2 {
		t.Fatalf("source calls = %d, want 2", calls.Load())
	}
	if n := c.inflightCount(); n != 0 {
		t.Fatalf("inflight = %d, want 0", n)
	}
}

// 加载中 Clear：旧结果不入缓存；新加载可立即启动。
func TestClearDuringLoad(t *testing.T) {
	started := make(chan int, 4)
	release1 := make(chan struct{})
	var calls atomic.Int32
	c := New(Options[string, string]{
		Source: func(k string) (string, error) {
			n := int(calls.Add(1))
			started <- n
			if n == 1 {
				<-release1
				return "old", nil
			}
			return "new", nil
		},
	})
	done := make(chan string, 1)
	go func() { v, _ := c.Get("k"); done <- v }()
	<-started

	c.Clear()

	// Clear 后新加载立即启动（不等旧加载结束）。
	newDone := make(chan string, 1)
	go func() { v, _ := c.Get("k"); newDone <- v }()
	if n := <-started; n != 2 {
		t.Fatalf("load after Clear = %d", n)
	}
	if v := <-newDone; v != "new" {
		t.Fatalf("load after Clear got %q, want new", v)
	}
	close(release1)
	if v := <-done; v != "old" {
		t.Fatalf("stale waiter got %q, want old", v)
	}
	// 旧结果不得入缓存：当前缓存值必须来自第二次加载。
	if v, _ := c.Get("k"); v != "new" {
		t.Fatalf("cached = %q, want new", v)
	}
	if calls.Load() != 2 {
		t.Fatalf("source calls = %d, want 2", calls.Load())
	}
}

// 独立键互不阻塞：键 a 的加载被挂起时，键 b 的 Get 可立即完成。
func TestKeysAreIndependent(t *testing.T) {
	blockA := make(chan struct{})
	c := New(Options[string, string]{
		Source: func(k string) (string, error) {
			if k == "a" {
				<-blockA
			}
			return "val-" + k, nil
		},
	})
	aStarted := make(chan struct{})
	go func() { close(aStarted); c.Get("a") }()
	<-aStarted
	for c.inflightCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	done := make(chan string, 1)
	go func() { v, _ := c.Get("b"); done <- v }()
	select {
	case v := <-done:
		if v != "val-b" {
			t.Fatalf("Get(b) = %q", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Get(b) blocked by in-flight load of key a")
	}
	close(blockA)
}

// 注入时钟 + TTL：过期后重新加载。
func TestTTLWithInjectedClock(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1000, 0)
	fakeNow := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
	var calls atomic.Int32
	c := New(Options[string, int]{
		Source: func(k string) (int, error) { return int(calls.Add(1)), nil },
		Now:    fakeNow,
		TTL:    10 * time.Second,
	})
	if v, _ := c.Get("k"); v != 1 {
		t.Fatalf("first Get = %d", v)
	}
	advance(5 * time.Second)
	if v, _ := c.Get("k"); v != 1 {
		t.Fatalf("before expiry Get = %d, want cached 1", v)
	}
	advance(6 * time.Second) // 总计 11s，已过期
	if v, _ := c.Get("k"); v != 2 {
		t.Fatalf("after expiry Get = %d, want reloaded 2", v)
	}
}

// 注入失败：错误返回给调用者与等待者，不写入缓存，OnError 被调用，下次 Get 重试。
func TestInjectedFailure(t *testing.T) {
	boom := errors.New("boom")
	var calls atomic.Int32
	var errMu sync.Mutex
	var errs []error
	c := New(Options[string, int]{
		Source: func(k string) (int, error) {
			if calls.Add(1) == 1 {
				return 0, boom
			}
			return 7, nil
		},
		OnError: func(k string, err error) {
			errMu.Lock()
			errs = append(errs, err)
			errMu.Unlock()
		},
	})
	if _, err := c.Get("k"); !errors.Is(err, boom) {
		t.Fatalf("Get err = %v, want boom", err)
	}
	if v, err := c.Get("k"); err != nil || v != 7 {
		t.Fatalf("retry Get = %d, %v", v, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("source calls = %d, want 2", calls.Load())
	}
	errMu.Lock()
	defer errMu.Unlock()
	if len(errs) != 1 || !errors.Is(errs[0], boom) {
		t.Fatalf("OnError errs = %v", errs)
	}
}

// 资源释放：并发加载 + 失效交错结束后，无遗留 inflight、
// 数据源内启动的 goroutine 全部退出、无 goroutine 泄漏。
func TestResourceRelease(t *testing.T) {
	var sourceWG sync.WaitGroup
	var calls atomic.Int32
	c := New(Options[int, int]{
		Source: func(k int) (int, error) {
			calls.Add(1)
			sourceWG.Add(1)
			done := make(chan struct{})
			go func() { defer sourceWG.Done(); <-done }()
			close(done)
			return k * 10, nil
		},
	})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := i % 5
			if _, err := c.Get(key); err != nil {
				t.Error(err)
			}
			if i%3 == 0 {
				c.Invalidate(key)
			}
			if i%10 == 0 {
				c.Clear()
			}
			if _, err := c.Get(key); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	sourceWG.Wait() // 数据源内 goroutine 全部退出
	if n := c.inflightCount(); n != 0 {
		t.Fatalf("inflight = %d, want 0 (resource leak)", n)
	}
	if calls.Load() == 0 {
		t.Fatal("source never called")
	}
}

// 并发压力：随机 Get/Invalidate/Clear 混合，验证无数据竞争与死锁（配合 -race）。
func TestConcurrentMixedStress(t *testing.T) {
	c := New(Options[int, int]{
		Source: func(k int) (int, error) { return k, nil },
	})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := (i + j) % 4
				switch j % 7 {
				case 3:
					c.Invalidate(key)
				case 5:
					c.Clear()
				default:
					if v, err := c.Get(key); err != nil || v != key {
						t.Errorf("Get(%d) = %d, %v", key, v, err)
						return
					}
				}
			}
		}(i)
	}
	wg.Wait()
	if n := c.inflightCount(); n != 0 {
		t.Fatalf("inflight = %d, want 0", n)
	}
}
