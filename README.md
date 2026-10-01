# phidetector — 自适应心跳健康度探测器（仿 φ-Accrual Failure Detector）

不采用固定超时阈值，而是基于历史心跳间隔的**失联概率评估**：
探测器维护心跳到达间隔的滑动窗口，估计其均值与方差，用正态分布
建模下一次心跳的到达时间，并计算怀疑度：

```
φ(t) = -log10(P_later(t - t_last))
```

`P_later` 为正态分布的生存函数（通过 `math.Erfc` 计算，保证尾部精度）。
φ 的语义直观：**φ = 8 意味着"节点其实还活着却这么晚"的概率小于 10⁻⁸**。
当 φ 超过阈值时触发租约回收。

## 为什么优于固定超时

| 场景 | 固定超时 | φ 自适应探测器 |
|---|---|---|
| 偶发短时抖动 | 误判死亡 → 剧烈重选 | 落在历史方差内，φ 保持低位 |
| 真实断网 | 死板等待固定时长 | φ 迅速越阈，果断回收租约 |
| 不同质量的链路 | 一个阈值难调 | 按各链路方差自动适配 |

## 结构

- `detector.go` — 核心：`Detector`（滑动窗口 + φ 计算），并发安全，时间由调用方注入便于测试
- `monitor.go` — `Monitor`：周期性评估 φ，越阈时精确触发一次租约回收回调
- `detector_test.go` — `go test` 验收测试
- `cmd/demo/main.go` — 运行样例：固定超时 vs 自适应探测器的对比演示

## 运行

```bash
go test ./... -v     # 验收测试
go run ./cmd/demo    # 运行样例
```

## 关键调参（Option）

- `WithPhiThreshold` — φ 阈值（默认 8，即误判概率 < 10⁻⁸）
- `WithWindowSize` — 历史窗口容量（默认 200 个间隔）
- `WithMinSamples` — φ 生效所需的最少样本数（默认 10）
- `WithMinStdDev` — 标准差下限，防止过稳链路变得过于敏感（默认 10ms）
- `WithAcceptablePause` — 平坦宽限期（如已知 GC 停顿）

## 验收边界覆盖（detector_test.go）

- `TestJitterDoesNotCauseFalsePositive` — 训练数据自然产生 >250ms 的间隔
  （固定超时必误判），φ 仅升至 ~3.7，不触发误警
- `TestRealFailureTriggersReclaim` — 真正断网 1s 后 φ 飙升至 ~115，果断越阈
- `TestAdaptsToVariance` — 同样的 200ms 延迟：平稳链路判死（φ≈23），
  抖动链路容忍（φ≈4）
- `TestDetectorSurvivesJitterStormDeterministic` — 5000 拍抖动风暴（模拟时间），
  φ 全程不越阈
- `TestMonitorReclaimsLeaseOnPartition` / `TestMonitorSurvivesJitterStorm` —
  真实时钟下 Monitor 的端到端行为
