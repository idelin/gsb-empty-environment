# Sliding Window Exact Quantile

滑动时间窗精确分位数维护器（TypeScript）。基于**有序统计跳表**
（order-statistics skip list，Redis zskiplist 风格，带 span 跨度计数），
支持窗口内事件过期、乱序插入与单条事件撤回，分位数按
`floor(q * (n - 1))` 秩严格定位，全程无批量全量排序。

## 设计

`SlidingWindowQuantile`（src/slidingWindowQuantile.ts）内部维护三份结构：

- `byValue`：按 `(value, id)` 字典序排列的跳表，支持 O(log n) 插入、
  删除与按秩选择（select-by-rank）。
- `byTime`：按 `(timestamp, id)` 排列的跳表，每次窗口推进时从表头
  逐条弹出已过期事件，单条过期代价 O(log n)。
- `events`：`id -> event` 的 Map，支撑单条事件精确撤回。

相等值通过复合键 `(value, id)` 中的唯一 id 去重区分，因此 `[1, 1]`
中撤回任意一条 `1` 都精确生效，另一条保留。

窗口语义：事件在 `timestamp > now - windowMs` 时存活，即窗口为
半开区间 `(now - windowMs, now]`；`now` 只前进不后退，乱序（迟到）
事件只要尚未过期即可插入，已过期的事件会被拒绝。

## API

```ts
const w = new SlidingWindowQuantile(10_000); // 窗口 10 秒
w.add({ id: "a", timestamp: 1000, value: 1 });
w.add({ id: "b", timestamp: 1000, value: 1 });
w.add({ id: "c", timestamp: 1000, value: 9 });
w.add({ id: "d", timestamp: 1000, value: 20 });
w.quantile(0.5);   // 1  (floor(0.5 * 3) = 1 -> sorted[1])
w.retract("a");    // 撤回一条 1
w.quantile(0.5);   // 9  (剩余 [1, 9, 20], floor(0.5 * 2) = 1)
w.advance(20_000); // 推进窗口，过期事件自动移除
```

## 运行

需要 Node.js >= 22.6（使用 `--experimental-strip-types` 直接运行
TypeScript，无构建步骤、无第三方依赖）。

```sh
npm test       # Node.js 内置测试运行器 (node:test)
npm run demo   # 运行样例：验收场景 + 持续流入/过期的滑动流
```

## 测试结果

```
ok 1 - insert and select by rank in sorted order
ok 2 - duplicate values stay distinct via id tie-break
ok 3 - remove returns false for missing keys
ok 4 - select out of range returns null
ok 5 - randomized insert/remove/select matches sorted-array model
ok 6 - acceptance: q=0.5 over [1, 1, 9, 20] is 1
ok 7 - acceptance: retracting one of the equal 1s yields 9
ok 8 - floor(q*(n-1)) rank positioning
ok 9 - events expire as the 10s window slides
ok 10 - boundary: event at exactly now - windowMs expires, one ms later survives
ok 11 - out-of-order insertion stays exact
ok 12 - single-event retraction among duplicates
ok 13 - continuous inflow with expiry matches naive model
ok 14 - invalid arguments are rejected
# tests 14
# pass 14
# fail 0
```

其中测试 13 以 2000 步随机流入/撤回/推进对照朴素排序模型逐步校验，
测试 5 以 5000 次随机操作对照排序数组模型校验跳表秩选择的正确性。
