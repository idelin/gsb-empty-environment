// 运行样例：演示代际缓存的核心语义。
package main

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"gencache"
)

// 可注入的模拟数据源（代替远程缓存/数据库）。
func makeSource(latency map[string]time.Duration) (gencache.Source[string, string], *sync.Map) {
	var calls sync.Map
	return func(key string) (string, error) {
		n, _ := calls.LoadOrStore(key, 0)
		calls.Store(key, n.(int)+1)
		if key == "flaky" && n.(int) == 0 {
			return "", errors.New("injected failure")
		}
		time.Sleep(latency[key])
		return fmt.Sprintf("value(%s)#%d", key, n.(int)+1), nil
	}, &calls
}

func main() {
	// 可注入时钟。
	var clockMu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	fakeNow := func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }

	src, calls := makeSource(map[string]time.Duration{"slow": 150 * time.Millisecond, "slow2": 150 * time.Millisecond})
	c := gencache.New(gencache.Options[string, string]{
		Source:  src,
		Now:     fakeNow,
		TTL:     time.Minute,
		OnError: func(k string, err error) { fmt.Printf("[OnError] key=%s err=%v\n", k, err) },
	})

	fmt.Println("== 1. 基本加载与缓存命中 ==")
	v, _ := c.Get("a")
	fmt.Println("first Get(a):", v)
	v, _ = c.Get("a")
	fmt.Println("second Get(a):", v, "(来自缓存)")

	fmt.Println("\n== 2. 加载中 Invalidate：旧结果不复活，新加载立即启动 ==")
	oldDone := make(chan string, 1)
	go func() { v, _ := c.Get("slow"); oldDone <- v }()
	time.Sleep(20 * time.Millisecond) // 旧加载进行中
	c.Invalidate("slow")
	newDone := make(chan string, 1)
	go func() { v, _ := c.Get("slow"); newDone <- v }() // 不等旧加载结束，立即启动新加载
	fmt.Println("new load result:", <-newDone)
	fmt.Println("stale waiter still receives:", <-oldDone)
	v, _ = c.Get("slow")
	fmt.Println("cached after stale completion:", v, "(仍是新值)")

	fmt.Println("\n== 3. 加载中 Clear：旧结果不入缓存 ==")
	clearDone := make(chan string, 1)
	go func() { v, _ := c.Get("slow2"); clearDone <- v }()
	time.Sleep(20 * time.Millisecond) // 旧加载进行中
	c.Clear()
	v, _ = c.Get("slow2") // 不等旧加载结束，立即启动新加载
	fmt.Println("after Clear Get(slow2):", v)
	fmt.Println("stale waiter receives:", <-clearDone, "(但未写入缓存)")
	v, _ = c.Get("slow2")
	fmt.Println("cached after stale completion:", v, "(仍是新值)")

	fmt.Println("\n== 4. 独立键互不阻塞 ==")
	go c.Get("slow")
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	v, _ = c.Get("fast")
	fmt.Printf("Get(fast)=%s 耗时 %v（未被 slow 阻塞）\n", v, time.Since(start).Round(time.Millisecond))

	fmt.Println("\n== 5. 注入时钟 + TTL 过期 ==")
	v, _ = c.Get("session")
	fmt.Println("Get(session):", v)
	clockMu.Lock()
	now = now.Add(2 * time.Minute)
	clockMu.Unlock()
	v, _ = c.Get("session")
	fmt.Println("时钟前进 2min 后 Get(session):", v, "(已重新加载)")

	fmt.Println("\n== 6. 注入失败与重试 ==")
	if _, err := c.Get("flaky"); err != nil {
		fmt.Println("Get(flaky) err:", err)
	}
	v, _ = c.Get("flaky")
	fmt.Println("retry Get(flaky):", v)

	fmt.Println("\n== 数据源调用次数 ==")
	calls.Range(func(k, n any) bool {
		fmt.Printf("  %s: %d 次\n", k, n)
		return true
	})
}
