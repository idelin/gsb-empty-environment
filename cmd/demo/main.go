// demo 复现验收场景：100 个对象、10ms~100ms 不等 TTL、
// 高频访问热对象滑动续命、后台批量回收、最终零泄漏。
package main

import (
	"fmt"
	"sync"
	"time"

	"ttlru"
)

func main() {
	c := ttlru.New(ttlru.WithCapacity(200), ttlru.WithJanitor(5*time.Millisecond))

	// 80 个冷对象：绝对 TTL 10ms~100ms 不等
	for i := 0; i < 80; i++ {
		ttl := time.Duration(10+i%91) * time.Millisecond
		c.Set(fmt.Sprintf("cold-%02d", i), i, ttl, 0)
	}
	// 20 个热对象：滑动过期窗口 60ms
	for i := 0; i < 20; i++ {
		c.Set(fmt.Sprintf("hot-%02d", i), i, 0, 60*time.Millisecond)
	}
	fmt.Printf("[t=0]    插入 100 个对象（80 冷 TTL 10~100ms + 20 热滑动 60ms），Len=%d\n", c.Len())

	// 高频访问热对象
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
					c.Get(key)
				}
			}
		}(i)
	}

	time.Sleep(150 * time.Millisecond)

	// 冷对象读取：超时立即返回空
	coldHits := 0
	for i := 0; i < 80; i++ {
		if _, ok := c.Get(fmt.Sprintf("cold-%02d", i)); ok {
			coldHits++
		}
	}
	fmt.Printf("[t=150ms] 冷对象读取命中 %d/80（超时立即返回空）\n", coldHits)

	// 热对象因滑动续命仍然存活
	hotHits := 0
	for i := 0; i < 20; i++ {
		if _, ok := c.Get(fmt.Sprintf("hot-%02d", i)); ok {
			hotHits++
		}
	}
	fmt.Printf("[t=150ms] 热对象读取命中 %d/20（高频访问滑动续命），Len=%d\n", hotHits, c.Len())

	// 停止访问，等待后台批量回收
	close(stop)
	wg.Wait()
	for c.Len() > 0 {
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Printf("[回收后] 后台批量回收完成，Len=%d（内存零泄漏）\n", c.Len())

	c.Close()
	fmt.Printf("[统计]   %+v\n", c.Stats())
}
