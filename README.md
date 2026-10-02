# adaptivebatch

向量化执行引擎的自适应批大小（Adaptive Batch Size）调节器，纯 Go 标准库实现。

## 设计

- `AdaptiveBatchSizer`（`regulator.go`）：核心调节器。执行引擎每处理完一个
  batch，把观测样本（行宽、L1/L2 命中率）通过 `Observe` 上报，调节器据此调整
  下一个 batch 的行数：
  - L1 命中率低于 `LowHitRate`（缓存颠簸、内存带宽受限）→ 按 `ShrinkFactor` 收缩；
  - L1 命中率高于 `HighHitRate` 且 L1 容量有富余 → 按 `GrowFactor` 放大；
  - 结果始终钳制在 `[MinBatch=256, MaxBatch=4096]`，且单批数据尽量容纳进 L1。
- `CacheModel`（`cache.go`）：确定性缓存模型，模拟 PMU 计数器，供测试与演示。
- `Executor`（`engine.go`）：极简向量化扫描算子，把调节器接入执行循环。

默认配置：L1 = 32 KiB，L2 = 1 MiB，初始 batch = 1024。

## 运行测试

```sh
go test ./...          # 或 GO111MODULE=on go test ./...
go test -v -run TestExecutorUltraWideTable .
```

## 运行样例

```sh
go run cmd/demo/main.go
```

超宽宽表（128 字节/行）场景下，调节器检测到 L1 颠簸后按
1024 → 512 → 256 收缩，最终单批 256 行 × 128 B = 32 KiB，刚好容纳进 L1 缓存；
极窄行（4 字节/行）场景下放大至 4096。
