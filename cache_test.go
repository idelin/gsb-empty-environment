package gencache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 基本加载与缓存命中：数据源只调用一次。
func TestGetCachesResult(t *testing.T) {
	var calls atomic.Int32
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		calls.Add(1)
		return "v-" + key, nil
	}})
	defer c.Close()

	for i := 0; i < 3; i++ {
		v, err := c.Get(context.Background(), "a")
		if err != nil || v != "v-a" {
			t.Fatalf("Get = %v, %v", v, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("loader called %d times, want 1", calls.Load())
	}
}

// 同一键的并发 Get 共享一次加载，所有等待者收到同一结果。
func TestConcurrentGetSharesLoad(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		calls.Add(1)
		<-release
		return "shared", nil
	}})
	defer c.Close()

	const n = 16
	var wg sync.WaitGroup
	results := make([]any, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := c.Get(context.Background(), "k")
			if err != nil {
				t.Errorf("Get err: %v", err)
			}
			results[i] = v
		}(i)
	}
	// 等所有等待者都挂上后再放行。
	waitFor(t, func() bool { return calls.Load() == 1 })
	close(release)
	wg.Wait()
	for i, v := range results {
		if v != "shared" {
			t.Fatalf("waiter %d got %v", i, v)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("loader called %d times, want 1", calls.Load())
	}
}

// 加载中发生 Invalidate：旧结果不入缓存；等待者仍收到旧加载的结果（语义明确）；
// 新加载可在旧加载结束前启动。
func TestInvalidateDuringLoad(t *testing.T) {
	releaseOld := make(chan struct{})
	var calls atomic.Int32
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		n := calls.Add(1)
		if n == 1 {
			<-releaseOld // 第一次加载阻塞，模拟慢数据源
			return "old", nil
		}
		return fmt.Sprintf("new-%d", n), nil
	}})
	defer c.Close()

	// 等待者加入第一次（旧）加载。
	waiterDone := make(chan any, 1)
	go func() {
		v, _ := c.Get(context.Background(), "k")
		waiterDone <- v
	}()
	waitFor(t, func() bool { return calls.Load() == 1 })

	c.Invalidate("k")

	// 新加载在旧加载结束前即可启动。
	newDone := make(chan any, 1)
	go func() {
		v, _ := c.Get(context.Background(), "k")
		newDone <- v
	}()
	waitFor(t, func() bool { return calls.Load() == 2 })

	select {
	case v := <-newDone:
		if v != "new-2" {
			t.Fatalf("new load got %v", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new load blocked by old load")
	}

	// 放行旧加载：等待者收到旧加载的结果，但旧值不得入缓存。
	close(releaseOld)
	if v := <-waiterDone; v != "old" {
		t.Fatalf("waiter got %v, want old (joined load's result)", v)
	}
	if calls.Load() != 2 {
		t.Fatalf("stale load revived cache: loader called %d times", calls.Load())
	}
	v, _ := c.Get(context.Background(), "k")
	if v != "new-2" {
		t.Fatalf("cache = %v, want new-2 (stale value must not revive)", v)
	}
}

// 加载中发生 Clear：旧结果不入缓存。
func TestClearDuringLoad(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		n := calls.Add(1)
		if n == 1 {
			<-release
			return "stale", nil
		}
		return "fresh", nil
	}})
	defer c.Close()

	done := make(chan struct{})
	go func() { c.Get(context.Background(), "k"); close(done) }()
	waitFor(t, func() bool { return calls.Load() == 1 })

	c.Clear()
	close(release) // 旧加载完成，但全局代际已变，不得写入
	<-done

	v, _ := c.Get(context.Background(), "k")
	if v != "fresh" {
		t.Fatalf("cache = %v, want fresh (cleared load must not be cached)", v)
	}
	if calls.Load() != 2 {
		t.Fatalf("loader called %d times, want 2", calls.Load())
	}
}

// 旧加载在新值缓存之后才完成：不得覆盖新值。
func TestStaleLoadDoesNotOverwriteNewerValue(t *testing.T) {
	releaseOld := make(chan struct{})
	var calls atomic.Int32
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		n := calls.Add(1)
		if n == 1 {
			<-releaseOld
			return "old", nil
		}
		return "new", nil
	}})
	defer c.Close()

	oldDone := make(chan struct{})
	go func() { c.Get(context.Background(), "k"); close(oldDone) }()
	waitFor(t, func() bool { return calls.Load() == 1 })

	c.Invalidate("k")
	if v, _ := c.Get(context.Background(), "k"); v != "new" {
		t.Fatalf("got %v, want new", v)
	}
	close(releaseOld) // 旧加载最后完成
	<-oldDone

	v, _ := c.Get(context.Background(), "k")
	if v != "new" {
		t.Fatalf("cache = %v, want new; stale load overwrote it", v)
	}
	if calls.Load() != 2 {
		t.Fatalf("loader called %d times, want 2", calls.Load())
	}
}

