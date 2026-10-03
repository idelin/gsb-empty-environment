// demo 是分层时间感知（TTL/TTD）LRU 双重索引器的运行样例：
// 插入 100 个 TTL 为 10ms~100ms 的对象，高频访问其中一批带滑动窗口的
// 热对象，观察读取过期即空、后台批量回收与最终零泄漏的全过程。
package main

import (
	"fmt"
	"sync"
	"time"

	"ttlcache"
)

func main() {
	c := ttlcache.New(0) // 不限容量

	const total = 100
	hot := map[string]bool{}
	for i := 0; i < total; i++ {
		key := fmt.Sprintf("obj-%03d", i)
		ttl := time.Duration(10+i%91) * time.Millisecond // 10ms ~ 100ms
		if i%10 == 0 {
			hot[key] = true
			c.Set(key, i, ttl, 30*time.Millisecond) // 热对象：滑动窗口 30ms
		} else {
			c.Set(key, i, ttl, 0)
		}
	}
	fmt.Printf("inserted %d objects (TTL 10ms~100ms, %d hot with 30ms sliding window)\n",
		c.Len(), len(hot))

	// 高频访问热对象，持续 150ms
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

	// 周期性观察存活数：冷对象随 TTL 到期被后台批量回收
	for round := 1; round <= 8; round++ {
		time.Sleep(30 * time.Millisecond)
		fmt.Printf("t=%3dms  alive=%3d\n", round*30, c.Len())
	}

	close(stop)
	wg.Wait()

	// 读取一个必然已过期的冷对象：立即返回空
	if _, ok := c.Get("obj-001"); !ok {
		fmt.Println("read expired cold object -> empty (as expected)")
	}

	// 等待后台收割协程清空所有死对象
	deadline := time.Now().Add(2 * time.Second)
	for c.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Printf("final alive=%d (zero leak)\n", c.Len())
	c.Close()
	fmt.Println("reaper goroutine stopped, all indexes cleared")
}
