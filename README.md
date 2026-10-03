# Peak Concurrency Tracker

滑动窗口峰值重叠区间检测器：统计设备占用区间的并发峰值，并动态输出峰值数值及对应的全部连续时间段。

## 语义

- 区间为**半开区间** `[start, end)`：在端点 `t` 处结束的区间与在 `t` 处开始的区间不并发（端点闭合处理，不多计）。
- 区间可**乱序到达**（结束时间早的区间可能晚于开始时间晚的区间上报），也支持**延迟补录**。
- 区间可按 `id` **撤回**，峰值与区间段随每次变更动态重算。
- 输出的时间段为并发等于峰值的**极大连续段**（相邻且同峰值的段自动合并）。

## API

```ts
import { PeakConcurrencyTracker } from './src/peak-concurrency.ts';

const tracker = new PeakConcurrencyTracker();
tracker.add('A', 0, 5);    // => { value: 1, segments: [{ start: 0, end: 5 }] }
tracker.add('B', 5, 9);    // => { value: 1, segments: [{ start: 0, end: 9 }] }
tracker.add('C', 4, 6);    // => { value: 2, segments: [{ start: 4, end: 6 }] }
tracker.remove('C');       // => { value: 1, segments: [{ start: 0, end: 9 }] }
tracker.peak();            // 随时查询当前峰值
```

- `add(id, start, end): PeakResult` — 登记占用区间并返回最新峰值；`start >= end` 或重复 `id` 抛错。
- `remove(id): PeakResult` — 撤回区间并返回最新峰值；未知 `id` 抛错。
- `peak(): PeakResult` — 查询当前峰值 `{ value, segments }`。
- `intervals()` / `size` / `has(id)` — 查看当前在册区间。

实现基于端点差分（sweep-line）：`add`/`remove` 为 O(1) 均摊，`peak()` 为 O(P log P)（P 为不同端点数）。

## 运行

需要 Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译；推荐 v23.9.0，见 `.nvmrc`）。

```sh
nvm use          # 可选，切换到 .nvmrc 指定版本
npm test         # Node.js 内置测试运行器
npm run demo     # 运行验收场景示例
```

## 验收场景

1. `[0, 5)` 与 `[5, 9)` 并发为 1（端点相接不重叠）。
2. 补录迟到区间 `[4, 6)` 后，`[4, 5)` 与 `[5, 6)` 区间并发峰值上升为 2，输出连续段 `[4, 6)`。
3. 撤回补录区间后峰值恢复为 1，连续段恢复为 `[0, 9)`。
