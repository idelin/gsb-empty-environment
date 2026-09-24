package ttlcache

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

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

func (f *fakeClock) set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t
}

// 半开语义：[set, expireAt) 内有效，恰好到点即失效。
func TestHalfOpenSemantics(t *testing.T) {
	fc := newFakeClock()
	c := New(Options{Now: fc.now})
	t0 := fc.now()
	c.Set("k", "v", 10*time.Second)

	fc.set(t0.Add(10*time.Second - time.Nanosecond))
	if _, err := c.Get("k"); err != nil {
		t.Fatalf("截止前 1ns 应命中: %v", err)
	}
	fc.set(t0.Add(10 * time.Second)) // 恰好到点
	if _, err := c.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("恰好到点应失效: %v", err)
	}
	if c.Len() != 0 {
		t.Fatalf("过期读取应就地清理, Len=%d", c.Len())
	}
}

// 续期后旧定时事件不得删除续期项（代际校验）。
func TestRenewStaleTimerMustNotDelete(t *testing.T) {
	fc := newFakeClock()
	c := New(Options{Now: fc.now})
	t0 := fc.now()
	c.Set("k", "v1", 10*time.Second)

	fc.set(t0.Add(5 * time.Second))
	if !c.Renew("k", 10*time.Second) { // 新截止 t0+15
		t.Fatal("renew should succeed")
	}
	fc.set(t0.Add(10 * time.Second)) // 旧事件的触发点
	if n := c.Expire(); n != 0 {
		t.Fatalf("旧定时事件不得删除续期项, expired=%d", n)
	}
	if v, err := c.Get("k"); err != nil || v != "v1" {
		t.Fatalf("续期项应存活, v=%v err=%v", v, err)
	}
	if c.StaleDropped != 1 {
		t.Fatalf("应丢弃 1 个惰性失效节点, got %d", c.StaleDropped)
	}
	fc.set(t0.Add(15 * time.Second)) // 新截止
	if n := c.Expire(); n != 1 {
		t.Fatalf("新截止应触发, expired=%d", n)
	}
}

// 删除后重建：旧代际节点不得误删新条目（双向校验）。
func TestDeleteThenResetOldTimerHarmless(t *testing.T) {
	fc := newFakeClock()
	c := New(Options{Now: fc.now})
	t0 := fc.now()
	c.Set("k", "v1", 10*time.Second)
	c.Delete("k")
	c.Set("k", "v2", 100*time.Second)

	fc.set(t0.Add(10 * time.Second))
	if n := c.Expire(); n != 0 {
		t.Fatalf("旧代际事件不得删除新条目, expired=%d", n)
	}
	if v, err := c.Get("k"); err != nil || v != "v2" {
		t.Fatalf("新条目应存活, v=%v err=%v", v, err)
	}
}

// 批量续期 + 全局时钟跳跃：只删除当时确实过期的版本。
func TestBatchRenewAndClockJump(t *testing.T) {
	fc := newFakeClock()
	c := New(Options{Now: fc.now})
	t0 := fc.now()
	for _, k := range []string{"a", "b", "c"} {
		c.Set(k, k, 10*time.Second)
	}
	fc.set(t0.Add(5 * time.Second))
	if n := c.RenewBatch([]string{"a", "b"}, 10*time.Second); n != 2 {
		t.Fatalf("批量续期应成功 2 个, got %d", n)
	}
	// 全局时钟向前跳跃：越过原截止，但未越过续期截止
	fc.set(t0.Add(10 * time.Second))
	if n := c.Expire(); n != 1 {
		t.Fatalf("只有未续期的 c 确实过期, expired=%d", n)
	}
	if _, err := c.Get("c"); !errors.Is(err, ErrNotFound) {
		t.Fatal("c 应过期")
	}
	for _, k := range []string{"a", "b"} {
		if _, err := c.Get(k); err != nil {
			t.Fatalf("%s 续期后应存活", k)
		}
	}
	// 再跳过续期截止：a、b 才允许删除
	fc.set(t0.Add(15 * time.Second))
	if n := c.Expire(); n != 2 {
		t.Fatalf("a、b 应同时过期, expired=%d", n)
	}
	if c.Len() != 0 {
		t.Fatalf("全部过期, Len=%d", c.Len())
	}
}

