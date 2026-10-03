# Matrix Clock 与分布式垃圾回收

TypeScript 实现的矩阵时钟（Matrix Clock）与基于它的分布式垃圾回收（DGC），
使用 Node.js 内置测试运行器（`node:test`）验证，无任何第三方依赖。

## 核心概念

- **矩阵时钟**：每个节点维护 `matrix[r][c]` —— 「据我所知，节点 r 已确认知悉
  来自节点 c 的事件直到逻辑时间 `matrix[r][c]`」。第 self 行即本节点的向量时钟，
  其余行为通过消息交换学到的其他节点的时钟认知。
- **GC 水位线**：对事件源 c，`gcWatermark(c) = min_r(matrix[r][c])`，即集群中
  **所有节点**都已确认知悉的最大 LSN。`LSN <= 水位线` 的历史日志可安全物理截断。
- **消息传播**：每条消息携带发送方的完整矩阵快照与日志副本；接收方逐行逐列
  取最大值合并（知识沿 gossip 路径传递性传播，时钟单调不回退）。

## 目录结构

- `src/matrix-clock.ts` — 矩阵时钟：`tick` / `acknowledge` / `snapshot` / `merge` / `gcWatermark`
- `src/node.ts` — 集群节点：本地事件（分配 LSN）、消息收发、日志复制、`collectGarbage()` 物理截断
- `src/demo.ts` — 3 节点（A/B/C）多轮通信演示
- `test/` — `node:test` 单元与场景测试

## 运行

要求 Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译）。

```bash
npm run demo   # 运行 3 节点演示
npm test       # 运行 node:test 测试（12 个用例）
```

## 演示说明（验收边界）

1. **Round 1**：A 产生 LSN 1–5，复制到 B、C。
2. **Round 2**：A 产生 LSN 6–10 并复制；B、C 各有本地事件并互相交换。
3. **Checkpoint 1**：A 尚未收到回执，矩阵中 B/C 行对 A 列为 0，
   `gcWatermark(A) = 0`，`collectGarbage()` 不截断任何条目（安全性）。
4. **Round 3**：B、C 将各自时钟认知回传给 A，A 的矩阵第 A 列变为
   `[10, 10, 10]` —— 每个节点都已确认知悉 A 的事件直到 LSN=10。
5. A 再产生 LSN 11–12（尚未传播），随后触发物理日志截断：
   LSN 1–10 被回收，LSN 11–12 保留。

关键输出：

```
  matrix @ A (rows = knowledge of, cols = events from)
         A   B   C
  A    10   1   1
  B    10   1   1
  C    10   1   1
  A gcWatermark(A) = 10  -> every node acknowledged A's events up to LSN=10
  ...
    origin A: watermark=10, reclaimed 10 entries
  after truncation:
  A physical log [own entries]: LSN=11, LSN=12
```

## 测试覆盖

- 时钟语义：`tick` / `acknowledge` / `merge` 单调性、幂等性、快照深拷贝
- 水位线：列最小值语义、受最慢节点限制
- 安全性：任一节点未确认前不发生截断
- 回收正确性：水位线=10 时恰好截断 LSN 1–10、保留 11–12，且 GC 单调幂等
