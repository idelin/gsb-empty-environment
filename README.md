# 故障转移粘性会话维护器（Sticky Session + Failover）

纯 Go 标准库实现的有状态连接保持与故障转移器：

- 根据用户 Cookie 建立粘性绑定（一级哈希），节点健康时始终路由到同一节点；
- 节点宕机时由 `HandleNodeFailure` 捕获异常，将该节点的全部会话基于**二级哈希**
  （`hash(sessionID, failedNode)`）均匀打散到其余健康节点；
- 每次（重）绑定都会重写会话头中的绑定标记 `X-Sticky-Binding: node=<id>;gen=<n>;via=<primary|failover>`；
- 未显式调用 `HandleNodeFailure` 时，`Bind`/`Route` 也会惰性自愈。

## 结构

- `sticky/sticky.go` — 核心实现（`Manager` / `Node` / `Session`）
- `sticky/sticky_test.go` — `go test` 验收测试
- `cmd/demo/main.go` — 运行样例：1000 会话粘到 A，A 宕机后平摊到 B/C

## 运行

```sh
go test ./... -v
go run ./cmd/demo
```

## 运行样例输出

```
== before failure ==
  node A: 1000 sessions
  node B: 0 sessions
  node C: 0 sessions

node A is DOWN -> failover migrated 1000 sessions

== after failover ==
  node A: 0 sessions
  node B: 502 sessions
  node C: 498 sessions

== sample rewritten binding headers ==
  sess-540d150eec4c8c50 -> route=B  X-Sticky-Binding: "node=B;gen=2;via=failover"
  sess-540d160eec4c8e03 -> route=B  X-Sticky-Binding: "node=B;gen=2;via=failover"
  sess-540d170eec4c8fb6 -> route=C  X-Sticky-Binding: "node=C;gen=2;via=failover"
  sess-7fc8fc0f05307e07 -> route=B  X-Sticky-Binding: "node=B;gen=2;via=failover"
  sess-a1e39b0f185a266d -> route=C  X-Sticky-Binding: "node=C;gen=2;via=failover"
```

## 测试结果

```
=== RUN   TestStickyBindingWhileHealthy
--- PASS: TestStickyBindingWhileHealthy (0.00s)
=== RUN   TestFailoverEvenRedistribution
    sticky_test.go:102: distribution after failover: B=502 C=498
--- PASS: TestFailoverEvenRedistribution (0.00s)
=== RUN   TestChainedFailover
--- PASS: TestChainedFailover (0.00s)
=== RUN   TestLazyHealOnRoute
--- PASS: TestLazyHealOnRoute (0.00s)
=== RUN   TestNoHealthyNodes
--- PASS: TestNoHealthyNodes (0.00s)
=== RUN   TestUnknownNodeAndCookie
--- PASS: TestUnknownNodeAndCookie (0.00s)
PASS
ok  	stickyfailover/sticky	0.323s
```

`TestFailoverEvenRedistribution` 即验收边界：1000 个会话绑定在节点 A，A 宕机后
全部迁移（B=502 / C=498，50/50 ± 10% 容差），且每个会话的绑定标记被重写为
`node=<B|C>;gen=2;via=failover`，此后路由保持新的粘性。
