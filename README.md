# allen-scheduler

基于 **Allen 区间代数** 的时区与日历排程时间区间代数约束求解器（TypeScript，零依赖）。

## 功能

- **定性层**：Allen 区间代数 13 个基本关系（`before`、`meets`、`overlaps`、`starts`、
  `during`、`finishes`、`equal` 及其逆关系）的约束网络；复合表由端点序枚举自动推导；
  用 **PC-2 路径一致性算法** 做传递闭包传播，检测定性不可满足并推导隐含关系。
- **定量层**：把传播后的关系标签翻译成端点差分约束（严格序用 `epsilon` 间隙），
  构造 **STP（Simple Temporal Problem）** 并用 Floyd-Warshall 求最紧界，
  输出每个任务的最早/最晚开始与结束时间窗、总时差。
- **关键路径**：以最小总时差识别关键任务，给出项目全局关键路径窗口。
- **时区与日历**：把求解器的工作分钟偏移投影到带固定 UTC 偏移、工作日与
  每日工作时段的墙钟日历（自动跳过周末/非工作时段）。

## 运行要求

Node.js ≥ 22.6（推荐 ≥ 23.6，原生 TypeScript type-stripping，无需编译）。

```bash
npm test        # node --test test/*.test.ts
npm run example # node examples/project-schedule.ts
```

## 验收场景

`examples/project-schedule.ts`：任务 A 必须在 B 之前结束（`before`），B 与 C 部分重叠
（`overlaps`），C 必须在工期第 5 天前开始，项目须在第 9 天结束前完成；另有 1 天工期
的松弛任务 D 只需在 C 开始前结束。1 工作日 = 480 工作分钟，`epsilon = 1` 分钟。

### 样例输出

```
=== 1. 定性传播（Allen 区间代数 · 传递闭包） ===
  A -> B: {b}
  A -> C: {b}        ← 由 A before B、B overlaps C 传递推导
  A -> D: {b,m,o,s,d,f,eq,bi,mi,oi,si,di,fi}
  B -> C: {o}
  B -> D: {bi,mi,oi,si,di}
  C -> D: {bi}

=== 2. 定量窗口（STP，单位 = 工作分钟，1 工作日 = 480） ===
  任务   ES     LS     EF     LF     时差    关键
  A       0   1437    960   2397   1437    *
  B     961   2398   2401   3838   1437    *
  C     962   2399   2882   4319   1437    *
  D       0   1918    480   2398   1918

=== 3. 关键路径 ===
  关键任务: A -> B -> C
  关键路径窗口: [0, 4319] 工作分钟（第 0 天 ~ 第 9 天）
  任务 A 最迟结束时间: 第 5 天前 3 分钟（LF = 2397）

=== 4. 墙钟投影（UTC+8，周一至周五 09:00-17:00） ===
  项目启动: 2026-01-05T09:00:00+08:00
  A: 最早开始 2026-01-05T09:00:00+08:00   最迟结束 2026-01-09T16:57:00+08:00
  ...
```

A 的最迟结束 2397 = 5×480 − 3：沿 `A.end < B.start < C.start < 第5天` 的严格序链，
每环扣 1 个 epsilon，由求解器自动传播得出。

### 测试结果

```
ℹ tests 38
ℹ pass 38
ℹ fail 0
```

覆盖：复合表正确性（含逆关系对称性）、传递闭包传播、矛盾检测、STP 窗口、
验收场景全窗口数值、关键路径识别、时区/周末日历投影。

## API 速览

```ts
import { SchedulingSolver, Calendar } from './src/index.ts';

const solver = new SchedulingSolver({ epsilon: 1 });
const A = solver.addTask('A', 2 * 480);            // 固定工期
const B = solver.addTask('B', { min: 400, max: 500 }); // 工期区间
solver.constrain(A, B, 'before');                  // 或 '{b,m}'、['o'] 等析取标签
solver.require(B, 'start', '<', 5 * 480);          // 点约束：第 5 天前开始

const result = solver.solve();
result.consistent;            // 约束网络是否可满足
result.windows;               // 各任务 ES/LS/EF/LF/时差/是否关键
result.criticalTasks;         // 关键路径上的任务
result.criticalWindow;        // 项目全局关键路径窗口
result.relationBetween(A, B); // 传播后两任务间仍允许的基本关系

const cal = new Calendar({
  timeZoneOffsetMinutes: 480, workDays: [1, 2, 3, 4, 5],
  dayStartMinutes: 540, dayEndMinutes: 1020,
});
cal.format(cal.toWallClock(result.windows[0].latestFinish, projectStart));
```

## 结构

- `src/allen.ts` — 13 个基本关系、逆关系、复合表（端点序枚举生成）、端点约束翻译
- `src/network.ts` — 定性约束网络 + PC-2 路径一致性（传递闭包）
- `src/stp.ts` — 简单时态问题求解器（Floyd-Warshall 最紧界 / 负环检测）
- `src/solver.ts` — 定性+定量整合：时间窗、时差、关键路径
- `src/calendar.ts` — 时区感知的工作日历投影
- `examples/project-schedule.ts` — 验收场景样例
- `test/` — Node 内置测试运行器（`node:test`）测试
