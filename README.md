微服务调用链 `A -> B -> C -> D` 若在每一跳都独立配置了 3 次重试与对冲请求（Hedging），下游 `D` 出现轻微过载时会沿链条按指数级（最高 `4^3 = 64` 倍）放大流量引发全站雪崩；引入滑动窗口重试预算（Retry Budget，如仅允许额外重试不超过活跃常规流量的 `20%` 外加 `minRetryConcurrency` 保底）、剩余截止期（Deadline）传播及自适应熔断后，需要在稳态建模和逐请求运行时决策中精准算清每跳真实到达率与是否收敛。为服务网格代理交付可导入的 Go 多跳重试控制与稳态分析包。组件包含两套相互一致的能力：1）**运行时准入与重试决策器**：为每条边维护滑动时间窗内的正常请求数与在途重试数、令牌/并发预算（`allowedRetries = max(minRetryConcurrency, floor(budgetRatio * activeRequests))`）、连续故障熔断器（Closed/Open/HalfOpen）与剩余截止期传播（`remainingDeadline = deadline - elapsed`；每次重试前校验剩余时间大于下游最小处理耗时加退避等待，否则直接中止重试防做无用功！），支持指数退避加确定性抖动与对冲请求取消（首个成功响应返回即取消其余在途对冲分支）；2）**多跳有向无环调用图稳态放大求解器**：输入拓扑 DAG、每节点容量上限与负载-故障率响应曲线 `p_fail(load)`（当到达率超过软阈值时故障率单调上升）、各边扇出倍率及重试/预算/对冲配置，通过不动点迭代精确求解稳态下每节点有效到达率、实际故障率与总请求放大倍率，识别哪些节点进入不可收敛雪崩态（放大倍率击穿硬容量），并计算可恢复全链路稳态的各边最大安全预算上限 `budgetRatio`。验收覆盖 4 跳链式重试无预算雪崩 vs 有预算收敛对比、截止期不足提前截断重试、`minRetryConcurrency` 低流量保底、对冲请求竞速取消以及稳态求解器与离散事件仿真在临界过载点结果吻合。

## 包结构

模块路径 `prescreen.local/ps-9f073c0c88`，仅依赖 Go 标准库，包含两个可独立导入的包。

- `retry`：逐请求运行时准入与重试决策器。
  - `SlidingWindow` / `RetryBudget`：滑动窗口常规请求计数与在途重试并发预算，实现 `allowedRetries = max(minRetryConcurrency, floor(budgetRatio * activeRequests))`；负比例或 `+Inf` 表示不限预算。
  - `CircuitBreaker`：连续故障 Closed/Open/HalfOpen 状态机，含 Open 超时探测与 HalfOpen 探针上限。
  - `Deadline`：剩余截止期传播，重试前强制 `remaining > minProcessing + backoff` 的严格大于校验，不足即 `AbortDeadline`。
  - `BackoffConfig`：指数退避 + 基于 FNV 的确定性抖动，相同 `(seed, call, attempt)` 产生可复现延迟。
  - `EdgeController`：把预算、熔断、截止期、退避与对冲组合为单次 `DecideRetry` / `DecideHedge` 决策。
  - `Executor`：驱动真实 `InvokeFunc`，首个成功响应即取消其余在途对冲分支；可接 `RealScheduler`（生产）或 `SimScheduler`（确定性测试/仿真）。
- `steady`：多跳 DAG 稳态放大求解器、安全预算搜索与离散事件仿真（DES）。
  - `Topology` / `NodeConfig` / `EdgeConfig` / `SoftThresholdCurve`：拓扑、硬容量、`p_fail(load)` 单调响应曲线、扇出与每边重试/预算/对冲/截止期配置。
  - `Solve` / `SolveWith`：阻尼不动点迭代。每轮先由负载反推各节点本地与有效故障率（反向拓扑，含下游级联），再前向传播物理负载；输出每节点有效/物理到达率、本地与有效故障率、链上放大倍率、容量击穿（雪崩）标记，以及每边尝试次数、预算上限与是否被预算/截止期约束。
  - `GlobalSafeRatio` / `MaxSafeBudgets`：二分搜索恢复全链路收敛所需的统一与逐边最大安全 `budgetRatio`。
  - `Simulate`：基于堆的离散事件仿真，直接复用 `retry.EdgeController`、`RetryBudget`、`CircuitBreaker`、`Deadline` 与 `Backoff`，因此解析模型与逐请求运行时共用同一套决策代码。

## 核心建模约定

- 边 `u->v` 的每次物理尝试都包含“v 本地处理 + v 的整条下游子链”，因此嵌套重试的物理负载是逐跳相乘的；3 次重试（4 次尝试）×3 跳在 sink 必败时给出精确的 `4^3 = 64` 倍放大，由 `TestWorstCaseAmplification64x` 锁定。
- 边调用成功率按离散的整数最大尝试次数计算 `1 - pFail^maxAttempts`；分数的期望尝试次数只用于负载与预算缩放，二者不可混用。
- 重试预算是并发上限，经下游平均服务时间用 Little 定律换算为尝试/秒速率后参与不动点；`minRetryConcurrency` 在低流量下提供保底并发。
- 节点有效故障率 = 本地故障 OR 任一必需出边调用失败（fan-out 要求全部成功），这是过载沿链反向传播、触发上游雪崩的机制。

## 验收测试映射

运行 `go test ./...`（并发安全用 `go test -race ./...` 验证）。

- 无预算雪崩 vs 有预算收敛：`steady/avalanche_test.go::TestAvalancheWithoutBudgetVsBudgetedConvergence`
- 64 倍最坏放大：`steady/avalanche_test.go::TestWorstCaseAmplification64x`
- 截止期不足提前截断：`retry/controller_test.go::TestControllerDeadlineTruncatesRetry`、`steady/controls_test.go::TestSolverDeadlineLimitsAttempts`、`retry/executor_test.go::TestExecutorDeadlineStopsRetry`
- `minRetryConcurrency` 低流量保底：`retry/budget_test.go::TestRetryBudgetMinFloorAtLowTraffic`、`steady/controls_test.go::TestMinRetryConcurrencyFloor`、`steady/simulation_test.go::TestSimulationMinRetryFloor`
- 对冲竞速取消：`retry/executor_test.go::TestExecutorHedgeRacingCancellation`、`retry/controller_test.go::TestControllerHedgeBudgetAndCancellation`、`steady/simulation_test.go::TestSimulationHedgeCancellation`
- 求解器与 DES 临界过载吻合：`steady/simulation_test.go::TestSolverMatchesSimulationAtCriticalOverload`
- 安全预算恢复稳态：`steady/avalanche_test.go::TestSafeBudgetRestoresSteadyState`

## 快速上手

```go
import (
    "prescreen.local/ps-9f073c0c88/retry"
    "prescreen.local/ps-9f073c0c88/steady"
)
```

运行时侧用 `retry.NewEdgeController(cfg, retry.NewRealScheduler())` 加 `retry.NewExecutor(...)` 包住每次下游调用；分析侧用 `steady.Solve(topology)` 得到每节点稳态到达率与雪崩判定，用 `steady.GlobalSafeRatio(topology)` 求可落地的预算比例。两个包的可运行示例见各自的 `example_test.go`。
