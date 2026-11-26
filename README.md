# 多跳重试控制与稳态分析包

为服务网格代理提供两套语义一致的 Go 能力，解决 `A -> B -> C -> D` 调用链中
逐跳独立重试/对冲导致的指数级流量放大（最坏 `4^3 = 64` 倍）与雪崩问题：

1. **运行时准入与重试决策器**（`retry` 包）：逐请求、逐跳的并发安全决策；
2. **多跳 DAG 稳态放大求解器**（`steady` 包）：不动点迭代求解稳态到达率与雪崩；
3. **离散事件仿真器**（`sim` 包）：虚拟时间复现运行时语义，交叉验证求解器。

## 目录结构

| 包 | 职责 |
| --- | --- |
| `retry` | 滑动窗口重试预算、连续故障熔断（Closed/Open/HalfOpen）、剩余截止期传播、确定性抖动指数退避、对冲竞速取消 |
| `steady` | DAG 拓扑校验、`p_fail(load)` 响应曲线、稳态不动点、雪崩判定、最大安全 `budgetRatio` 二分 |
| `sim` | 泊松到达、虚拟时间事件堆、预算/熔断/截止期/对冲仿真、速率测量 |
| `example` | 可运行示例（`go test ./example/`） |

## 运行时语义（retry）

每条边维护一个滑动窗口，核心预算公式与需求完全一致：

```
allowedRetries = max(minRetryConcurrency, floor(budgetRatio * activeRequests))
```

- **截止期传播**：每次重试前校验
  `remainingDeadline = deadline - now > backoff + minProcessingTime`，
  不满足则直接返回 `ErrDeadlineExceeded`，不再发起注定无用的尝试。
- **退避**：`base * multiplier^(attempt-1)` 叠加 FNV-1a 确定性全抖动，
  相同 `requestID` 在任意副本上得到相同序列，避免重试同步。
- **对冲**：首尝试在 `HedgeDelay` 内未返回则发竞速分支；任一分支成功立即
  `cancel()` 其余在途分支，被取消分支不计入熔断与指标。
- **熔断**：连续故障达阈值进入 Open 快速失败，冷却后 HalfOpen 限并发探测，
  探测成功回到 Closed。

## 稳态求解（steady）

对 DAG 按拓扑序逐节点自洽求解（序列单调不减，无不动点即雪崩）：

```
p_k     = curve(load_k / capacity)
extra_k = Σ 入边逻辑速率 × extraDemand(边, p_k)
load_k+1 = 逻辑到达率 L + extra_k
```

- `extraDemand` 的无约束上界为几何级数 `geom(p, MaxAttempts) - 1`
  （对冲与重试共享 `MaxAttempts` 尝试预算，仅改变发起时机）；
- 预算约束：`min(需求, max(MinRetryRate/L, BudgetRatio))`；
- 物理速率击穿硬容量（`loadRatio > 1`）或迭代发散即标记 `Avalanche`；
- `MaxSafeBudgets` 对每条边二分求出可恢复全链路稳态的最大 `budgetRatio`。

### 速率语义映射

运行时按并发计数、求解器按速率计数，二者通过 Little 定律对齐：

```
minRetryConcurrency ≈ MinRetryRate × 窗口时长
activeRequests      ≈ 常规逻辑速率 × 窗口时长
```

`sim` 包在同一滑动窗口内分别统计逻辑请求与额外尝试速率，复现该口径。

## 验收对照

| 验收点 | 测试 |
| --- | --- |
| 4 跳链无预算雪崩 vs 有预算收敛 | `steady.TestUnboundedChainAvalanche` / `TestBudgetedChainConverges` |
| 最坏 64 倍放大 | `steady.TestWorstCase64xAmplification`（三跳连乘 ≈ 63.7） |
| 截止期不足提前截断 | `retry.TestDeadlineTruncatesRetry`、`sim.TestSimDeadlineTruncation` |
| `minRetryConcurrency` 低流量保底 | `retry.TestMinRetryConcurrencyFloor`、`steady.TestMinRetryRateFloor` |
| 对冲竞速取消 | `retry.TestHedgeRaceCancellation`、`sim.TestSimHedgeCancellation` |
| 求解器与仿真临界过载吻合 | `sim.TestSimMatchesSolverBudgeted`（稳态偏差 < 1%）、`TestCriticalOverloadAgreement`（雪崩判定一致） |
| 熔断三态生命周期 | `retry.TestCircuitBreakerLifecycle` |
| 确定性抖动 | `retry.TestDeterministicJitter` |

## 运行

```bash
go test ./... -count=1 -race
```
