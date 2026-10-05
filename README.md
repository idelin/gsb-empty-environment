# 充电调度器（charging-scheduler）

在整数时间格与整数能量单位下，为到离站车辆求解**按时满足需求的最低费用充电计划**。
考虑因素：车辆到离站时间窗、需求电量、单时隙最大功率、站点分时功率上限、分时电价，
以及可选的**充电启动次数限制**；无解时输出**时间窗能量缺口**报告。

## 问题定义

- 时间离散为时隙 `0..T-1`，时隙 `t` 表示区间 `[t, t+1)`。
- 每个时隙有站点功率上限 `powerLimit`（该时隙全场可充能量上限）与单位电价 `price`（可为负）。
- 每辆车给出充电窗口 `[arrival, departure)`（`departure` 时隙不可再充）、需求电量 `demand`、
  单时隙最大功率 `maxPower`，以及可选的最大启动次数 `maxStarts`
  （“启动”指从“不充”变为“充”的一次跳变，即连续充电段的段数）。
- 目标：所有车在离站前充满足额能量，总费用 `Σ 时隙充电量 × 时隙电价` 最小。

## 使用方法

```ts
import { scheduleCharging } from './src/index';

const result = scheduleCharging({
  slots: [
    { powerLimit: 2, price: 1 },
    { powerLimit: 2, price: 3 },
    { powerLimit: 2, price: 1 },
  ],
  vehicles: [
    { id: 'car-1', arrival: 0, departure: 3, demand: 2, maxPower: 1, maxStarts: 1 },
  ],
});

if (result.feasible) {
  console.log(result.totalCost, result.plan);
} else {
  console.log(result.totalDeficit, result.vehicleDeficits, result.windowDeficits);
}
```

运行测试（需要 [Bun](https://bun.sh)，或用任意 TS 工具链编译后运行）：

```sh
bun test            # 运行全部测试
npm run typecheck   # 若安装了 typescript，则进行 tsc 严格类型检查
```

## 输出结构

**可行时**（`feasible: true`）：`totalCost`、`totalEnergy`，以及每辆车的 `plan`
（逐时隙非零充电量、费用、启动次数）。

**不可行时**（`feasible: false`）：

- `totalDeficit`：总体能量缺口下界 = `max(联合最大流缺口, 各车独立缺口之和)`。
- `vehicleDeficits[]`：每辆车**独占站点**时（计窗口、最大功率、启动次数限制）的
  最大可充能量 `deliverable` 与缺口 `deficit`。
- `windowDeficits[]`：对每个离站截止时间 `d`，统计 `departure ≤ d` 的车辆需求之和
  与时隙 `[0, d-1]` 内最大可交付能量（前缀最大流精确值）之差，即时间窗能量缺口。

## 算法说明

1. **最低费用流（无启动限制时精确最优）**
   建模为二分图最小费用最大流：`源 → 车辆（容量=需求）→ 时隙（容量=min(最大功率,需求)，费用=电价）→ 汇（容量=功率上限）`。
   采用 SSP + SPFA 增广；容量全为整数，故流量与费用均为整数；支持负电价。
2. **启动次数限制**
   启动次数是固定费用型约束，一般多车情形下无多项式精确算法。这里采用坐标下降修复：
   对每辆超限车辆，将其从站点占用中移除后，用**单车辆精确 DP**
   （状态：已充能量 × 已用启动次数 × 上一时隙是否在充）在剩余容量上重排。
   单车辆场景即为整体精确最优；多车场景输出保证可行（若收敛），费用为近似最优。
3. **无解分析**
   联合最大流给出总体可交付上界；逐车独立 DP/容量求和给出单车缺口；
   按截止时间的前缀最大流给出各时间窗缺口。

## 代码结构

- `src/types.ts` — 问题与结果的类型定义
- `src/validate.ts` — 输入校验（整数性、窗口范围、唯一 id 等）
- `src/minCostFlow.ts` — 整数最小费用最大流（SSP/SPFA）
- `src/flowScheduler.ts` — 流网络构建与计划提取
- `src/vehicleDp.ts` — 单车辆启动次数受限精确 DP
- `src/scheduler.ts` — 主入口、启动限制修复循环、缺口报告
- `test/` — 单元测试 + 小规模暴力枚举随机对拍
