# sliding-window-quantile

滑动时间窗**精确**分位数维护器（TypeScript，零依赖）。

## 设计

- **有序统计 Treap**（`src/orderStatisticTree.ts`）：以 `(value, id)` 复合键排序的
  随机平衡 BST，子树维护 `size`，`insert` / `remove` / `select(rank)` 均为期望
  O(log n)。值可重复，靠唯一 `id` 区分——绝不批量全量排序。
- **过期最小堆**（`src/minHeap.ts`）：按 `timestamp` 排序的惰性过期队列，
  撤回（retract）产生的堆内残留条目在弹出时跳过。
- **窗口语义**（`src/slidingWindowQuantile.ts`）：窗口为半开区间
  `(now - windowMs, now]`，`timestamp <= now - windowMs` 的事件过期。
  `now` 通过 `advance(now)` 显式推进，`add` 也会把 `now` 推进到已见最新时间戳，
  因此乱序插入不会让窗口回退；到达时已过期的事件被拒绝（`add` 返回 `false`）。
- **分位数定位**：`k = floor(q * (n - 1))`，返回排序后第 `k` 个（0 起）值，不插值。

## API

```ts
const win = new SlidingWindowQuantile(10_000); // 10 秒窗口
win.add({ id: "a", timestamp: 1_000, value: 1 });   // -> boolean（false = 已过期被拒）
win.retract("a");                                    // 单条撤回 -> boolean
win.advance(now);                                    // 推进窗口右缘
win.quantile(0.5);                                   // 精确分位数
win.size;                                            // 窗口内事件数
```

## 运行

需要 Node ≥ 22.6（利用原生 type stripping 直接运行 `.ts`，无需编译）：

```sh
npm test    # node --test test/*.test.ts
npm run demo
```

## 验收边界（均有测试覆盖）

- 窗口 10 秒，持续流入 + 过期，窗口内恒为最近 10 秒事件。
- `[1, 1, 9, 20]` 时 `q=0.5` → `floor(0.5*3)=1` → **1**。
- 撤回一条 `1` 后 `[1, 9, 20]`，`q=0.5` → `floor(0.5*2)=1` → **9**。
- 时间戳与值都相等的事件按 `id` 区分：撤回精确命中一条，边界过期同时成组失效。
- 乱序插入：窗口内的迟到事件被接受，已过期的被拒绝。
- 随机化对拍：5000 步混合 add/retract/advance 与排序数组 oracle 逐步比对。
