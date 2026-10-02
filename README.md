# gracefulleave

优雅离开（Graceful Leave）与平静期广播 —— Go 标准库实现的节点主动下线协议。

## 协议流程

1. **Leave 发起**：节点调用 `Node.Leave()`，通过 `Transport.BroadcastLeaveIntent`
   向所有对等节点同步广播离开意向（LeaveIntent）。
2. **Leaving 状态**：对等节点在 `HandleLeaveIntent` 中将该成员置为
   `StateLeaving`，并启动平静期（Quiescent Period）定时器。
3. **平静期阻断新流量**：`Membership.Ready()` 只返回 `StateAlive` 成员，
   `Router` 不再向 Leaving 成员路由新请求；已在途（in-flight）的 RPC
   继续正常执行直至完成，不会产生突发 RPC 超时。
4. **物理销毁**：平静期定时器超时后，路由条目被彻底删除
   （`Membership.remove`）；离开节点自身在排空在途请求后
   `Shutdown` 并从 Transport 注销。此后对该节点的调用立即以
   `ErrNodeUnreachable` 快速失败，而非挂起等待超时。

## 结构

| 文件 | 职责 |
|---|---|
| `member.go` | 成员状态机（Alive → Leaving）与在途计数 |
| `membership.go` | 本地路由表：状态迁移、平静期定时器、物理删除 |
| `transport.go` | 内存消息总线：LeaveIntent 广播、点对点调用 |
| `router.go` | 就绪池轮询路由，Leaving 成员不接收新流量 |
| `node.go` | 节点：`Leave()` / `HandleLeaveIntent()` / `Shutdown()` |
| `cluster.go` | 测试/演示用的集群装配 |
| `cmd/demo/main.go` | 端到端运行样例 |

## 运行样例

```console
$ go run ./cmd/demo
cluster up: 3 nodes, quiescent period = 500ms

[t=0ms] node-C initiates Leave: broadcasting LeaveIntent to peers
[t=50ms] node-A view: node-C=Leaving, ready pool=[node-B]
[t=50ms] node-B view: node-C=Leaving, ready pool=[node-A]
[quiescent] in-flight RPC to node-C completed normally (no timeout)
[t=500ms] quiescent timer expires -> physically destroying node-C routing entries
[after]  node-A view: node-C entry present=false, ready pool=[node-B]
[after]  node-B view: node-C entry present=false, ready pool=[node-A]

traffic summary: 776 calls, 0 failures (no sudden RPC timeouts)
```

## 测试结果

```console
$ go test -race -v ./...
=== RUN   TestReadyExcludesLeaving
--- PASS: TestReadyExcludesLeaving (0.00s)
=== RUN   TestPhysicalRemovalAfterQuiescent
--- PASS: TestPhysicalRemovalAfterQuiescent (0.00s)
=== RUN   TestMarkLeavingIdempotentAndUnknown
--- PASS: TestMarkLeavingIdempotentAndUnknown (0.00s)
=== RUN   TestLeaveBroadcastMarksPeersLeaving
--- PASS: TestLeaveBroadcastMarksPeersLeaving (0.00s)
=== RUN   TestRouterSkipsLeavingMember
--- PASS: TestRouterSkipsLeavingMember (0.00s)
=== RUN   TestInFlightCallCompletesDuringQuiescent
--- PASS: TestInFlightCallCompletesDuringQuiescent (0.02s)
=== RUN   TestEntryPhysicallyRemovedAfterQuiescent
--- PASS: TestEntryPhysicallyRemovedAfterQuiescent (0.15s)
=== RUN   TestNoSuddenFailuresUnderLoad
    node_test.go:170: 371430 calls completed with zero failures while n2,n3 left
--- PASS: TestNoSuddenFailuresUnderLoad (0.53s)
=== RUN   TestCallToRemovedNodeFailsFast
--- PASS: TestCallToRemovedNodeFailsFast (0.16s)
=== RUN   TestLeaveAndShutdownIdempotent
--- PASS: TestLeaveAndShutdownIdempotent (0.16s)
=== RUN   TestShutdownDrainsInFlight
--- PASS: TestShutdownDrainsInFlight (0.05s)
PASS
ok  	gracefulleave	2.527s
```

测试与验收边界的对应关系：

- **Leave 广播**：`TestLeaveBroadcastMarksPeersLeaving` —— 对等节点收到
  LeaveIntent 后将离开节点置为 Leaving。
- **平滑移出就绪路由池**：`TestRouterSkipsLeavingMember`、
  `TestReadyExcludesLeaving` —— 平静期内新流量 0 次到达 Leaving 节点。
- **定时器超时后物理销毁**：`TestPhysicalRemovalAfterQuiescent`（注入手动
  定时器，确定性验证）、`TestEntryPhysicallyRemovedAfterQuiescent`。
- **无突发 RPC 超时**：`TestInFlightCallCompletesDuringQuiescent`、
  `TestShutdownDrainsInFlight`、`TestNoSuddenFailuresUnderLoad`
  （37 万次并发调用、两个节点中途离开，0 失败）、
  `TestCallToRemovedNodeFailsFast`（条目销毁后快速失败而非超时）。
