# Graceful Leave（优雅离开）与平静期广播 — Go 实现

节点主动下线协议：节点主动发起 `Leave` 并广播离开意向；系统将其置为
`Leaving` 状态，在平静期（Quiescent Period）内阻断新流量、放行在途请求；
平静期定时器超时后，各节点彻底物理销毁该节点的路由条目。仅使用 Go 标准库。

## 状态机

```
Alive --Leave()--> Leaving --平静期超时--> Dead（路由条目物理销毁）
```

- **Alive**：位于各对端的就绪路由池，正常收发流量。
- **Leaving**：收到 Leave 广播后，对端立即将其移出就绪池（阻断新流量），
  并启动平静期定时器；已在途的 RPC 正常执行完成，无突发超时。
- **Dead**：定时器超时后路由条目被物理删除；离开节点在途排空后转为 Dead。

## 代码结构

- `state.go` — 节点/路由状态定义（Alive / Leaving / Dead）
- `router.go` — `RouteTable`：就绪池、Leaving 标记、平静期定时器、物理销毁
- `node.go` — `Node`：状态机、Leave 发起、在途请求跟踪与排空
- `cluster.go` — `Cluster`：Join/Leave 广播、RPC 路由（本地快速失败）
- `cmd/demo/main.go` — 可运行演示
- `graceful_test.go` — 验收测试

## 运行

```sh
go test -race -v ./...   # 运行测试
go run ./cmd/demo        # 运行演示
```

## 演示输出样例

```
[t=  0ms] 集群就绪, node-1 就绪池: [node-2 node-3 node-4 node-5]
[t= 28ms] node-3 发起 Leave, 广播离开意向
[t= 29ms] 平静期内新 RPC -> 快速失败: graceful: target not in ready pool (无 RPC 超时)
[t= 34ms] node-1 就绪池: [node-2 node-4 node-5] (node-3 已移出)
[t=228ms] 在途慢请求正常完成
[t=664ms] 平静期结束, node-3 路由条目已物理销毁: true
[t=664ms] node-3 最终状态: Dead
```

## 测试结果（go test -race -v）

```
=== RUN   TestLeaveMovesNodeOutOfReadyPool
--- PASS: TestLeaveMovesNodeOutOfReadyPool (0.00s)
=== RUN   TestNewTrafficBlockedDuringQuiescent
--- PASS: TestNewTrafficBlockedDuringQuiescent (0.00s)
=== RUN   TestInflightCompletesDuringQuiescent
--- PASS: TestInflightCompletesDuringQuiescent (0.07s)
=== RUN   TestPhysicalRemovalAfterQuiescent
--- PASS: TestPhysicalRemovalAfterQuiescent (0.24s)
=== RUN   TestRemovalDrivenByQuiescentTimer
--- PASS: TestRemovalDrivenByQuiescentTimer (0.00s)
=== RUN   TestConcurrentLeaveAndCalls
--- PASS: TestConcurrentLeaveAndCalls (0.02s)
PASS
ok  	graceful	2.100s
```

## 验收边界覆盖

| 验收点 | 测试 |
| --- | --- |
| Leave 后平滑移出就绪路由池 | `TestLeaveMovesNodeOutOfReadyPool` |
| 平静期内阻断新流量、本地快速失败无 RPC 超时 | `TestNewTrafficBlockedDuringQuiescent` |
| 在途请求正常完成、排空后节点转 Dead | `TestInflightCompletesDuringQuiescent` |
| 定时器超时后物理销毁路由条目 | `TestPhysicalRemovalAfterQuiescent` / `TestRemovalDrivenByQuiescentTimer` |
| 并发 Leave 与 RPC 竞争安全 | `TestConcurrentLeaveAndCalls`（`-race` 通过） |
