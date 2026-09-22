# retrybudget — 共享预算的两层重试（单进程事件模拟器）

一个只依赖 Go 标准库的确定性离散事件模拟器，演示**上层代理重试**与**下层副本重试**
如何共享同一份请求预算和同一个截止时间，避免嵌套重试把请求数乘法放大。

- 上层：`ProxyBranches` 个代理（hedged）分支
- 下层：每个分支顺序执行至多 `ReplicaAttempts` 次副本重试
- 3 × 3 的朴素组合最多可发出 **9** 个请求；当共享预算为 5 时，实际发出**绝不超过 5**

时间、网络延迟、故障（成功/失败/静默）、取消全部由事件队列脚本化控制；
没有 goroutine、没有 wall clock，因此每次运行都可复现。

## 目录

- `sim/sim.go` — 虚拟时钟 + 优先事件队列（同时刻 FIFO）、全局唯一 ID
- `sim/cancel.go` — 可组合的取消树，一次性传播语义
- `transport.go` — **唯一**的请求发出点（chokepoint），持有预算与截止
- `call.go` — 两层重试状态机，唯一的终结转移 `finish`
- `cmd/demo/main.go` — 场景演示
- `*_test.go` / `sim/*_test.go` — `go test` 安全性与活性测试

## 关键设计

**单一发出点。** 所有层、所有分支要发包都必须经过 `Transport.Send`。它在真正记账前
依次检查三道闸：已取消 → 截止已到（`now >= deadline`，截止时刻也算晚）→ 预算剩余。
被拒的请求**不消耗预算**（`DeniedBudget/DeniedDeadline/DeniedCancel` 单独计数），
通过后才 `remaining--`、`Sent++` 并写入只增的事实日志 `SendRecord`。

**每次尝试有 ID。** `Sim.NextID()` 生成进程内单调唯一 ID；每个被发出的请求都有
`(id, branch, replicaAttempt, sentAt)`，测试据此断言无重复、可追踪。

**取消传播。** `Ctl` 构成根/分支/请求树。终结（成功、失败、预算耗尽、截止）时
`root.Cancel` 同步传播到所有在途请求；传输层的取消钩子保证“静默对端”也恰好被结算一次。
`Ctl.Cancel` 是一次性的：重复取消是空操作，第一个理由（如 deadline）不会被覆盖。

**迟到失败不改变终态。** 回复回调首先检查 `c.done`；`finish` 是唯一终结转移且幂等。
成功后其他分支迟到的成功/失败都变成 no-op，在途请求则被取消（`SendRecord.Canceled`）。

## 安全性证据（不变量）

1. **请求数 ≤ 预算。** 预算只在 `Transport.Send` 的临界区递减；任何路径发包都要过这道闸。
   测试：`TestBudgetCapsAmplification`（3×3、预算 5 → 实发 5；预算 0 → 0）、
   `TestDenialConsumesNoBudget`。
2. **截止后不发。** 判定 `now >= deadlineAt` 在记账之前；事件循环只会让时间单调前进。
   测试：`TestTransportDeadlineChokepoint`、`TestDeadlineDeniesAtExactCutoff`、
   `TestNoSendAfterDeadline`，并逐条断言 `SendRecord.SentAt < deadline`。
3. **成功后迟到失败无效。** 回调以 `done` 为闸门，终结时取消全部在途请求。
   测试：`TestLateFailureAfterSuccess`、`TestSeveralLateFailuresAfterSuccess`、
   `TestCancelPropagation`（慢请求在成功时刻被标记为 canceled）。
4. **取消恰好一次、全树到达。**
   测试：`sim.TestCancelTree`、`sim.TestChildAfterCancelIsBornCanceled`。

## 活性证据（终态可达）

- 下层重试能从瞬时故障恢复：`TestLivenessSuccessAfterRetries`（失败两次，第三次成功）。
- 分支全部失败时终结为错误：预算充足 + 全失败场景在 9 个请求后结束。
- 预算耗尽是终结错误，立即拆除全调用，不再产生事件：`TestBudgetCapsAmplification`。
- 截止总是终结，即使所有对端静默：`TestLivenessDeadlineTerminatesSilence`。
- 无截止且对端完全静默时，队列排空由活性兜底终结，杜绝“永久挂起”：
  `TestLivenessSilentPeer`；同时事件队列有最大事件数，异常重试风暴会直接 panic：
  `sim.TestRunBudgetPanicsOnStorm`。
- 确定性：相同脚本两次运行的终态、实发数、终结时刻完全一致：`TestDeterministic`。

## 运行

```sh
go test ./...
go test -race ./...     # 可选
go run ./cmd/demo
```

## 演示输出（示例）

```
== naive 3 proxy x 3 replica, generous budget (the 9) ==
terminal : FAIL: retrybudget: all attempts failed
time     : t=5   deadline-hit=false
sent     : 9 actual wire requests
per-branch sends: map[0:3 1:3 2:3]

== same 3x3 with ONE shared budget of 5 (no uncontrolled 9) ==
terminal : FAIL: retrybudget: shared request budget exhausted
sent     : 5 actual wire requests
denied   : budget=1 deadline=0 canceled=0
per-branch sends: map[0:2 1:2 2:1]

== deadline at t=4; replies take 10 (nothing sent after cutoff) ==
terminal : FAIL: context deadline exceeded
time     : t=4   deadline-hit=true
sent     : 3 actual wire requests

== hedge: branch 0 succeeds at t=2, branch 1's slow failure is late ==
terminal : SUCCESS
time     : t=2
sent     : 2 actual wire requests   # 慢分支在 t=2 被取消，迟到失败不改终态

== liveness: lower layer fails twice, third replica attempt succeeds ==
terminal : SUCCESS
time     : t=5
sent     : 3 actual wire requests
```
