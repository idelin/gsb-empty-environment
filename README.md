# utc-work-calendar

在给定 UTC 开放区间上累加工作时长的 TypeScript 日历库。

## 语义

- 开放区间为半开区间 `[start, end)`（epoch 毫秒），可重叠、可乱序，使用前先做并集（`unionIntervals`）。
- 暂停日为 UTC 日期（`'YYYY-MM-DD'`），覆盖并删除当天全部开放时间（`applyPauseDays`）。
- `addWorkTime(start, durationMs, open, pauseDays)`：
  - `durationMs > 0` 从 `start` 向前累加，`< 0` 向后倒推，`0` 原样返回；
  - 恰好落在区间端点（右端/左端）即视为合法完成，不要求日历还有后续开放时间；
  - 日历开放时间不足时抛 `RangeError`。
- 返回值 `{ end, segments }` 中的 `segments` 是经过的开放区间证据，按时间先后排列、互不重叠，总长度等于 `|durationMs|`。

## 示例

```ts
import { addWorkTime } from "./src/calendar";

const open = [
  { start: Date.UTC(2026, 0, 5, 9), end: Date.UTC(2026, 0, 5, 17) },
  { start: Date.UTC(2026, 0, 6, 9), end: Date.UTC(2026, 0, 6, 17) },
];

addWorkTime(Date.UTC(2026, 0, 5, 16), 2 * 3_600_000, open, ["2026-01-06"]);
// RangeError: 1 月 6 日全休，日历不足
```

## 测试

```sh
bun test
```

测试用逐分钟有限日历（把有限时间窗切成分钟格逐格模拟）作为独立参照，
随机核对跨天、全休（全部暂停日）与负向倒推场景，并对固定跨天日历做穷举核对。
