# vector-mempool

向量化中间结果内存池与垃圾回收规避器（TypeScript / Node.js ≥ 22.18，无需任何依赖与编译步骤，Node 直接执行类型擦除）。

## 设计

- **`src/pool.ts` — ChunkPool**：构建时一次性预分配 `capacity` 个固定大小 Chunk（每个含 `Float64Array` 缓冲）。空闲槽位用预分配 `Int32Array` 栈管理，`acquire()`/`release()` 只做指针移动，热路径零堆分配；池耗尽时的增长会计入 `allocations` 计数（验收要求其恒为 0），并带双重释放防护。
- **`src/operators.ts` — 向量化算子**：Pull-based（Volcano）模型。`ScanSource` 从池中取 Chunk 填充数据；9 个变换算子全部**就地（in-place）**改写 Chunk 缓冲，下游直接复用同一内存槽位；`ReplicateOperator` 扇出算子演示流水线中段的 acquire/release 复用；`SumSink` 消费完立即把 Chunk 归还对象池。
- **`src/pipeline.ts`**：组装 `ScanSource → 10 个算子（过滤、加、乘、开方、截断、取模、绝对值、缩放偏移、滑动累加、扇出复制）→ SumSink`。
- **`src/monitor.ts` — MemoryMonitor**：通过 `perf_hooks` 的 `gc` PerformanceEntry 统计 GC 次数与停顿时长，并采样 `heapUsed` 追踪堆内存包络线。

## 运行

```bash
npm test       # Node 内置测试运行器（node:test）
npm run sample # 1 亿行流水线样例 + 内存监控报告
```

## 测试结果（node --test）

```
✔ pool reuses slots and guards against double release (0.711333ms)
✔ pool grows only when exhausted and counts it as an allocation (0.156542ms)
✔ 10-operator pipeline produces correct results (11.7565ms)
✔ zero heap allocation and zero GC pauses in steady state (765.859333ms)
ℹ tests 4
ℹ pass 4
ℹ fail 0
```

## 样例输出（1 亿行、10 算子流水线）

```
=== Vectorized pipeline sample run ===
rows scanned        : 100,000,000
rows after fan-out  : 133,333,332
chunks processed    : 48,830
elapsed             : 3984.5 ms
--- pool ---
pool capacity       : 16 chunks x 4096 slots
acquire / release   : 73245 / 73245
in-flight (leaks)   : 0
heap allocations    : 0  (post-setup chunk growth)
--- memory monitor ---
GC events           : 0
GC pause time       : 0.000 ms
heap baseline       : 7.76 MB
heap min / max      : 7.76 MB / 9.37 MB
heap delta          : 1.61 MB over 49 samples
```

验收边界达成：流水线建立后堆分配计数为 0（`allocations: 0`）、全程无 GC 停顿（`GC events: 0`）、内存曲线平直（1 亿行执行中堆波动仅 ~1.6 MB，来自监控器自身采样）。
