# 故障转移粘性会话（Sticky Session）维护器

纯 Go 标准库实现的有状态连接保持与故障转移器：

- 根据用户 Cookie（`sticky_uid`）建立粘性绑定；
- 节点健康时，同一会话始终导向同一节点（一级哈希 + 显式绑定表）；
- 节点故障时，`Failover` 将该节点的全部会话基于**二级哈希**
  （以故障节点 ID 为盐的 FNV-1a）均匀打散到其余健康节点；
- 每次路由在响应头 `X-Sticky-Binding: <uid>@<nodeID>` 中重写绑定标记。

## 文件结构

- `sticky.go` — 核心库：`Node` / `Manager`（`Bind`、`Failover`、`Route`）
- `sticky_test.go` — `go test` 验收测试
- `cmd/demo/main.go` — 运行样例：1000 会话绑定 A → A 宕机 → 平摊到 B/C

## 运行

```sh
go test ./... -v
go run ./cmd/demo
```

## 运行样例输出

```
== 阶段 1：初始绑定（仅 A 在线） ==
  节点 A: 1000 个会话

== 阶段 2：节点 A 宕机，1000 个会话被重定向 ==
  节点 B: 481 个会话
  节点 C: 519 个会话

== 阶段 3：抽样验证绑定标记重写 ==
cookie=u-7cc409e33f03945d  ->  X-Sticky-Binding: u-7cc409e33f03945d@B
cookie=u-624b456a60ea0f9e  ->  X-Sticky-Binding: u-624b456a60ea0f9e@B
cookie=u-b0e5a85588d412ae  ->  X-Sticky-Binding: u-b0e5a85588d412ae@B
cookie=u-bfdce762d717b96c  ->  X-Sticky-Binding: u-bfdce762d717b96c@B
cookie=u-a3c8b495d75c0803  ->  X-Sticky-Binding: u-a3c8b495d75c0803@C
```

## 测试结果

```
=== RUN   TestStickyBindingStableWhileHealthy
--- PASS: TestStickyBindingStableWhileHealthy (0.00s)
=== RUN   TestFailoverEvenRedistribution
    sticky_test.go:89: distribution after failover: B=500 C=500
--- PASS: TestFailoverEvenRedistribution (0.00s)
=== RUN   TestBindingHeaderRewrittenAfterFailover
    sticky_test.go:137: rewritten binding header: user-0001@B
--- PASS: TestBindingHeaderRewrittenAfterFailover (0.00s)
=== RUN   TestRouteIssuesCookieForNewSession
--- PASS: TestRouteIssuesCookieForNewSession (0.00s)
=== RUN   TestNoHealthyNode
--- PASS: TestNoHealthyNode (0.00s)
=== RUN   TestFailoverUnknownNode
--- PASS: TestFailoverUnknownNode (0.00s)
PASS
ok  	sticky	0.435s
```

验收边界覆盖：1000 个会话绑定在 A；A 宕机后全部被捕获并基于二级哈希
均匀平摊给 B 和 C（测试中恰为 500/500，断言容差 ±20%），
且会话头中的绑定标记被重写为新节点。
