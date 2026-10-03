# 向量化中间结果内存池与垃圾回收规避器

执行算子间中间向量的内存复用池：预分配固定大小的 Chunk 内存块，算子计算完毕即归还，
下游直接复用内存槽位，使整个查询执行期间达成**零堆内存分配（Zero Allocation）**、
**无 GC 停顿**、**内存曲线平直**。

## 运行环境

- Node.js ≥ 23.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译步骤、零依赖）

## 快速开始

```bash
npm test       # Node.js 内置测试运行器（node:test）
npm run demo   # 运行样例：10 算子流水线 × 20000 轮稳态执行 + 验收输出
```

## 架构

```
src/chunk.ts      Chunk（定长 1024 槽位 Float64Array）+ ChunkPool（预分配对象池）
src/operators.ts  10 个向量化算子（scan / filter / add / mul / sqrt / abs / clamp / sin / negate / sum）
src/pipeline.ts   Pipeline：算子链执行器，聚合结果写入预分配结果单元
src/monitor.ts    MemoryMonitor：堆采样（自身零分配）+ GC 事件观测
examples/demo.ts  运行样例与验收输出
test/             node:test 单元测试与验收测试
```

### 关键设计

- **预分配对象池**：`ChunkPool` 构造期一次性分配全部 Chunk；稳态 `acquire`/`release`
  只做数组 push/pop。池耗尽时**抛错而非扩容**，从机制上保证零分配承诺可验证。
- **算子协议**：转换算子 acquire 输出 Chunk → 紧密循环就地计算 → release 输入 Chunk →
  返回输出 Chunk；下游立即复用归还的槽位。
- **结果单元传参**：聚合结果写入预分配的 `Float64Array` 单元（`pipeline.result[0]`），
  不做标量返回，消除跨函数边界的 double 装箱（HeapNumber 分配）。
- **状态单元化**：算子的可变数值状态（如 LCG 种子）保存在 `Uint32Array` 单元而非
  number 字段——uint32 状态超 Smi 范围时字段反复装箱会产生堆分配。
- **V8 陷阱规避**：逐元素循环中避免 `v < lo ? lo : v` 这类 Smi/Float64 混合三元表达式
  （其 phi 节点会被选择为 Tagged 表示，导致每元素装箱一次）；改用 `Math.min/max`。

## 验收口径

`examples/demo.ts` 构建 10 算子流水线，预热后连续执行 20000 轮，判定：

1. **零堆分配**：`ChunkPool.heapAllocations` 在流水线建立后增量为 0；
2. **无 GC 停顿**：`perf_hooks` GC 观测在稳态区间事件数为 0、停顿 0 ms；
3. **内存曲线平直**：稳态区间 `heapUsed` 振幅 < 64 KiB。

## 运行样例输出（实测）

```
=== 向量化中间结果内存池 · 运行样例 ===
流水线算子数: 10
Chunk 池容量: 16 块 × 1024 槽位 (Float64)
建立期堆分配次数: 16（仅预分配阶段）

--- 稳态执行结果（验收区间） ---
执行轮数:            20000
校验和 (checksum):   -339666.749943
堆分配增量:          0 次
Chunk 借还次数:      acquire=270000 release=270000
GC 事件数:           0
GC 停顿总时长:       0.000 ms
heapUsed 最大振幅:   60.5 KiB

--- 内存曲线（每 2000 轮采样） ---
  轮次   heapUsed(MiB)  heapTotal(MiB)   rss(MiB)
      0           8.18           13.69      88.78
   2000           8.23           13.69      88.84
   4000           8.29           13.69      88.86
   6000           8.29           13.69      88.86
   8000           8.29           13.69      88.86
  10000           8.29           13.69      88.86
  12000           8.29           13.69      88.86
  14000           8.29           13.69      88.86
  16000           8.29           13.69      88.86
  18000           8.29           13.69      88.86
  20000           8.29           13.69      88.86

验收: 零堆分配=✔  无GC停顿=✔  内存曲线平直(振幅<64KiB)=✔
```

## 测试结果（实测）

```
✔ 构造期一次性预分配全部 Chunk
✔ Chunk 底层存储为定长 Float64Array
✔ 归还后槽位被直接复用，不产生新分配
✔ acquire 时逻辑长度被重置
✔ 池耗尽时抛错而非悄悄扩容（零分配承诺的机制保障）
✔ 借还计数平衡
✔ 非法容量被拒绝
✔ 流水线包含 10 个算子
✔ 流水线计算结果与参考实现一致
✔ 稳态执行期堆分配计数为零且借还平衡
✔ 稳态执行期无 GC 事件
✔ 稳态执行期内存曲线平直
✔ 同一批 Chunk 槽位在算子间循环复用
ℹ tests 13  ℹ pass 13  ℹ fail 0
```
