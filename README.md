# phiaccrual — 自适应心跳健康度探测器（仿 φ-Accrual Failure Detector）

纯 Go 标准库实现。不依赖固定超时阈值，而是基于历史心跳间隔的分布
（正态近似）持续计算节点崩溃的**怀疑度 φ**，φ 超过阈值时触发租约回收。

## 原理

探测器维护最近 N 次心跳间隔的滑动窗口，估计其均值 μ 与标准差 σ，对
当前静默时长 `t - lastHeartbeat` 计算：

```
φ(t) = -log10(1 - F(t - lastHeartbeat))    F ~ N(μ, σ²) 的 CDF
```

- 延迟落在历史方差之内 → φ 缓慢爬升，**不误警**；
- 真正断网 → 静默持续超出分布可解释范围，φ 指数式冲高，**果断判死**；
- `MinStdDeviation` 为方差估计兜底，避免冷启动/超稳链路下过度敏感
  （与 Akka 的 `min-std-deviation` 约定一致）；
- `AcceptableHeartbeatPause` 可整体平移均值，容忍 GC 等环境停顿。

## 结构

- `detector.go` — `Detector`（φ 计算）、`Monitor`/`Lease`（租约监控与回收）
- `detector_test.go` — go test 验收测试
- `cmd/demo/main.go` — 可运行样例：抖动期不误判，断网后果断回收租约

## 运行

```sh
go test -v ./...    # 验收测试
go run ./cmd/demo   # 运行样例（约 8 秒）
```

## 验收边界覆盖

| 场景 | 测试 |
| --- | --- |
| 偶发短时抖动不误警 | `TestPhiStaysLowDuringNormalJitter` |
| 固定超时剧烈误判 vs φ 零误判 | `TestFixedTimeoutWouldFlapWherePhiDoesNot` |
| 真正断网果断回收租约（且仅一次） | `TestRealPartitionReclaimsLeaseDecisively` |
| φ 随静默单调增长 | `TestPhiGrowsMonotonicallyWithSilence` |
| 分布自适应更慢链路 | `TestDistributionAdaptsToSlowerNetwork` |
| 真实时钟下监控循环 | `TestMonitorRunLoopWithRealClock` |
