# vecpool — 向量化中间结果内存池与 GC 规避器

纯 Go 标准库实现（兼容 Go 1.11+），为向量化查询执行引擎提供
**零堆分配（Zero Allocation）** 的中间向量内存复用能力。

## 设计

- **Chunk**：固定大小（1024 个 float64）的向量内存块，承载算子间的中间结果。
- **Pool**：固定容量对象池。创建时一次性预分配全部 Chunk 与空闲槽位栈；
  之后 `Get`/`Put` 只移动槽位指针，绝不触发堆分配。池耗尽时返回 `nil`
  并计入 `Misses`，而不是悄悄向堆申请内存——零分配承诺不被破坏。
- **Pipeline**：数据以 Chunk 为单位依次流经全部算子。每经过一个算子，
  输入 Chunk 立即归还池中，输出 Chunk 从池中借出。同一时刻在途 Chunk
  恒为 3 个（源块 + 一进一出），因此容量 ≥ 3 的池即可支撑任意级数的流水线。
- **Monitor**：`SampleHeap`/`Diff` 采样堆分配、GC 次数与停顿；
  `Warmup` 预热探针，排除 Go 运行时对 `ReadMemStats` 前几次调用的
  一次性内部分配干扰。

## 内置算子（10 个）

`AddScalar` `MulScalar` `SubScalar` `DivScalar` `Abs` `Neg`
`Sqrt` `Square` `Clamp` `ScaleShift`

## 运行样例

```sh
go run ./cmd/demo
```

输出（100000 轮连续执行，10 算子流水线）：

```
round      mallocs(+)     heapAlloc(B)   gcRuns(+)  gcPause(ns)
20000      0              172368         0          0
40000      0              172368         0          0
60000      0              172368         0          0
80000      0              172368         0          0
100000     0              172368         0          0

---- steady-state summary ----
iterations          : 100000
heap mallocs        : 0
heap alloc delta    : +0 bytes
GC runs             : 0
GC pause total      : 0 ns
pool gets/puts      : 1010001/1010000
pool heap allocs    : 0
pool misses         : 0
PASS: zero heap allocation, zero GC pause, flat memory curve.
```

## 测试

```sh
go test ./... -v          # 单元测试
go test -bench . -benchmem # 基准：验证 0 B/op, 0 allocs/op
```

测试结果：

```
=== RUN   TestPipelineCorrectness   --- PASS   # 10 算子结果与标量复算一致
=== RUN   TestZeroAllocation        --- PASS   # 10000 轮：mallocs=0 heapDelta=0B gc=0 pause=0ns
=== RUN   TestChunkReuse            --- PASS   # gets=4 > capacity=3，证明槽位复用
=== RUN   TestPoolExhaustionNoAlloc --- PASS   # 池耗尽返回 nil，不偷分配堆内存
BenchmarkPipeline-10  100000  28398 ns/op  0 B/op  0 allocs/op
```

`TestZeroAllocation` 同时使用 `runtime.ReadMemStats` 差值与标准库
`testing.AllocsPerRun` 双重验证：流水线建立后的稳态执行中，
堆分配计数为零、无 GC 触发、无 GC 停顿、内存曲线平直。