// 时钟回拨策略：不提前删除、不复活已删条目，只按当前时钟即时比较。
func TestClockBackwardJumpPolicy(t *testing.T) {
	fc := newFakeClock()
	c := New(Options{Now: fc.now})
	t0 := fc.now()
	c.Set("k", "v", 10*time.Second)

	fc.set(t0.Add(-time.Hour)) // 回拨
	if n := c.Expire(); n != 0 {
		t.Fatalf("回拨后不得触发过期, expired=%d", n)
	}
	if _, err := c.Get("k"); err != nil {
		t.Fatal("回拨后条目应存活")
	}
	fc.set(t0.Add(10 * time.Second)) // 再次到达截止
	if n := c.Expire(); n != 1 {
		t.Fatalf("到达截止应触发, expired=%d", n)
	}
	fc.set(t0) // 回拨到过期之前也不得复活
	if _, err := c.Get("k"); !errors.Is(err, ErrNotFound) {
		t.Fatal("已过期条目不得因回拨复活")
	}
}

// 同键续期 1000 次：堆不得无限增长，上界 2*L+3。
func TestThousandRenewsHeapBounded(t *testing.T) {
	fc := newFakeClock()
	c := New(Options{Now: fc.now})
	c.Set("hot", 0, time.Minute)

	maxHeap := 0
	for i := 0; i < 1000; i++ {
		fc.advance(time.Second)
		if !c.Renew("hot", time.Minute) {
			t.Fatal("renew failed")
		}
		c.Expire()
		if h := c.HeapLen(); h > maxHeap {
			maxHeap = h
		}
	}
	const bound = 2*1 + 3 // 2*L+3，L=1
	if maxHeap > bound {
		t.Fatalf("堆峰值 %d 超过上界 %d（无压缩时为 1000+）", maxHeap, bound)
	}
	if c.Compactions == 0 {
		t.Fatal("应发生过压缩")
	}
	t.Logf("1000 次续期: heap 峰值=%d, compactions=%d, staleDropped=%d",
		maxHeap, c.Compactions, c.StaleDropped)
}

// 压缩期间新定时器可入队；原子换入不丢事件。
func TestCompactionConcurrentEnqueueNoLoss(t *testing.T) {
	fc := newFakeClock()
	c := New(Options{Now: fc.now})
	c.Set("stable", 1, time.Hour)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for id := 0; id < 4; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				select {
				case <-stop:
					return
				default:
				}
				c.Set(fmt.Sprintf("k%d-%d", id, j), j, time.Hour)
				c.Renew("stable", time.Hour)
			}
		}(id)
	}
	for i := 0; i < 200; i++ {
		c.compact()
	}
	close(stop)
	wg.Wait()
	c.compact() // 收尾压缩

	c.mu.Lock()
	if c.compacting || len(c.pending) != 0 {
		t.Fatalf("压缩应收尾干净: compacting=%v pending=%d", c.compacting, len(c.pending))
	}
	live := 0
	for _, n := range c.timers {
		if e, ok := c.items[n.key]; ok && e.gen == n.gen && e.expireAt == n.expireAt {
			live++
		}
	}
	items := len(c.items)
	c.mu.Unlock()
	if live != items {
		t.Fatalf("每个活条目应恰有一个活定时事件: live=%d items=%d（事件丢失）", live, items)
	}

	// 功能验证：快进到全部过期，所有事件都必须触发。
	fc.advance(2 * time.Hour)
	if n := c.Expire(); n != items {
		t.Fatalf("应触发 %d 个事件, got %d", items, n)
	}
	if c.Len() != 0 {
		t.Fatalf("全部过期后 Len=%d", c.Len())
	}
}