// 独立键互不阻塞：一个键的慢加载不阻塞其他键。
func TestIndependentKeysDoNotBlock(t *testing.T) {
	releaseA := make(chan struct{})
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		if key == "a" {
			<-releaseA
		}
		return "v-" + key, nil
	}})
	defer c.Close()

	go c.Get(context.Background(), "a")

	done := make(chan any, 1)
	go func() { v, _ := c.Get(context.Background(), "b"); done <- v }()
	select {
	case v := <-done:
		if v != "v-b" {
			t.Fatalf("got %v", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("key b blocked by key a's in-flight load")
	}
	close(releaseA)
}

// 每键失效互不影响。
func TestInvalidateIsPerKey(t *testing.T) {
	var calls atomic.Int32
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		calls.Add(1)
		return "v-" + key, nil
	}})
	defer c.Close()

	c.Get(context.Background(), "a")
	c.Get(context.Background(), "b")
	c.Invalidate("a")
	c.Get(context.Background(), "a")
	c.Get(context.Background(), "b") // 仍应命中缓存
	if calls.Load() != 3 {
		t.Fatalf("loader called %d times, want 3 (b must stay cached)", calls.Load())
	}
}

// TTL 过期：使用注入的假时钟。
func TestTTLWithInjectedClock(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	var calls atomic.Int32
	c := New(Options{
		Clock: clock,
		TTL:   time.Minute,
		Loader: func(_ context.Context, key string) (any, error) {
			return calls.Add(1), nil
		},
	})
	defer c.Close()

	v1, _ := c.Get(context.Background(), "k")
	now = now.Add(30 * time.Second)
	v2, _ := c.Get(context.Background(), "k") // 未过期，命中
	now = now.Add(31 * time.Second)
	v3, _ := c.Get(context.Background(), "k") // 已过期，重新加载
	if v1 != v2 || v1 == v3 {
		t.Fatalf("v1=%v v2=%v v3=%v, want v1==v2!=v3", v1, v2, v3)
	}
}

// 失败处理：错误不入缓存、OnError 被调用、下次 Get 重试。
func TestErrorHandling(t *testing.T) {
	boom := errors.New("boom")
	var calls atomic.Int32
	errCh := make(chan error, 1)
	c := New(Options{
		OnError: func(key string, err error) { errCh <- err },
		Loader: func(_ context.Context, key string) (any, error) {
			if calls.Add(1) == 1 {
				return nil, boom
			}
			return "ok", nil
		},
	})
	defer c.Close()

	if _, err := c.Get(context.Background(), "k"); !errors.Is(err, boom) {
		t.Fatalf("Get err = %v, want boom", err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, boom) {
			t.Fatalf("OnError got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("OnError not called")
	}
	// 错误未缓存，下次重试成功。
	if v, err := c.Get(context.Background(), "k"); err != nil || v != "ok" {
		t.Fatalf("retry = %v, %v", v, err)
	}
}

// 资源释放：Close 等待所有在途加载结束；关闭后 Get 返回 ErrClosed。
func TestCloseReleasesResources(t *testing.T) {
	release := make(chan struct{})
	var running atomic.Int32
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		running.Add(1)
		defer running.Add(-1)
		<-release
		return "v", nil
	}})

	const n = 8
	for i := 0; i < n; i++ {
		go c.Get(context.Background(), fmt.Sprintf("k%d", i))
	}
	waitFor(t, func() bool { return running.Load() == int32(n) })

	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while loads still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release) // 放行所有加载
	<-closed
	if running.Load() != 0 {
		t.Fatalf("%d loaders still running after Close", running.Load())
	}
	if _, err := c.Get(context.Background(), "x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Get after Close = %v, want ErrClosed", err)
	}
}

// 等待者上下文取消不影响加载本身与其他等待者。
func TestWaiterContextCancel(t *testing.T) {
	release := make(chan struct{})
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		<-release
		return "v", nil
	}})
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Get(ctx, "k"); done <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	close(release)
	// 加载仍完成并缓存。
	if v, err := c.Get(context.Background(), "k"); err != nil || v != "v" {
		t.Fatalf("got %v, %v", v, err)
	}
}

// 并发压力测试（配合 -race）：随机 Get/Invalidate/Clear 不 panic、不泄露。
func TestConcurrentStress(t *testing.T) {
	c := New(Options{Loader: func(_ context.Context, key string) (any, error) {
		time.Sleep(time.Millisecond)
		return key, nil
	}})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i%8)
			for j := 0; j < 50; j++ {
				switch j % 5 {
				case 3:
					c.Invalidate(key)
				case 4:
					c.Clear()
				default:
					c.Get(context.Background(), key)
				}
			}
		}(i)
	}
	wg.Wait()
	c.Close()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(time.Millisecond)
	}
}
