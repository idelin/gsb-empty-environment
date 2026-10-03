# Adaptive Batch Size Regulator

向量化执行引擎的自适应批大小（Adaptive Batch Size）调节组件，纯 Go 标准库实现。

## 设计

调节器（`adaptivebatch.Controller`）在每个 batch 执行后接收反馈
（行宽 `RowBytes`、L1/L2 缓存命中/未命中计数、耗时），并给出下一个
batch 的行数：

- **行较宽**（batch 足迹 > L1，或缓存 miss 率 EMA ≥ 20%）：几何收缩
  `1024 → 512 → 256`，直到单批数据刚好容纳进 L1，避免缓存颠簸；
- **行极窄**（miss 率 ≤ 5% 且足迹远小于 L1）：几何放大
  `1024 → 2048 → 4096`，摊薄调度开销；
- 批大小始终夹在 `[MinBatchSize=256, MaxBatchSize=4096]` 之间；
- 目标值 = `L1Size / RowBytes`，即"恰好填满 L1 的行数"。

缓存行为由 `CacheModel` 模拟（32 KiB L1 / 1 MiB L2 / 主存，带宽逐级
递减一个数量级），使调节器可以在无 PMU 的环境下端到端测试；接入真实
执行引擎时，用硬件性能计数器填充 `Observation` 即可。

## 结构

- `adaptivebatch/controller.go` — 调节器核心（反馈 EMA + 几何调节）
- `adaptivebatch/simulator.go` — L1/L2/主存带宽模型
- `adaptivebatch/executor.go` — 向量化扫描执行器（调节器 + 模型闭环）
- `adaptivebatch/controller_test.go` — 全部测试
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
export GO111MODULE=on
go test ./... -v
go run ./cmd/demo
```

## 运行样例（`go run ./cmd/demo`）

```
=== Scenario 1: ultra-wide table scan (128 B/row, L1 = 32 KiB) ===
acceptance: batch shrinks 1024 -> 256 so one batch fits exactly in L1

batch# rows     footprint    L1-hit%    batch-time   fits-L1
1      1024     128 KiB      25.0       1.802µs      false
2      512      64 KiB       50.0       709ns        false
3      256      32 KiB       100.0      163ns        true
4      256      32 KiB       100.0      163ns        true
  ... (steady state) ...
4092   256      32 KiB       100.0      163ns        true

total: 1048576 rows in 4092 batches, 669.181µs wall, 1567.0 M rows/s, final batch = 256

--- baseline: fixed batch size 1024 (no regulator) ---
total: 1048576 rows, 1.845248ms wall, 568.3 M rows/s (batch fixed at 1024)

=== Scenario 2: narrow table scan (8 B/row) ===
expectation: batch grows 1024 -> 4096 while the cache stays hot

batch# rows     footprint    L1-hit%    batch-time   fits-L1
1      1024     8 KiB        100.0      40ns         true
2      2048     16 KiB       100.0      81ns         true
3      4096     32 KiB       100.0      163ns        true
  ... (steady state) ...

total: 1048576 rows in 258 batches, 41.726µs wall, 25130.0 M rows/s, final batch = 4096
```

超宽表场景下，调节器检测到内存带宽受限（L1 命中率 25%），将批大小
从 1024 动态下调至 256（256 × 128 B = 32 KiB，刚好装满 L1），
吞吐从 568 M rows/s 提升到 1567 M rows/s（**2.76×**）。

## 测试结果（`go test ./... -v`）

```
=== RUN   TestWideRowsShrinkBatchTo256        --- PASS   # 128 B 宽行：1024 → 256，足迹恰好 = L1
=== RUN   TestNarrowRowsGrowBatchTo4096       --- PASS   # 8 B 窄行：1024 → 4096
=== RUN   TestBatchStableWhenAlreadyOptimal   --- PASS   # 32 B 行：稳定保持 1024
=== RUN   TestBatchSizeAlwaysWithinBounds     --- PASS   # 任意行宽下不越界 [256, 4096]
=== RUN   TestAcceptanceUltraWideScan         --- PASS   # 验收：端到端扫描 1024→512→256，快 2.76×
=== RUN   TestShrinksOnReportedCacheMisses    --- PASS   # 仅凭高 miss 率（足迹未超 L1）也会收缩
PASS
ok      adaptive-batch/adaptivebatch
```
