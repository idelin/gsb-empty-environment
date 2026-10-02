# leaserouter — 细粒度分区范围租约路由网关

按 Key Range 划分的分布式分区租约路由器，纯 Go 标准库实现。

## 组件

| 文件 | 职责 |
| --- | --- |
| `range.go` | 半开区间 `[Start, End)`；`End` 为空表示右无界（覆盖到 `Z` 及以后） |
| `lease.go` | 租约：持有节点、区间、TTL、 fencing Epoch、父租约 ID（租约继承链） |
| `node.go` | 存储节点；每节点一把私有互斥锁，慢节点不会阻塞其他节点的请求 |
| `router.go` | 租约表（有序、不重叠）；`Route` 走读锁并发查找；`Grant`/`Split` 走写锁 |
| `gateway.go` | 客户端入口：按 Key 解析租约持有者并转发请求，拒绝过期租约 |

## 关键语义

- **动态分裂**：`Split(boundary, rightNodeID)` 把包含 `boundary` 的区间一分为二，
  左半区间 `[Start, boundary)` 由当前持有者**继承**（租约继承），
  右半区间 `[boundary, End)` 授予新节点。
- **租约继承与 fencing**：子租约记录 `Parent` 并把 `Epoch` 加一，
  任何过期的父租约立即失去效力；分裂可链式继续（如 `[M,∞)` 再在 `S` 处分裂）。
- **并发隔离**：路由查找只持有 `RWMutex` 读锁；每个节点独立加锁，
  因此 Node-1 上的长请求不会阻塞 Node-2 的查询。

## 运行样例

```console
$ go run ./cmd/demo
== Step 1: whole key space [A-Z] leased to Node-1 ==
  granted: lease#1 node=Node-1 range=[A, ∞) epoch=1 parent=none

== Step 2: range too large -> split at 'M', right half leased to Node-2 ==
  left  child (inherited by Node-1): lease#2 node=Node-1 range=[A, M) epoch=2 parent=#1
  right child (granted to Node-2):   lease#3 node=Node-2 range=[M, ∞) epoch=2 parent=#1

== Step 3: concurrent queries 'Apple' and 'Zoo' ==
  (each node simulates 300ms processing latency)
  query "Zoo"  -> [M, ∞)  via Node-2    = "place 🦒" (took 302ms)
  query "Apple" -> [A, M)  via Node-1    = "fruit 🍎" (took 302ms)

Both queries finished in 303ms.
Serial execution would take >= 600ms; the two nodes worked in parallel, unblocked.
```

## 测试结果

```console
$ go test -race -v ./...
=== RUN   TestRouteBeforeSplit
--- PASS: TestRouteBeforeSplit (0.00s)
=== RUN   TestSplitCreatesIndependentLeasesWithInheritance
--- PASS: TestSplitCreatesIndependentLeasesWithInheritance (0.00s)
=== RUN   TestRoutingAfterSplit
--- PASS: TestRoutingAfterSplit (0.00s)
=== RUN   TestConcurrentQueriesDoNotBlock
--- PASS: TestConcurrentQueriesDoNotBlock (0.13s)
=== RUN   TestConcurrentQueriesWallTime
--- PASS: TestConcurrentQueriesWallTime (0.31s)
=== RUN   TestSplitChainKeepsFencingEpochs
--- PASS: TestSplitChainKeepsFencingEpochs (0.00s)
=== RUN   TestLeaseExpiry
--- PASS: TestLeaseExpiry (0.05s)
=== RUN   TestSplitAndGrantErrors
--- PASS: TestSplitAndGrantErrors (0.00s)
PASS
ok  	leaserouter	2.159s
```

其中 `TestConcurrentQueriesDoNotBlock` 是确定性验证：测试直接从外部持有
Node-1 的锁（模拟其卡死），断言 `Zoo` 查询照常完成、`Apple` 查询确实阻塞，
解锁后 `Apple` 恢复——证明两个租约持有节点互不阻塞。
