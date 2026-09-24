// cacheaudit 演示：记录有界缓存事件日志，审计线性一致性，
// 区分确定陈旧与证据不足，并对违规历史做最小化。
package main

import (
	"fmt"

	"cacheaudit"
)

// scriptedClock 是可注入的确定性时钟。
func scriptedClock() func() int64 {
	var t int64
	return func() int64 { t++; return t }
}

func printEvents(title string, events []cacheaudit.Event) {
	fmt.Printf("--- %s ---\n", title)
	for _, ev := range events {
		fmt.Println(" ", ev)
	}
}

func main() {
	// 场景 1：健康缓存，命中/失效/加载/发布/淘汰全部落日志。
	fmt.Println("== scenario 1: healthy cache ==")
	versions := map[string]uint64{"a": 1, "b": 1}
	loader := func(key string) (string, uint64, error) {
		return fmt.Sprintf("value-%s-v%d", key, versions[key]), versions[key], nil
	}
	released := false
	c := cacheaudit.NewCache(64, loader, scriptedClock(), func() error {
		released = true
		return nil
	})
	c.Get("a")     // 失效 + 加载 v1（未发布，不算命中）
	c.Get("a")     // 仍未发布：再次失效
	c.Publish("a") // 发布 v1
	c.Get("a")     // 命中 v1
	c.Get("b")     // 失效 + 加载
	c.Publish("b")
	c.Get("b") // 命中
	c.Evict("b")
	c.Get("b") // 淘汰后失效 + 重新加载
	stats := c.Stats()
	fmt.Printf("stats: hits=%d misses=%d hitRate=%.2f (denominator=hits+misses=%d)\n",
		stats.Hits, stats.Misses, stats.HitRate(), stats.Total())
	printEvents("event log", c.Log().Events())
	rep := cacheaudit.Audit(c.Log().Events(), c.Log().Dropped())
	fmt.Println("audit:", rep)
	if err := c.Close(); err != nil {
		fmt.Println("close error:", err)
	}
	c.Close() // 幂等：资源只释放一次
	fmt.Println("resource released:", released)

	// 场景 2：数据源升级到 v2 并发布后，故障组件仍返回 v1 —— 确定陈旧。
	fmt.Println("\n== scenario 2: stale return after newer publish ==")
	c2 := cacheaudit.NewCache(64, loader, scriptedClock(), nil)
	defer c2.Close()
	c2.Get("a")       // 加载 v1
	c2.Publish("a")   // 发布 v1
	c2.Get("a")       // 命中 v1
	c2.Evict("a")     // 淘汰
	versions["a"] = 2 // 数据源升级
	c2.Get("a")       // 加载 v2
	c2.Publish("a")   // 发布 v2
	// 模拟故障组件：在 v2 已发布后写入一条返回 v1 的命中记录，
	// 来源指向真实的 v1 加载事件，保留来源版本证据。
	log := c2.Log()
	var v1LoadSeq uint64
	for _, ev := range log.Events() {
		if ev.Kind == cacheaudit.KindLoad && ev.Version == 1 {
			v1LoadSeq = ev.Seq
		}
	}
	stale := log.Append(cacheaudit.KindHit, "a", 1,
		fmt.Sprintf("load:%d", v1LoadSeq), "", 100, 101)
	fmt.Println("injected stale hit:", stale)
	rep2 := cacheaudit.Audit(log.Events(), log.Dropped())
	fmt.Println("audit:", rep2)
	min := cacheaudit.Minimize(log.Events())
	printEvents(fmt.Sprintf("minimized stale subset (%d of %d events)", len(min), len(log.Events())), min)
	fmt.Println("audit(minimized):", cacheaudit.Audit(min, 0))

	// 场景 3：环形缓冲容量不足导致覆盖 —— 必须报告证据不足。
	fmt.Println("\n== scenario 3: ring overwrite loses evidence ==")
	c3 := cacheaudit.NewCache(4, loader, scriptedClock(), nil)
	defer c3.Close()
	for i := 0; i < 6; i++ {
		c3.Get("k")
	}
	fmt.Printf("log dropped %d events (capacity 4)\n", c3.Log().Dropped())
	rep3 := cacheaudit.Audit(c3.Log().Events(), c3.Log().Dropped())
	fmt.Println("audit:", rep3)
}
