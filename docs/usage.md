# 引擎使用指南

所有绝对时间均为 UTC  epoch 毫秒（与 `Date.now()` 同单位）；墙上时间规则
（交接时刻、Restriction）用本地组件表达，并按图层的 IANA 时区解释。

## 1. 构建跨 DST 值班表并求时间线

```ts
import { buildTimeline, whoIsOnCall, type Schedule } from "./dist/src/index.js";

const schedule: Schedule = {
  id: "team-a",
  layers: [
    {
      id: "base",
      priority: 1,
      timeZone: "America/New_York",
      members: ["alice", "bob"],
      unit: "daily",                 // daily | weekly | custom
      handoff: { hour: 9, minute: 0 },
      anchorLocal: { year: 2026, month: 3, day: 5, hour: 9, minute: 0 },
      restrictions: [
        {
          kind: "weekly",
          weekdays: [1, 2, 3, 4, 5], // 周一至周五（0=周日）
          start: { hour: 9, minute: 0 },
          end: { hour: 18, minute: 0 },
          // end <= start 即跨午夜，引擎自动拆为两段 UTC
        },
      ],
    },
  ],
  overrides: [
    {
      id: "ov-1",
      userId: "carol",
      startMs: Date.parse("2026-03-09T18:00:00Z"),
      endMs: Date.parse("2026-03-09T20:00:00Z"),
    },
  ],
};

// 精确、无重叠、半开区间 [start, end) 的 UTC 时间线；相邻同人自动合并。
const timeline = buildTimeline(schedule, startMs, endMs);

// 任意时刻谁在值班（无人返回 null）。
const user = whoIsOnCall(schedule, Date.now());
```

优先级固定为 `Override > 高序图层 > 低序图层`。一条只覆盖
`14:00-16:00` 的 Override 会把 `09:00-18:00` 的值班区间劈成三段；若替班
用户与原用户相同，相邻段会重新合并为一段。

## 2. 多级升级、空班跳级与循环

```ts
import {
  EscalationEngine,
  ScheduleTargetResolver,
  VirtualClock,
} from "./dist/src/index.js";

const clock = new VirtualClock(Date.parse("2026-06-15T10:00:00Z"));
const resolver = new ScheduleTargetResolver([schedule, backupSchedule]);
const engine = new EscalationEngine({ clock, resolver });

engine.trigger("alert-1", {
  id: "policy",
  numLoops: 2, // 完整遍历层级表的次数；到末级后回卷 L1
  levels: [
    { id: "L1", delayMinutes: 0, targets: [{ type: "schedule", scheduleId: "team-a" }] },
    { id: "L2", delayMinutes: 30, targets: [{ type: "user", userId: "sre-lead" }] },
  ],
});

engine.advance(clock, 31 * 60_000); // 驱动虚拟时钟，触发到期计时器
```

- 触发即通知 `L1`；未 Ack/Resolve 时按各级 `delayMinutes` 升级。
- 某层级在当前时刻解析为空目标（Restriction 导致无人值班）时，记录
  `level_skipped_empty` 并立刻顺延到下一个非空层级，绝不原地空等。
- 全部层级在所有循环内均为空时，立刻进入 `unassignable` 并发出
  `unassignable` 事件；遍历是有界的（层级数 × numLoops），不会死循环。

## 3. Ack / Snooze / Resolve

```ts
engine.acknowledge("alert-1", "alice");      // 取消待发升级计时器
engine.snooze("alert-1", 15 * 60_000);       // 静默，到期自动恢复并重新计时
engine.resolve("alert-1", "alice");          // 永久终结全部调度事件
```

`alert.events` 是完整的只读时间线（通知、空班跳级、计时器武装/撤销、
静默与恢复、状态变更、不可分配）。生产环境可注入 `SystemClock`；测试与
回放使用 `VirtualClock` 配合 `engine.advance(clock, ms)` 即可确定性复现。
