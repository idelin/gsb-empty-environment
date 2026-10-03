package ttlcache_test

import (
	"fmt"
	"time"

	"ttlcache"
)

// Example 演示：带绝对过期与滑动过期的 LRU 缓存的基本用法。
func Example() {
	c := ttlcache.New(2) // 容量上限 2
	defer c.Close()

	c.Set("session", "token-1", 100*time.Millisecond, 40*time.Millisecond)
	c.Set("config", 42, 100*time.Millisecond, 0)

	if v, ok := c.Get("session"); ok {
		fmt.Println("hit:", v)
	}

	// 容量超限，最冷的对象被逐出
	c.Set("third", true, 100*time.Millisecond, 0)
	if _, ok := c.Get("config"); !ok {
		fmt.Println("config evicted (coldest)")
	}

	// 等待超过绝对过期时间后，读取立即返回空
	time.Sleep(150 * time.Millisecond)
	if _, ok := c.Get("session"); !ok {
		fmt.Println("session expired")
	}
	// Output:
	// hit: token-1
	// config evicted (coldest)
	// session expired
}
