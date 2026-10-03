# 会话超时可撤销合并窗口（Session Window Merger）

按相邻事件时间间隔不超过 `gap` 合并会话窗口，输出窗口合并/分裂的
`retract`/`upsert` 变更流，并在水位线推进后丢弃超出最大延迟范围的状态。

## 语义

- **合并**：相邻事件时间差 `<= gap` 的事件并入同一会话窗口 `[start, end]`。
- **迟到桥接**：迟到事件可连接原本独立的两个窗口，旧窗口发 `retract`，
  合并后的窗口发 `upsert`。
- **删除分裂**：删除桥接事件后窗口重新分裂，同样以 `retract` + `upsert`
  变更流通知下游。
- **同键更新**：窗口边界不变但事件数变化时，仅发 `upsert`（无需 retract）。
- **水位线**：`advanceWatermark(wm)` 后，`end < wm - maxLateness - gap`
  的窗口状态被丢弃（视为已最终确定，不再发 retract）；
  `t < wm - maxLateness` 的事件视为过于迟到，直接丢弃。

## 环境要求

Node.js >= 22.6（利用内置类型擦除直接运行 TypeScript，无需编译步骤；
Node >= 23.6 默认开启）。测试使用 Node 内置 `node:test`，零依赖。

## 运行

```bash
npm test    # 运行全部内置测试
npm run demo  # 运行验收场景样例
```

## API

```ts
import { SessionWindowMerger } from './src/session-windows.ts';

const merger = new SessionWindowMerger({ gap: 5, maxLateness: 10 });
merger.addEvent('e0', 0);    // -> [upsert [0, 0]]
merger.addEvent('e9', 9);    // -> [upsert [9, 9]]（间隔 9 > gap，独立窗口）
merger.addEvent('e4', 4);    // -> [retract [0,0], retract [9,9], upsert [0,9]]
merger.removeEvent('e4');    // -> [retract [0,9], upsert [0,0], upsert [9,9]]
merger.advanceWatermark(30); // 丢弃超出最大延迟范围的状态
```

## 样例输出（`npm run demo`）

```
== 事件 t=0 到达 ==
  upsert  window=[0, 0] count=1

== 事件 t=9 到达（间隔 9 > gap=5，独立窗口） ==
  upsert  window=[9, 9] count=1

== 迟到事件 t=4 到达（桥接两窗口） ==
  retract window=[0, 0] count=1
  retract window=[9, 9] count=1
  upsert  window=[0, 9] count=3

== 删除事件 t=4（窗口重新分裂） ==
  retract window=[0, 9] count=3
  upsert  window=[0, 0] count=1
  upsert  window=[9, 9] count=1

== 水位线推进到 30（丢弃超出最大延迟的状态） ==
  purged   window=[0, 0]（已最终确定，不再发 retract）
  purged   window=[9, 9]（已最终确定，不再发 retract）
  剩余状态事件数: 0

== 过于迟到的事件 t=5（< watermark 30 - maxLateness 10，被丢弃） ==
  (无窗口变更)
```

## 测试结果（`npm test`）

```
✔ 验收场景：gap=5，迟到事件合并双窗口，删除后重新分裂
✔ 相邻事件间隔恰好等于 gap 时合并
✔ 乱序事件插入窗口中间只扩展窗口
✔ 水位线推进后丢弃超出最大延迟范围的状态
✔ 水位线单调推进，回退无效
✔ 删除未知事件与重复添加同一 id
✔ 变更流往返一致：合并后再删除桥接事件可完全还原
ℹ tests 7  ℹ pass 7  ℹ fail 0
```

## 文件结构

- `src/session-windows.ts` — 核心实现（窗口合并、diff 变更流、水位线清理）
- `test/session-windows.test.ts` — Node 内置测试（`node:test` + `node:assert`）
- `examples/demo.ts` — 验收场景运行样例
