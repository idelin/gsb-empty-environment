package ttlru

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可手动推进的时钟，用于确定性测试。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1700000000, 0)}
}

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func newFakeCache(fc *fakeClock, opts ...Option) *Cache {
	c := New(opts...)
	c.now = fc.now
	return c
}

func TestSetGet(t *testing.T) {
	fc := newFakeClock()
	c := newFakeCache(fc)
	defer c.Close()

	c.Set("a", 1, time.Minute, 0)
	v, ok := c.Get("a")
	if !ok || v.(int) != 1 {
		t.Fatalf("Get(a) = %v, %v; want 1, true", v, ok)
	}
	if _, ok := c.Get("missing"); ok {
		t.Fatal("Get(missing) should miss")
	}
}

func TestAbsoluteExpiration(t *testing.T) {
	fc := newFakeClock()
	c := newFakeCache(fc)
	defer c.Close()

	c.Set("a", 1, 50*time.Millisecond, 0)
	fc.advance(49 * time.Millisecond)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("entry should still be alive before deadline")
	}
	fc.advance(2 * time.Millisecond) // 越过绝对过期点
	if _, ok := c.Get("a"); ok {
		t.Fatal("expired entry must read as empty")
	}
	if c.Len() != 0 {
		t.Fatalf("expired entry should be removed on read, Len = %d", c.Len())
	}
}

func TestSlidingExpiration(t *testing.T) {
	fc := newFakeClock()
	c := newFakeCache(fc)
	defer c.Close()

	// 仅滑动过期：窗口 30ms，每 20ms 访问一次可持续续命
	c.Set("hot", 1, 0, 30*time.Millisecond)
	for i := 0; i < 5; i++ {
		fc.advance(20 * time.Millisecond)
		if _, ok := c.Get("hot"); !ok {
			t.Fatalf("sliding entry died at iteration %d despite fresh access", i)
		}
	}
	// 停止访问，越过滑动窗口后应过期
	fc.advance(31 * time.Millisecond)
	if _, ok := c.Get("hot"); ok {
		t.Fatal("sliding entry should expire after idle window")
	}
}

func TestAbsoluteBeatsSliding(t *testing.T) {
	fc := newFakeClock()
	c := newFakeCache(fc)
	defer c.Close()

	// 绝对 40ms + 滑动 30ms：访问虽续滑动窗口，但绝对到期仍生效
	c.Set("x", 1, 40*time.Millisecond, 30*time.Millisecond)
	fc.advance(20 * time.Millisecond)
	if _, ok := c.Get("x"); !ok {
		t.Fatal("should be alive at 20ms")
	}
	fc.advance(25 * time.Millisecond) // t=45ms > 绝对 40ms
	if _, ok := c.Get("x"); ok {
		t.Fatal("absolute deadline must win over sliding renewal")
	}
}

func TestLRUHeatPromotionAndEviction(t *testing.T) {
	fc := newFakeClock()
	c := newFakeCache(fc, WithCapacity(3))
	defer c.Close()

	c.Set("a", 1, 0, 0)
	c.Set("b", 2, 0, 0)
	c.Set("c", 3, 0, 0)
	// 提升 a 的热度，使 b 成为最冷
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a should hit")
	}
	c.Set("d", 4, 0, 0) // 超容量，应淘汰 b

	if _, ok := c.Get("b"); ok {
		t.Fatal("coldest entry b should be evicted")
	}
	for _, k := range []string{"a", "c", "d"} {
		if _, ok := c.Get(k); !ok {
			t.Fatalf("%s should survive eviction", k)
		}
	}
	if got := c.Stats().Evicted; got != 1 {
		t.Fatalf("Evicted = %d, want 1", got)
	}
}

func TestBatchReap(t *testing.T) {
	fc := newFakeClock()
	c := newFakeCache(fc)
	defer c.Close()

	// 100 个对象，TTL 从 10ms 到 100ms 不等
	// i=0..90 -> TTL 10..100ms；i=91..99 -> TTL 10..18ms（第二轮）
	for i := 0; i < 100; i++ {
		ttl := time.Duration(10+i%91) * time.Millisecond
		c.Set(fmt.Sprintf("k%03d", i), i, ttl, 0)
	}
	if c.Len() != 100 {
		t.Fatalf("Len = %d, want 100", c.Len())
	}

	fc.advance(55 * time.Millisecond)
	// 已过期：第一轮 TTL 10..55ms 共 46 个 + 第二轮 TTL 10..18ms 共 9 个
	if n := c.ReapExpired(); n != 55 {
		t.Fatalf("ReapExpired = %d, want 55", n)
	}
	fc.advance(50 * time.Millisecond)
	if n := c.ReapExpired(); n != 45 {
		t.Fatalf("second ReapExpired = %d, want 45", n)
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d, want 0", c.Len())
	}
}

