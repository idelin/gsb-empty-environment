# Temporal Interval Connectivity Checker

判断时序图中两点在查询区间内是否**始终**连通。边在半开区间 `[start, end)` 上生效，
查询区间为 `[queryStart, queryEnd)`。

## 原理

只在区间两端采样会漏掉中间的断开。本工具按所有**有效端点**（裁剪到查询范围内的
边起止时刻）把查询区间切成原子时间片——每个时间片内边的生效状态恒定，因此对每个
时间片独立做一次 BFS 即是精确判定，而非近似。

首个不连通的时间片即为"首次断开的最小时间片"，同时返回割证据：

- `reachable`：该时间片内从源点可达的节点集合；
- `cut`：恰好一端在可达集内、且与查询区间有交集的所有边。它们在该时间片内全部
  不生效，共同构成分离源点与终点的割；每条边标注失效原因
  （`expired` 已失效 / `not-yet-active` 尚未生效）。

## 边界语义

- 空查询区间（`queryStart === queryEnd`）： vacuously 连通；
- `queryStart > queryEnd`：抛出 `RangeError`；
- 同时开闭：`[0,5)` 与 `[5,10)` 在 `t=5` 处无缝衔接，相邻有效段连续覆盖；
- 退化边（`start >= end`）从不生效，直接忽略；
- 边按无向处理；`source === target` 恒连通。

## 用法

```ts
import { checkConnectivity } from './src/temporalConnectivity.ts';

const result = checkConnectivity(
  [
    { u: 'a', v: 'b', start: 0, end: 4 },
    { u: 'a', v: 'b', start: 6, end: 12 },
  ],
  'a',
  'b',
  0,
  10,
);
// => { connected: false, sliceStart: 4, sliceEnd: 6,
//      reachable: ['a'],
//      cut: [ { edge: …[0,4), reason: 'expired' },
//             { edge: …[6,12), reason: 'not-yet-active' } ] }
```

## 运行测试

需要 Node >= 22.6（`--experimental-strip-types` 直接运行 TypeScript）：

```sh
npm test
```
