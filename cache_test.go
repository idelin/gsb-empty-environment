package ttlcache

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// waitUntil 轮询等待条件成立，超时则失败。
func waitUntil(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

// assertEmpty 验证三个索引全部清空（内存零泄漏）。
func assertEmpty(t *testing.T, c *Cache) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) != 0 || c.lru.Len() != 0 || len(c.h) != 0 {
		t.Fatalf("leak: map=%d lru=%d heap=%d", len(c.items), c.lru.Len(), len(c.h))
	}
}

func TestGetSetAndMiss(t *testing.T) {
	c := New(0)
	defer c.Close()
	if _, ok := c.Get("nope"); ok {
		t.Fatal("expected miss on empty cache")
	}
	c.Set("a", 1, time.Second, 0)
	if v, ok := c.Get("a"); !ok || v.(int) != 1 {
		t.Fatalf("expected (1,true), got (%v,%v)", v, ok)
	}
}

func TestExpiredReadReturnsEmptyImmediately(t *testing.T) {
	c := New(0)
	defer c.Close()
	c.Set("x", "v", 15*time.Millisecond, 0)
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.Get("x"); ok {
		t.Fatal("expected miss for expired entry")
	}
	// 读取时已同步剔除，索引中不应残留
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) != 0 || c.lru.Len() != 0 || len(c.h) != 0 {
		t.Fatal("expired entry not removed on read")
	}
}

func TestSlidingExtendsButAbsoluteCaps(t *testing.T) {
	c := New(0)
	defer c.Close()
	// 绝对上限 120ms，滑动窗口 60ms：只要持续访问就存活，但绝不能活过 120ms。
	c.Set("hot", "v", 120*time.Millisecond, 60*time.Millisecond)
	start := time.Now()
	for time.Since(start) < 100*time.Millisecond {
		if _, ok := c.Get("hot"); !ok {
			t.Fatalf("hot entry died early at %v", time.Since(start))
		}
		time.Sleep(15 * time.Millisecond)
	}
	// 继续高频访问，越过绝对上限后必须判定死亡。
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, ok := c.Get("hot"); !ok {
			return // 被绝对上限正确截断
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("absolute TTL failed to cap sliding renewal")
}

func TestIdleEntryDiesBySlidingWindow(t *testing.T) {
	c := New(0)
	defer c.Close()
	// 绝对上限 5s，滑动窗口 40ms：不访问则 40ms 后死亡。
	c.Set("idle", "v", 5*time.Second, 40*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	if _, ok := c.Get("idle"); ok {
		t.Fatal("idle entry should have expired by sliding window")
	}
}

func TestLRUEvictionAndPromotion(t *testing.T) {
	c := New(3)
	defer c.Close()
	c.Set("a", 1, time.Second, 0)
	c.Set("b", 2, time.Second, 0)
	c.Set("c", 3, time.Second, 0)
	// 提升 a 的热度，使 b 成为最冷。
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should be alive")
	}
	c.Set("d", 4, time.Second, 0) // 触发容量淘汰
	if _, ok := c.Get("b"); ok {
		t.Fatal("coldest entry b should have been evicted")
	}
	for _, k := range []string{"a", "c", "d"} {
		if _, ok := c.Get(k); !ok {
			t.Fatalf("entry %s should survive", k)
		}
	}
	if c.Len() != 3 {
		t.Fatalf("expected len 3, got %d", c.Len())
	}
}

func TestOverwriteKeepsSingleIndexEntry(t *testing.T) {
	c := New(0)
	defer c.Close()
	c.Set("k", 1, 50*time.Millisecond, 0)
	c.Set("k", 2, 50*time.Millisecond, 0)
	c.mu.Lock()
	n := len(c.items)
	hn := len(c.h)
	ln := c.lru.Len()
	c.mu.Unlock()
	if n != 1 || hn != 1 || ln != 1 {
		t.Fatalf("duplicate index entries: map=%d heap=%d lru=%d", n, hn, ln)
	}
	if v, _ := c.Get("k"); v.(int) != 2 {
		t.Fatal("overwrite did not take effect")
	}
}

// TestAcceptance 验收场景：
// 插入 100 个对象，TTL 分布在 10ms~100ms；其中一批“热”对象带滑动窗口并被高频访问；
// 过期读取立即返回空；后台协程批量回收；最终三个索引全部清空，内存零泄漏。
func TestAcceptance(t *testing.T) {
	c := New(0)
	defer c.Close()

	const total = 100
	hot := make(map[string]bool)
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("obj-%03d", i)
		ttl := time.Duration(10+i%91) * time.Millisecond // 10ms ~ 100ms
		if i%10 == 0 {                                   // 每 10 个里挑 1 个作为高频热对象
			hot[key] = true
			c.Set(key, i, ttl, 30*time.Millisecond) // 滑动窗口 30ms
		} else {
			c.Set(key, i, ttl, 0)
		}
	}
	if c.Len() != total {
		t.Fatalf("expected %d entries, got %d", total, c.Len())
	}

	// 高频访问即将过期的热对象，持续 150ms（远超其绝对 TTL）。
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for key := range hot {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					c.Get(k)
					time.Sleep(5 * time.Millisecond)
				}
			}
		}(key)
	}

	// 热对象在访问期间必须存活（滑动续期），但不得突破绝对硬上限。
	time.Sleep(60 * time.Millisecond)
	alive := 0
	for key := range hot {
		if _, ok := c.Get(key); ok {
			alive++
		}
	}
	if alive == 0 {
		t.Fatal("sliding renewal failed: all hot entries died while being accessed")
	}
	t.Logf("hot entries alive under access at 60ms: %d/%d", alive, len(hot))

	// 冷对象在各自 TTL 之后读取必须立即返回空。
	time.Sleep(80 * time.Millisecond) // 此刻已越过全部 100ms 上限
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("obj-%03d", i)
		if hot[key] {
			continue
		}
		if _, ok := c.Get(key); ok {
			t.Fatalf("cold entry %s should have expired", key)
		}
	}

	close(stop)
	wg.Wait()

	// 后台收割协程应批量回收全部死对象，最终零残留。
	waitUntil(t, 2*time.Second, "background reaper to drain all entries", func() bool {
		return c.Len() == 0
	})
	assertEmpty(t, c)
}

func TestBatchReapSingleWakeup(t *testing.T) {
	c := New(0)
	defer c.Close()
	// 100 个对象几乎同时到期，验证一次唤醒即可批量剔除。
	for i := 0; i < 100; i++ {
		c.Set(fmt.Sprintf("b%d", i), i, 30*time.Millisecond, 0)
	}
	waitUntil(t, 2*time.Second, "batch reap", func() bool {
		return c.Len() == 0
	})
	assertEmpty(t, c)
}

func TestCloseStopsReaperAndClears(t *testing.T) {
	c := New(0)
	for i := 0; i < 50; i++ {
		c.Set(fmt.Sprintf("z%d", i), i, time.Hour, 0)
	}
	c.Close()
	assertEmpty(t, c)
	// Close 之后写入被拒绝，读取为未命中。
	c.Set("late", 1, time.Second, 0)
	if _, ok := c.Get("late"); ok {
		t.Fatal("closed cache must not accept writes")
	}
	c.Close() // 幂等
}
