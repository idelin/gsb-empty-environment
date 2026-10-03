# vecpool — 向量化中间结果内存池与 GC 规避器

执行算子间的中间向量内存复用池。预分配固定大小的 Chunk 内存块，算子计算
完毕后将 Chunk 归还对象池，下游直接复用内存槽位，整个查询执行期间达成
零堆内存分配（Zero Allocation），无 GC 停顿，内存曲线平直。

## 结构

- `chunk.go` — `Chunk`（固定 1024 个 float64 的内存块）与 `Pool`
  （构造时一次性预分配全部内存，`Get`/`Put` 不再分配，池空时阻塞等待归还）
- `operator.go` — 向量化算子：逐元素变换（Add/Mul/Square/Sqrt/Abs/Sin/
  Log1p/Clamp/SubtractMean）与压缩算子 `FilterGreaterThan`
- `pipeline.go` — `Pipeline`：10 个算子串联，算子间通过池化 Chunk 传递中间结果
- `monitor.go` — `MemMonitor`：采样 `runtime.MemStats`，验证堆分配计数、
  GC 周期数、STW 停顿在流水线建立后均为零
- `cmd/demo/main.go` — 运行样例：流式执行 20 万个 Chunk 并打印内存曲线

## 运行样例

```sh
go run ./cmd/demo
```

## 测试

```sh
go test ./... -v
```

关键验收测试：

- `TestZeroAllocationDuringExecution` — `testing.AllocsPerRun` 断言完整
  10 算子流水线每次执行堆分配为 0
- `TestMonitorFlatMemoryCurve` — 5 万个 Chunk 流经流水线，监控器断言
  TotalAlloc 增量 = 0、GC 次数 = 0、STW 停顿 = 0
- `TestChunkMemoryReuse` / `TestPoolGetPutZeroAllocation` /
  `TestPoolExhaustionBlocks` — 池的复用、零分配与阻塞语义
- `TestPipelineCorrectness` — 与标量参考实现逐元素比对
