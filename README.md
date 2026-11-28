# 跨 DST 值班表求解与多级告警升级引擎

PagerDuty 风格 SRE 响应控制面的核心求解库，纯 TypeScript、零运行时依赖，基于
平台内置的 `Intl.DateTimeFormat`（全 ICU）完成 IANA 时区与 DST 计算，绝不使用
固定 `86400s` 的朴素日加减。

## 模块结构

- `src/time.ts` — DST 感知的墙上时间引擎：偏移探测、二分定位偏移跳变、春令
  gap 钳制、秋令 overlap 的 earlier/later 消歧、按日历单位（日/时/分）推进。
- `src/schedule.ts` — 多图层值班表求解：Base Rotation、周级 Restriction（含跨
  午夜拆段）、临时 Override，按 `Override > 高序图层 > 低序图层` 叠加，输出
  归一化、无重叠的精确 UTC 时间线。
- `src/escalation.ts` — 多级升级状态机：空班即时跳级、全空班
  `UNASSIGNABLE_ALERT`、`numLoops` 回卷、`Ack` / `Snooze` / `Resolve` 的
  计时器精确撤销与重激活；时钟可注入（生产用 `RealClock`，测试用 `VirtualClock`）。

## 运行测试

本镜像的 Node 未编译原生 TypeScript 支持，因此测试脚本先用全局 `tsc` 编译，
再由 Node 内置 test runner 执行编译产物：

```bash
npm test          # tsc 编译 + node --test
npm run typecheck # 仅严格类型检查
npm run build     # 输出带 .d.ts / sourcemap 的 dist-test 产物
```

## 关键设计

### 1. 墙上时间优先，UTC 仅用于输出

交接时刻永远表达为「某 IANA 时区的本地墙上时间」。`addWallDuration` 先在墙上
分量上做日历加法，再解析回 UTC，因此美东每日 09:00 交接在春令跳空日真实持续
23 小时、秋令重叠日持续 25 小时，但墙上落点始终是 09:00。

```ts
import { addWallDuration, resolveWall, wallPartsAt } from "./src/time.js";

const sat = resolveWall("America/New_York", {
  year: 2026, month: 3, day: 7, hour: 9, minute: 0, second: 0, millisecond: 0,
}).instant;
const sun = addWallDuration("America/New_York", sat, { days: 1 });
sun - sat === 23 * 3_600_000;            // 春令跳空
wallPartsAt("America/New_York", sun).hour === 9;
```

不存在的墙上时间（春令 gap，如纽约 02:30）被钳制到 gap 之后的第一个真实瞬间；
重复的墙上时间（秋令 overlap，如纽约 01:30）通过 `disambiguation: "earlier" |
"later"` 显式选择第几次出现。

### 2. 叠加优先级与一裂三

`buildTimeline` 收集所有图层、限制窗、Override 的边界做扫描线切分，每个最小
区间取最高优先级覆盖；相邻且同人的区间自动合并。一条仅覆盖 14:00–16:00 的
Override 会把 09:00–18:00 的值班区间劈成三段（同人 Override 则合并回单段）。

```ts
import { buildTimeline, onCallAt, coverageTimeline } from "./src/schedule.js";

const timeline = coverageTimeline(schedule, windowStartUtc, windowEndUtc);
onCallAt(schedule, instantUtc); // 限制窗外返回 null（空班）
```

### 3. 升级与空班跳级

- 触发即解析 L1 并通知；层级目标在「评估那一刻」解析，空图层立即发出
  `EMPTY_LEVEL_SKIP` 并跳到下一层，不等待该层 `delayMinutes`。
- 只有真正通知到人的层级才会挂计时器；首个 pass 全部为空则立即
  `UNASSIGNABLE_ALERT`，且不挂任何计时器，从结构上杜绝死循环。
- `numLoops` 表示首轮之后的额外完整轮次；末级延时到期后回卷 L1，达到上限后
  发出 `ESCALATION_EXHAUSTED`。
- `ack` 取消待发升级计时器；`snooze(ms)` 取消升级计时器并挂唤醒计时器，
  到期后状态回到 `triggered` 并从 L1 重新计时；`resolve` 清除全部计时器并
  永久终结。

```ts
import { EscalationEngine, VirtualClock } from "./src/escalation.js";

const clock = new VirtualClock(Date.parse("2026-03-02T15:00:00Z"));
const engine = new EscalationEngine(policy, { schedules, clock });
const alert = engine.triggerAlert("INC-1");
clock.advance(5 * 60_000); // 确定性推进，观察 NOTIFY / SKIP / EXHAUSTED 事件
alert.ack();
```

## 验收矩阵

| 场景 | 测试位置 |
| --- | --- |
| 美东/欧洲春令 23h、秋令 25h 交接对齐 | `test/time.test.ts` |
| gap 钳制、overlap earlier/later 消歧 | `test/time.test.ts` |
| 局部 Override 一裂三与相邻同人合并 | `test/schedule.test.ts` |
| Restriction 跨午夜拆段、周末空班、DST 时长 | `test/schedule.test.ts` |
| 图层优先级与确定性平局裁决 | `test/schedule.test.ts` |
| Restriction 空班即时跳级 | `test/escalation.test.ts` |
| 全空班 UNASSIGNABLE 且无计时器/无死循环 | `test/escalation.test.ts` |
| numLoops 回卷轮次上限 | `test/escalation.test.ts` |
| Ack / Snooze / Resolve 计时器撤销与重激活 | `test/escalation.test.ts` |

全部 31 个测试用例在 `npm test` 下通过。
