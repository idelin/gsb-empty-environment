# 分层重试：共享请求预算与截止时间（单进程事件模拟器）

一个两层重试系统的**确定性单进程事件模拟**。上层（proxy）最多 3 次尝试，每次
调用下层（service）；下层最多 3 次尝试，每次向某个副本（replica）发一个物理
请求。两层共享**同一个请求预算**和**同一个截止时间**，因此重试不会跨层乘法
放大。通信、故障、时间全部由模拟器控制，不接触真实网络与墙钟。

仅使用 Go 标准库；验证全部在 `go test` 中。

## 目录结构

- `sim/sim.go`：离散事件模拟器。虚拟时钟、FIFO 网络消息（`Send`）、同组件控制
  消息（`Local`）、定时器（`After`）、事件数上限（防活锁）。
- `retrysys/budget.go`：共享预算 + 截止时间，唯一的“发物理请求”闸门
  （`Acquire`：检查与扣减在同一个模拟步内完成）。
- `retrysys/ledger.go`：审计账本、层级尝试 ID（`C1.P2.S3.R(replica-0)`），以及
  直接从账本校验安全不变量的 `Check`。
- `retrysys/service.go`：下层重试状态机（尝试、超时、取消、终态）。
- `retrysys/proxy.go`：上层重试状态机（尝试、退避、取消传播、终态）。
- `retrysys/replica.go`：脚本化副本（延迟/成功/失败/丢包/是否响应取消/迟到）。
- `retrysys/world.go`：装配 actor、预算、截止时间、账本并运行。
- `retrysys/retrysys_test.go`、`sim/sim_test.go`：性质与模拟器测试。
- `cmd/demo/main.go`：四个关键场景的可运行样例。

## 关键设计

**共享预算（不乘法放大）**：物理请求只能经 `Budget.Acquire(now)` 发出。它在一个
模拟步内同时检查“未到截止时间”和“还有余额”，随后扣减。上层 3 次 × 下层 3 次
并不产生 9 个请求，而是与这至多 9 次**逻辑尝试**竞争同一个物理请求池（默认
容量 3）。

**截止时间（截止后不再发）**：`World` 在 `Deadline` 时刻投递唯一的 `DeadlineFire`
事件；预算被置为过期，在途请求被取消，任何“开启新尝试/发新请求”的入口都先检查
`now >= deadline` 或 `Acquire`，二者必拒。

**尝试 ID 与实际请求数**：每次逻辑尝试与每个物理请求都有唯一层级 ID；账本逐条
记录 `attempt/send/reject/reply/cancel/timeout/expire/terminal`，
`Ledger.PhysicalSends` 即实际发出的物理请求数（被拒绝、未取到预算的不计入）。

**取消传播**：尝试定时器先触发 → 记账取消 → 向下层/副本发取消；脚本可令副本
“尊重取消”（抑制待发回复）或“忽略取消”（取消丢失，构造一条可控迟到失败）。

**迟到不改变终态**：任一层一旦写入首个 `terminal` 事件即终态；之后到达的回复
（含成功之后的迟到失败）只记 `late-after-terminal` 证据并被丢弃，不重启循环、
不再发送、不覆盖终态。

## 安全性论证（Safety）

模拟是单线程状态机：虚拟时刻内消息按序处理，检查与状态变更之间没有交错。

- **S1 截止后不发送**。发送的唯一通路在请求前 `Acquire`，且上层开启尝试前另有
  `now >= deadline` 判定；截止事件把预算置过期。因此任意 `send` 事件的 tick
  严格小于 `deadline`。由 `Ledger.Check` 对每条 `send` 断言。
- **S2 不超预算、不乘法放大**。物理发送次数等于 `Acquire` 成功次数；每次成功
  扣 1，余额为 0 后恒失败。故物理发送数 ≤ `MaxRequests`；“3×3”被同一个池串行
  化，上、下层后续尝试得到的是 `shared-budget-exhausted` 拒绝而不是第 4..9 个
  请求。由测试与 `Check` 断言。
- **S3 终态唯一且迟到无效**。`finish` 以 `done` 守卫，只写一次 `terminal`；终态
  后该调用的 actor 状态被删除或置 `done`，任何回复落入 `st==nil`/`done` 分支，
  只记证据不产生发送或状态转移。`Check` 校验每个终态键只出现一次，且终态之后
  不存在 `send`。

## 活性论证（Liveness）

- **成功闭包**：任一物理请求成功 → 下层取消其余在途请求、写成功终态并上报 →
  上层写成功终态；之后不再发送（由终态守卫保证），调用以 `ok` 结束
  （`TestLivenessSucceedsWithinBudget`）。
- **有限失败闭包**：每次失败或超时最多消耗一个物理请求并前进一个尝试序号；
  物理请求受预算上界、逻辑尝试受每层次数上界、时间受截止时间上界。三条上界
  任意一条到达都会产生终态（`attempt-budget-exhausted` /
  `shared-budget-exhausted` / `deadline-reached`），故调用必然终止，无无限重试
  （`TestLivenessExhaustionAndDropTerminate`）。
- **事件循环终止**：模拟器另有 `MaxSimSteps` 上限，任何意外自激循环都会显式报错
  而不是挂起（`TestSimulatorEventBudget`）。相同脚本两次运行账本逐字相同
  （`TestDeterministic`），结论可复现。

## 运行

```bash
go test ./...
go test ./... -v
go run ./cmd/demo
```
