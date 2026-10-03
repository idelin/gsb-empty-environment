# ttlru — 分层时间感知（TTL/TTD）LRU 双重索引缓存

纯 Go 标准库实现，支持**绝对过期（TTL/TTD）**与**滑动过期（TTI）**的并发安全 LRU 缓存。

## 设计：每个条目挂在两条索引上

```
        ┌─────────────────── Cache ───────────────────┐
        │                                             │
        │   map[string]*entry  ──  O(1) 按键定位       │
        │                                             │
        │   LRU 双向链表        ──  队首最热/队尾最冷   │
        │   (container/list)       Get/Set 命中 O(1)   │
        │                          提升到队首           │
        │                                             │
        │   到期小顶堆          ──  堆顶 = 最早死亡     │
        │   (container/heap)     批量剔除 O(k·log n)   │
        │                                             │
        └─────────────────────────────────────────────┘
```

- **生效到期时间** = min(绝对过期时间, 最近访问时间 + 滑动窗口)，两者可单独或同时设置。
- **热度提升**：`Get`/`Set` 命中时 `list.MoveToFront`，严格 O(1)；滑动续期后 `heap.Fix` 修正堆序。
- **批量剔除**：后台 janitor 定时器 + 写路径顺带触发，从堆顶连续弹出已过期死对象；读路径发现过期立即返回空并同步摘除。
- **三索引同步摘除**：map、链表、堆同时删除，堆 `Pop` 时置 nil 释放引用，保证内存零泄漏。
- 时钟可注入（`c.now`），逻辑测试用假时钟完全确定化。

## API

```go
c := ttlru.New(ttlru.WithCapacity(1000), ttlru.WithJanitor(5*time.Millisecond))
defer c.Close()

c.Set("k", v, 50*time.Millisecond, 0)  // 绝对过期 50ms
c.Set("k", v, 0, 30*time.Millisecond)  // 滑动过期：30ms 无访问则死
c.Set("k", v, time.Minute, 30*time.Millisecond) // 两者取较早者
v, ok := c.Get("k")   // 过期立即返回 (nil, false)
n := c.ReapExpired()  // 手动批量回收
```

## 运行样例

```
$ go run ./cmd/demo
[t=0]    插入 100 个对象（80 冷 TTL 10~100ms + 20 热滑动 60ms），Len=100
[t=150ms] 冷对象读取命中 0/80（超时立即返回空）
[t=150ms] 热对象读取命中 20/20（高频访问滑动续命），Len=20
[回收后] 后台批量回收完成，Len=0（内存零泄漏）
[统计]   {Hits:722621 Misses:80 Expired:0 Evicted:0 Reaped:100}
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestSetGet
--- PASS: TestSetGet (0.00s)
=== RUN   TestAbsoluteExpiration
--- PASS: TestAbsoluteExpiration (0.00s)
=== RUN   TestSlidingExpiration
--- PASS: TestSlidingExpiration (0.00s)
=== RUN   TestAbsoluteBeatsSliding
--- PASS: TestAbsoluteBeatsSliding (0.00s)
=== RUN   TestLRUHeatPromotionAndEviction
--- PASS: TestLRUHeatPromotionAndEviction (0.00s)
=== RUN   TestBatchReap
--- PASS: TestBatchReap (0.00s)
=== RUN   TestJanitorReclaimsInBackground
--- PASS: TestJanitorReclaimsInBackground (0.12s)
=== RUN   TestAcceptance
--- PASS: TestAcceptance (0.27s)
    stats: {Hits:408602 Misses:80 Expired:0 Evicted:0 Reaped:100}
=== RUN   TestCloseIdempotent
--- PASS: TestCloseIdempotent (0.00s)
PASS
ok  	ttlru	1.011s

$ go test -race ./...
ok  	ttlru	2.467s
```

- `TestAcceptance` 即验收边界：100 个对象、10ms~100ms 不等 TTL、高频访问滑动续命、
  超时读取立即返回空、后台批量回收、map/链表/堆三索引清零验证内存零泄漏。
- 逻辑测试（过期/续期/LRU 淘汰/批量回收）使用注入假时钟，完全确定、无 sleep 依赖。
