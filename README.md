# slot-priority-queue

TypeScript 最小优先队列，支持改优先级、取消和取最小。

## 特性

- **稳定句柄**：`insert` 返回 `{ slot, generation }` 句柄。槽位在取消/取出后回收复用，
  复用时代次（generation）递增，旧句柄立即失效：`cancel` 返回 `false`，
  `setPriority` / `batchSetPriority` 抛出 `StaleHandleError`。
- **同优先级 FIFO**：优先级相同按入队序号排序；序号为 `bigint`，
  超过 `Number.MAX_SAFE_INTEGER` 仍保持精确。
- **原子批量更新**：`batchSetPriority` 任一更新失败时整体回滚——
  同时恢复堆数组与「槽位 → 堆下标」索引映射，队列可继续正常使用。
- **重复取消安全**：`cancel` 幂等，重复取消返回 `false` 且不破坏队列。

## API

```ts
const q = new PriorityQueue({ initialSeq: 0n });
const h = q.insert(5);            // -> Handle
q.setPriority(h, 1);              // 改优先级（旧句柄抛 StaleHandleError）
q.cancel(h);                      // -> boolean（重复取消返回 false）
q.batchSetPriority([{ handle: h, priority: 2 }]); // 原子，失败整体回滚
q.peek();                         // -> { priority, seq } | undefined
q.extractMin();                   // -> { priority, seq } | undefined
q.size; q.has(h);
q.debugCheckInvariants();         // 内部一致性校验（测试/调试用）
```

## 运行测试

需要 Node ≥ 23.6（原生 TypeScript 类型擦除）：

```sh
node --test test/*.test.ts
```

测试包含：独立排序列表模型对随机操作（插入/取出/取消/改优先级/批量更新，
含失败注入回滚）的逐步核对、序号超过安全整数范围、槽位复用句柄失效、重复取消。
