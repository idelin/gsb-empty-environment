全球多细胞服务升级时，既要防止一对互为灾备的细胞同时发布失去冗余，又要避开各区域各自的本地业务高峰冻结窗，并控制全网同时处于升级与观察期的总容量占比。为发布编排控制面交付可嵌入的 TypeScript 调度组件。输入细胞集合（≤9 个，每个含所属区域、流量权重、升级耗时与健康观察耗时、区域内每日本地时间冻结窗及已知 UTC 偏移区间——覆盖夏令时跳变）、全局最大同时不可用流量占比上限 U、每区域最大并发升级细胞数 M、以及互为灾备互斥的细胞无向边集合。规则：一个细胞一旦在时刻 t 启动，将在 `[t, t+upgrade+bake)` 期间全程占用所在区域并发槽与全网不可用流量预算，且整个区间不得与该区域任意冻结窗有正长度交集（可在冻结窗结束整点启动，或恰好在冻结窗开始整点结束）；互为灾备的两个细胞运行区间不可重叠；允许在全局起始时刻后任意合法整分钟启动。求使全部细胞完成发布（makespan）最早的精确排期，并列按各细胞启动时刻之和最小，再按启动向量字典序最小。验收在小实例上用关键事件时刻状态搜索对照最优完成时刻与全部约束，覆盖跨夏令时跳变导致冻结窗 UTC 偏移变化、灾备配对互斥推迟、总流量占比超限分波，以及某细胞耗时超过两个冻结窗之间间隙导致不可行报错。

## 可嵌入 API

核心实现位于 `src/scheduler.ts`，公共导出位于 `src/index.ts`。

```ts
import { scheduleUpgrades, validateSchedule } from "./src/index.js";

const result = scheduleUpgrades({
  startUtc: "2026-03-29T00:30:00Z",
  U: { numerator: 2, denominator: 3 }, // 也可传 2 / 3 这样的 number
  M: 2,
  totalTrafficWeight: 6,               // 缺省为所有细胞权重之和
  regions: {
    "cn-east": {
      freezeWindows: [{ start: "12:00", end: "14:00" }],
      utcOffsetIntervals: [
        { endUtc: "2026-03-29T00:00:00Z", offsetMinutes: 480 },
        { startUtc: "2026-03-29T00:00:00Z", offsetMinutes: 480 },
      ],
    },
  },
  cells: [
    {
      id: "cell-a",
      regionId: "cn-east",
      trafficWeight: 2,
      upgradeMinutes: 20,
      bakeMinutes: 10,
    },
    { id: "cell-b", regionId: "cn-east", trafficWeight: 2, upgradeMinutes: 30 },
    { id: "cell-c", regionId: "cn-east", trafficWeight: 2, upgradeMinutes: 15 },
  ],
  disasterRecoveryEdges: [["cell-a", "cell-b"]],
});

console.log(result.startMinutes);      // 相对全局起点的整数分钟
console.log(result.makespanMinutes);  // 最早完成时刻
console.log(result.sumStartMinutes);  // 并列第二目标

const check = validateSchedule(input, result.startMinutes);
if (!check.valid) throw new Error(check.violations[0].message);
```

`scheduleUpgrades` 的别名包括 `schedule`、`planUpgrades`、`solveSchedule` 和 `optimizeSchedule`。不想用异常控制流程时，可调用 `tryScheduleUpgrades`，它返回 `{ success, result | error }`。

## 输入约定

- 时间内部以 UTC epoch minute 的整数表示；ISO 字符串、`Date`、epoch minute 数字均可作为时刻输入。
- `upgradeMinutes + bakeMinutes` 是并发槽、流量预算、灾备互斥和冻结窗检查共同使用的占用区间长度。
- `freezeWindows` 使用区域本地钟的每日 `HH:mm`；`end <= start` 时自动按跨午夜窗口处理，也可显式传 `overnight: true`。
- `utcOffsetIntervals` 为半开 UTC 区间 `[startUtc, endUtc)` 上的整数分钟偏移。区间内发生夏令时跳变时，冻结检查会在偏移边界切分 UTC 区间，因此不会错误地只用一个偏移换算整个作业。
- `U` 推荐传精确有理数 `{ numerator, denominator }` 或 `"2/3"`；也支持普通数字。
- `M` 可传统一整数，也可传 `{ "cn-east": 2, "cn-north": 1 }` 按区域覆盖。
- DR 边可传 ID/下标二元组，也可传 `{ a, b }`、`{ from, to }`、`{ cellA, cellB }`。

## 输出与错误

`ScheduleResult` 包含：

- `startMinutes` / `starts` / `startVector`：按输入细胞顺序排列的相对启动分钟。
- `endMinutes`：每个细胞的 `start + upgrade + bake`。
- `makespanMinutes`：最大结束时刻。
- `sumStartMinutes`：相对启动时刻之和。
- `cells`：带绝对 UTC 字符串、区域、权重和耗时的完整排期。

不可行时抛出 `ScheduleInfeasibleError`。典型 `reason` 包括：

- `CELL_NEVER_FITS_FREEZE_WINDOWS`：细胞耗时超过所有周期性冻结窗间隙。
- `CELL_EXCEEDS_GLOBAL_BUDGET`：单细胞权重已超过 `U` 预算。
- `REGION_CONCURRENCY_ZERO`：区域 `M=0`。
- `NO_FEASIBLE_SCHEDULE`：关键事件范围内无可行排期。

输入结构错误抛出 `ScheduleInputError`。

## 精确搜索

最优排期中的每个启动时刻只可能是：

1. 全局起点；
2. 所属区域某个冻结窗的结束整点；
3. 另一个细胞的占用区间结束整点。

原因是：若启动时刻不属于上述边界，则把该细胞整体左移一个整数分钟不会触碰新的冻结结束边界，也不会在区域槽、全网预算或灾备边上产生新的释放点冲突；这会提前该细胞并降低启动时刻之和，与三级最优性矛盾。

实现因此执行关键事件记忆化搜索：

- 状态为 `(当前事件时刻, 未启动掩码, 活动细胞结束时刻向量)`。
- 在每个事件时刻枚举所有可同时启动的细胞子集。
- 用位掩码检查灾备边，用活动权重和区域计数检查累计容量。
- 跳到下一个活动结束点；无活动细胞时跳到下一个冻结/偏移关键事件。
- 后缀方案依次比较最终 `makespan`、未启动细胞启动和、按细胞下标排列的启动向量字典序。

搜索视界先尝试构造一个全串行可行排期作为已证明上界；构造失败时使用夏令时尾段周期间隙推导的安全视界。由于细胞数不超过 9，位掩码和记忆化状态足够小。

## 验收与测试

```bash
npm run typecheck
npm test
```

测试包含 18 个用例：指定的 DST 偏移变化、灾备推迟、流量分波、冻结间隙不可行，以及 8 组小实例的逐分钟暴力枚举对照。暴力预言机独立检查每个整数启动向量的冻结、DR、区域并发和全网权重，并比较 `(makespan, sum, lexVector)`；同时断言调度器的每个启动点都属于全局起点、冻结结束点或细胞结束点。
