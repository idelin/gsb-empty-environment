Kubernetes Vertical Pod Autoscaler（VPA）通过指数衰减直方图持续估计容器 CPU/内存真实分位数，并在 OOM 发生时迅速抬升下界，同时在原地/重建更新时严守 PDB 与最小存活副本约束，四处核心算法极易失真：1）**指数半衰期衰减直方图（`DecayingHistogram`）防浮点溢出实现**：桶边界按几何级数增长（首桶宽度 `firstBucketSize`、倍率 `ratio > 1`）；新样本 `(value, weight, timestamp)` 按相对参考时间 `referenceTimestamp` 的权重缩放因子 `2^((t - ref) / halfLife)` 累加入桶——当时间跨度较大导致指数因子超过阈值（如 `2^100`）时，必须自动推进 `referenceTimestamp` 并将全部现有桶权重与总权重同步右移缩放，保证长期运行零浮点上溢且精确保持分位数不变！支持按给定分位 `p`（如 P50/P90/P95）返回线性或桶上界推荐值；2）**内存 OOM 阶梯抬升（OOM Bump-Up）与峰值滑动窗口**：维护滑动时间窗内每次容器 OOM 事件对应的实际内存上限，每次 OOM 自动生成虚拟抬升样本 `max(oomMemory * oomBumpUpRatio, oomMemory + oomMinBumpUp)`，确保即使正常采样的 P90 偏低，推荐下界也绝不低于近期未过期的 OOM 抬升水位；3）**置信度区间与 `ContainerResourcePolicy` 钳位**：基于历史样本跨度与样本数计算 `LowerBound`、`Target`、`UncappedTarget`、`UpperBound`（样本越少置信区间越宽！），再按容器策略的 `minAllowed`/`maxAllowed` 与 `controlledResources` 严格钳位（保留 `UncappedTarget` 供监控诊断！）；4）**安全驱逐决策器（Eviction Controller）**：仅当运行中 Pod 的当前资源请求落在 `[LowerBound, UpperBound]` 之外（或差值超过最小相对变更阈值且已超过驱逐冷却期！）才允许驱逐，且必须同时遵守 `minReplicas` 保底与所属 PDB 的 `disruptionsAllowed` 实时预算（单轮仅驱逐在预算内的最偏离 Pod！）。为弹性伸缩控制面交付可导入的 Go VPA 衰减直方图、OOM 抬升与安全驱逐引擎。验收覆盖跨百倍半衰期无浮点溢出重标定与精确分位数对照、OOM 虚拟抬升压倒低分位样本、少样本置信区间展宽与 `min/maxAllowed` 钳位以及 PDB/冷却期/`minReplicas` 驱逐门禁。

## 实现

仅依赖 Go 标准库，模块路径 `prescreen.local/ps-8d7338cd36`，按算法职责分包：

- `pkg/vpa/histogram` — 几何桶 `Histogram` 与半衰期 `DecayingHistogram`。
  衰减因子上限 `2^100`；超限即按 100 个半衰期推进 `referenceTimestamp`
  并对全部桶权重与总权重统一乘以 `2^-100`（分位数恒等变换），支持
  `Linear`/`UpperBound` 两种分位返回模式。
- `pkg/vpa/oom` — OOM 事件滑动窗口与抬升样本
  `max(oom*bumpUpRatio, oom+minBumpUp)`；`BumpFloor` 返回未过期事件的
  最高抬升水位，作为内存推荐硬下界。
- `pkg/vpa/estimator` — 分位目标 + 随样本数/时间跨度收窄的置信区间，
  `ApplyPolicy` 按 `minAllowed`/`maxAllowed`/`controlledResources`
  钳位并保留 `UncappedTarget`。
- `pkg/vpa/recommender` — 单容器推荐流水线，串联双资源衰减直方图、
  OOM 虚拟样本注入、内存下界抬升与策略钳位。
- `pkg/vpa/eviction` — 冷却期、置信区间越界/最小相对变更阈值、
  `minReplicas` 保底与 PDB `disruptionsAllowed` 预算门禁；每轮最多
  驱逐偏差最大的一个 Pod，并通过 `Reason` 解释每个候选的裁决。

## 测试

```
go test ./... -race
```

验收用例覆盖：跨数百个半衰期重标定后的分位数恒等性与有限性、OOM 抬升
压倒低分位样本及窗口过期、少样本宽区间与 `min/maxAllowed` 钳位（含
`UncappedTarget` 保留）、以及冷却期/PDB/`minReplicas`/单轮单 Pod 驱逐门禁，
另含一个端到端工作流测试（`pkg/vpa/integration_test.go`）与可运行示例
（`pkg/vpa/example_test.go`）。