func TestJanitorReclaimsInBackground(t *testing.T) {
	c := New(WithJanitor(5 * time.Millisecond))
	defer c.Close()

	for i := 0; i < 100; i++ {
		ttl := time.Duration(10+i%91) * time.Millisecond
		c.Set(fmt.Sprintf("k%03d", i), i, ttl, 0)
	}
	deadline := time.Now().Add(5 * time.Second)
	for c.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Len() != 0 {
		t.Fatalf("janitor failed to reclaim all, Len = %d", c.Len())
	}
	if got := c.Stats().Reaped; got != 100 {
		t.Fatalf("Reaped = %d, want 100", got)
	}
}

// TestAcceptance 验收边界：插入 100 个对象，TTL 10ms~100ms 不等；
// 高频访问部分即将过期的对象（滑动续命）；读取时超时立即返回空；
// 后台批量回收；最终内存零泄漏。
func TestAcceptance(t *testing.T) {
	c := New(WithCapacity(200), WithJanitor(5*time.Millisecond))

	// 80 个冷对象：绝对 TTL 10ms~100ms 不等
	for i := 0; i < 80; i++ {
		ttl := time.Duration(10+i%91) * time.Millisecond
		c.Set(fmt.Sprintf("cold-%02d", i), i, ttl, 0)
	}
	// 20 个热对象：滑动过期窗口 60ms，高频访问即可续命
	for i := 0; i < 20; i++ {
		c.Set(fmt.Sprintf("hot-%02d", i), i, 0, 60*time.Millisecond)
	}
	if c.Len() != 100 {
		t.Fatalf("Len = %d, want 100", c.Len())
	}

	// 高频访问热对象，持续 150ms（远超冷对象最长 TTL 100ms）
	// 高频访问热对象：紧凑循环持续命中，不依赖 sleep 计时
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("hot-%02d", i)
			for {
				select {
				case <-stop:
					return
				default:
					if _, ok := c.Get(key); !ok {
						t.Errorf("hot key %s expired while being actively accessed", key)
						return
					}
				}
			}
		}(i)
	}

	// 150ms 后：所有冷对象（最长 TTL 100ms）读取必须立即返回空
	time.Sleep(150 * time.Millisecond)
	for i := 0; i < 80; i++ {
		if _, ok := c.Get(fmt.Sprintf("cold-%02d", i)); ok {
			t.Fatalf("cold-%02d should have expired", i)
		}
	}
	// 热对象因高频访问仍然存活
	for i := 0; i < 20; i++ {
		if _, ok := c.Get(fmt.Sprintf("hot-%02d", i)); !ok {
			t.Fatalf("hot-%02d should survive via sliding renewal", i)
		}
	}

	// 停止访问，热对象在滑动窗口后也应被后台批量回收
	close(stop)
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for c.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// 内存零泄漏：map / LRU 链表 / 小顶堆全部清空
	c.mu.Lock()
	leak := len(c.items) + c.lru.Len() + c.exp.Len()
	c.mu.Unlock()
	if leak != 0 {
		t.Fatalf("memory leak: items+list+heap = %d, want 0", leak)
	}
	c.Close() // 验证 janitor goroutine 可干净退出

	st := c.Stats()
	t.Logf("stats: %+v", st)
	// 读取路径即时剔除由 TestAbsoluteExpiration 确定性覆盖；
	// 此处 Expired 与 janitor 的 Reaped 存在合法竞争，二者居其一即可。
	if st.Hits == 0 || st.Misses < 80 || st.Reaped+st.Expired != 100 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestCloseIdempotent(t *testing.T) {
	c := New(WithJanitor(time.Millisecond))
	c.Close()
	c.Close() // 不应 panic 或死锁
}