// 独立逐键扫描参考实现，在全部触发点上与堆驱动过期对照。
func TestReferenceCrossCheckAllTriggerPoints(t *testing.T) {
	fc := newFakeClock()
	c := New(Options{Now: fc.now})
	type refEntry struct {
		v   int
		exp int64
	}
	ref := map[string]refEntry{}
	keys := []string{"a", "b", "c", "d", "e"}
	rng := rand.New(rand.NewSource(42))

	check := func(step int) {
		t.Helper()
		now := fc.now().UnixNano()
		// 参考实现：逐键线性扫描，删除当时确实过期的版本
		for k, e := range ref {
			if e.exp <= now {
				delete(ref, k)
			}
		}
		c.mu.Lock()
		got := make(map[string]int, len(c.items))
		for k, e := range c.items {
			got[k] = e.value.(int)
		}
		c.mu.Unlock()
		want := make(map[string]int, len(ref))
		for k, e := range ref {
			want[k] = e.v
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: 堆驱动结果 %v != 逐键扫描参考 %v", step, got, want)
		}
		// 不变量：陈旧节点占用上界 H <= 2L+2
		if h, l := c.HeapLen(), c.Len(); h > 2*l+2 {
			t.Fatalf("step %d: 违反上界 heap=%d > 2*%d+2", step, h, l)
		}
	}

	for step := 0; step < 3000; step++ {
		switch rng.Intn(6) {
		case 0:
			k := keys[rng.Intn(len(keys))]
			ttl := time.Duration(rng.Intn(100)) * time.Second
			v := rng.Int()
			c.Set(k, v, ttl)
			ref[k] = refEntry{v, fc.now().Add(ttl).UnixNano()}
		case 1:
			k := keys[rng.Intn(len(keys))]
			ttl := time.Duration(rng.Intn(100)) * time.Second
			if _, ok := ref[k]; ok && c.Renew(k, ttl) {
				ref[k] = refEntry{ref[k].v, fc.now().Add(ttl).UnixNano()}
			}
		case 2:
			k := keys[rng.Intn(len(keys))]
			c.Delete(k)
			delete(ref, k)
		case 3:
			fc.advance(time.Duration(rng.Intn(50)) * time.Second)
		case 4:
			c.Expire()
			check(step)
		case 5:
			ttl := time.Duration(rng.Intn(100)) * time.Second
			c.RenewBatch(keys, ttl)
			exp := fc.now().Add(ttl).UnixNano()
			for _, k := range keys {
				if e, ok := ref[k]; ok {
					ref[k] = refEntry{e.v, exp}
				}
			}
		}
	}
	c.Expire()
	check(-1)
	t.Logf("对照通过: compactions=%d staleDropped=%d", c.Compactions, c.StaleDropped)
}

// 数据源失败注入：失败不留任何状态，资源释放可验证。
func TestLoaderFailureLeavesNoState(t *testing.T) {
	fc := newFakeClock()
	boom := errors.New("source unavailable")
	calls := 0
	c := New(Options{
		Now: fc.now,
		Loader: func(key string) (any, time.Duration, error) {
			calls++
			if key == "bad" {
				return nil, 0, boom
			}
			return "v-" + key, time.Minute, nil
		},
	})
	if _, err := c.Get("bad"); !errors.Is(err, boom) {
		t.Fatalf("应透传数据源错误, got %v", err)
	}
	if c.Len() != 0 || c.HeapLen() != 0 {
		t.Fatalf("失败不得留状态: items=%d heap=%d", c.Len(), c.HeapLen())
	}
	if v, err := c.Get("ok"); err != nil || v != "v-ok" {
		t.Fatalf("回源应成功, v=%v err=%v", v, err)
	}
	if _, err := c.Get("ok"); err != nil {
		t.Fatal("第二次应命中缓存")
	}
	if calls != 2 {
		t.Fatalf("loader 应只调用 2 次, got %d", calls)
	}
}

// 资源释放：过期/删除/覆盖后条目与堆节点都被回收，回调可观测。
func TestResourceRelease(t *testing.T) {
	fc := newFakeClock()
	type ev struct {
		key    string
		reason EvictReason
	}
	var mu sync.Mutex
	var events []ev
	c := New(Options{
		Now: fc.now,
		OnEvict: func(k string, _ any, r EvictReason) {
			mu.Lock()
			events = append(events, ev{k, r})
			mu.Unlock()
		},
	})
	c.Set("a", 1, 10*time.Second)
	c.Set("b", 2, 10*time.Second)
	c.Set("b", 3, 10*time.Second) // 覆盖 -> ReasonReplaced
	c.Delete("a")                 // -> ReasonDeleted
	c.Set("c", 4, 10*time.Second)
	fc.advance(20 * time.Second)
	if n := c.Expire(); n != 2 { // b, c 过期
		t.Fatalf("expired=%d, want 2", n)
	}
	if c.Len() != 0 {
		t.Fatalf("条目应全部释放, Len=%d", c.Len())
	}
	if h := c.HeapLen(); h != 0 {
		t.Fatalf("堆应排空（含惰性节点）, HeapLen=%d", h)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []ev{{"b", ReasonReplaced}, {"a", ReasonDeleted}, {"b", ReasonExpired}, {"c", ReasonExpired}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("事件序列不符:\n got %v\nwant %v", events, want)
	}
}

// 后台 janitor 冒烟测试（真实时钟）。
func TestJanitor(t *testing.T) {
	c := New(Options{})
	c.Set("k", "v", 20*time.Millisecond)
	stop := c.StartJanitor(10 * time.Millisecond)
	defer stop()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.Len() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("janitor 未能清理过期键")
}
