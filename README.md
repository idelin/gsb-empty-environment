# 充电调度器（EV Charging Scheduler）

在整数时间格上，为一批电动车制定**按时满足需求、费用最低**的充电计划。
考虑车辆的到离站时间窗、需求电量、每格最大功率、充电启动次数限制，
以及站点的分时功率上限与分时电价。所有时间与能量均为整数单位，计算全程整数精确。

## 问题模型

- 时间离散为格 `t = 0..T-1`；每格能量单位整数。
- 车辆 `i`：充电窗口 `[arrivalSlot, departureSlot)`、需求 `demandEnergy`、
  每格最大充电量 `maxPowerPerSlot`（= 最大功率 × 格长）、启动次数上限 `maxStarts`（可选）。
- 站点：每格功率上限 `powerLimitPerSlot[t]`、电价 `pricePerSlot[t]`（费用/能量单位）。
- 目标：满足全部需求的前提下最小化 `Σ 电价[t] × 充电量[t]`。
- “启动次数”= 连续充电段（会话）数；车辆充电的极大连续时间格段数不得超过 `maxStarts`。

## 使用方法

```ts
import { scheduleCharging } from "./src/index.ts";

const plan = scheduleCharging(
  [
    { id: "EV-1", arrivalSlot: 0, departureSlot: 8, demandEnergy: 40,
      maxPowerPerSlot: 15, maxStarts: 2 },
  ],
  {
    slotCount: 8,
    powerLimitPerSlot: [20, 20, 20, 20, 10, 10, 20, 20],
    pricePerSlot:      [ 3,  3,  1,  1,  5,  5,  2,  2],
  },
);
```

运行（Node ≥ 22.6 可直接执行 TypeScript，推荐 ≥ 23）：

```bash
npm test    # node --test test/*.test.ts
npm run demo
```

如需 `tsc` 类型检查：`npm i -D typescript @types/node && npx tsc --noEmit`。

## 输出

`ChargePlan`：

- `feasible` / `totalCost` / `totalDemand` / `totalDelivered`
- `schedules[]`：每车的分格充电量、连续会话（`sessions`）、交付电量与费用
- `unmetDemand[]`：未满足的车辆及缺口电量
- `windowDeficits[]`：**无解时的时间窗能量缺口**，三类成因：
  - `station-capacity`：区间 `[s,e)` 内窗口完全落入的车辆需求之和超过站点区间容量
    （Horn 区间条件，只报告缺口极大的最窄区间，精确定位瓶颈时段）；
  - `vehicle-power`：车辆自身 `最大功率 × 窗口长度` 不足以覆盖需求；
  - `start-limit`：启动次数限制导致无法安置的能量。

## 算法

1. **最小费用最大流（精确）**：源 → 车辆（容量=需求）→ 窗口内时间格
   （容量=每格最大功率）→ 汇（容量=站点功率上限，费用=电价）。
   SSP + Johnson 势的 Dijkstra，全整数运算。流量不足总需求即无解（此时输出缺口诊断）。
2. **启动次数修复（启发式）**：启动次数受限属于 NP 难问题。对超限车辆反复消除
   能量最小的会话，把能量按费用增量最小迁入不会新增会话的时间格
   （现有会话内补足、紧邻外延、填隙合并）；无法安置的能量计为 `start-limit` 缺口。
   不带 `maxStarts` 时结果即为全局最优（有暴力枚举对照测试验证）。
3. **缺口诊断**：`O(T²)` 区间网格（2D 前/后缀和 + 支配过滤），`T > 4096` 时
   自动跳过区间诊断，仅保留逐车缺口。

复杂度：流网络 `O(V·T)` 条边，SSP 每次增广 `O(E log N)`；诊断 `O(T² + V)`。

## 文件结构

- `src/types.ts` — 输入/输出类型
- `src/minCostFlow.ts` — 整数最小费用最大流
- `src/scheduler.ts` — 建网求解、启动次数修复、缺口诊断
- `test/scheduler.test.ts` — 功能与自洽性测试
- `test/optimality.test.ts` — 与暴力枚举对照的最优性测试
- `examples/demo.ts` — 示例
