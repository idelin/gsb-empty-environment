// 运行样例：演示续期代际、惰性失效、批量续期、时钟跳跃与堆压缩上界。
package main

import (
	"fmt"
	"sync"
	"time"

	"ttlcache"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func main() {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := ttlcache.New(ttlcache.Options{
		Now: clock.Now,
		OnEvict: func(key string, _ any, r ttlcache.EvictReason) {
			fmt.Printf("  [evict] key=%-4s reason=%s\n", key, r)
		},
	})

	fmt.Println("== 1. 写入三个键，TTL 10s ==")
	c.Set("a", 1, 10*time.Second)
	c.Set("b", 2, 10*time.Second)
	c.Set("c", 3, 10*time.Second)
	fmt.Printf("  items=%d heap=%d\n", c.Len(), c.HeapLen())

	fmt.Println("== 2. t+5s 续期 a 与 b（批量），c 不续 ==")
	clock.Advance(5 * time.Second)
	c.RenewBatch([]string{"a", "b"}, 10*time.Second)
	fmt.Printf("  items=%d heap=%d（含 2 个惰性失效节点）\n", c.Len(), c.HeapLen())

	fmt.Println("== 3. 时钟跳到 t+10s：只有 c 确实过期 ==")
	clock.Advance(5 * time.Second)
	fmt.Printf("  expired=%d items=%d\n", c.Expire(), c.Len())

	fmt.Println("== 4. 时钟回拨 1 小时：不删任何东西（明确策略） ==")
	clock.Advance(-time.Hour)
	fmt.Printf("  expired=%d items=%d\n", c.Expire(), c.Len())

	fmt.Println("== 5. 快进到 a/b 新截止 t+15s，恰好到点即失效（半开语义） ==")
	clock.Advance(time.Hour) // 回到 t+10s
	clock.Advance(5 * time.Second)
	fmt.Printf("  expired=%d items=%d heap=%d\n", c.Expire(), c.Len(), c.HeapLen())

	fmt.Println("== 6. 同键续期 1000 次：堆有明确上界 ==")
	c.Set("hot", 0, time.Minute)
	maxHeap := 0
	for i := 0; i < 1000; i++ {
		clock.Advance(time.Second)
		c.Renew("hot", time.Minute)
		c.Expire()
		if h := c.HeapLen(); h > maxHeap {
			maxHeap = h
		}
	}
	fmt.Printf("  1000 次续期后: heap 峰值=%d（上界 2*1+3=5） compactions=%d staleDropped=%d\n",
		maxHeap, c.Compactions, c.StaleDropped)

	fmt.Println("== 7. 数据源回源与失败注入 ==")
	loaded := 0
	d := ttlcache.New(ttlcache.Options{
		Now: clock.Now,
		Loader: func(key string) (any, time.Duration, error) {
			loaded++
			if key == "bad" {
				return nil, 0, fmt.Errorf("source unavailable")
			}
			return "value-of-" + key, time.Minute, nil
		},
	})
	v, _ := d.Get("x")
	fmt.Printf("  Get(x)=%v loaderCalls=%d\n", v, loaded)
	d.Get("x")
	fmt.Printf("  再 Get(x) 命中缓存 loaderCalls=%d\n", loaded)
	if _, err := d.Get("bad"); err != nil {
		fmt.Printf("  Get(bad) 失败: %v；items=%d heap=%d（无残留状态）\n", err, d.Len(), d.HeapLen())
	}
}
