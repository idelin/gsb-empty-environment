# compressedbitmap — 压缩位图 + 确定性模拟与故障复现工具 (Go)

纯标准库实现，无第三方依赖。包含两部分：

- **`bitmap`**：Roaring 风格压缩位图。64 位 ID 按高 48 位分桶，每个桶（65536 个值）
  自动在 **ArrayContainer**（≤4096 个元素）、**BitmapContainer**（8192 字节位图）与
  **RunContainer**（RLE 区间）之间选择最紧凑表示；支持 `And / Or / Xor` 及
  `AndMany / OrMany / XorMany` 批量运算，全部非变异（返回新位图）。
- **`sim`**：离散时间、事件驱动的确定性模拟框架。同一种子产生完全一致的
  操作流、事件调度、位图状态与滚动校验和，支持百万步回归测试；
  `Recorder` 记录操作日志，`Replay` 逐步重放并逐条校验校验和，
  发现分叉即返回 `Fault` 并可生成最小复现前缀（bug 复现）。

## 构建与测试

```sh
go test ./...          # 单元测试 + 100 万步确定性回归（黄金校验和）
go test -race ./...    # 并发安全（race 模式自动缩短回归调度长度）
go test -run xxx -bench . -benchtime 1s ./bitmap/   # 性能基准
```

## 运行样例

```sh
go run ./cmd/demo                          # 容器自动选择 + 10^10 规模交并 + 100 万步模拟
go run ./cmd/demo -steps 1000000 -log ops.log   # 记录操作日志
go run ./cmd/demo -replay ops.log               # 重放并逐条校验
go run ./cmd/demo -log ops.log -fault           # 注入故障并生成最小复现 repro.log
```

## API 速览

```go
b := bitmap.New()
b.Add(42)                    // 稀疏单点 -> ArrayContainer
b.AddRange(1<<20, 1<<21)     // 密集区间 -> RunContainer (RLE)
b.Optimize()                 // 全量重压缩
c := a.And(b)                // 非变异集合运算
d := bitmap.OrMany(a, b, c)  // 批量运算
safe := bitmap.NewSafe()     // RWMutex 包装的并发安全位图

e := sim.NewEngine(42, 8)    // seed, 位图数量
sum := e.Run(1_000_000, hook)// 确定性推进 100 万步
rec := sim.NewRecorder(w)    // 记录 op 流
fault := sim.Replay(42, 8, records) // 重放校验，nil 表示一致
```

## 确定性契约

- 所有随机性来自单一种子；稀疏填充使用独立的 `fillRng`，只在 `apply` 内推进，
  因此重放记录日志时可逐位复现状态。
- 事件堆按 `(time, seq)` 排序，相同时间的事件按入队顺序弹出。
- 每步将目标位图的结构校验和（容器键/类型/基数）与操作折叠进滚动 FNV 哈希；
  黄金校验和硬编码于 `sim/sim_test.go`，任何行为变更都会打破回归测试。
