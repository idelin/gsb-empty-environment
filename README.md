# 物流中转规划器

给定有限班次表，为最多 10 批货物求**总费用最小**的运输方案（`planner.ts`）。

## 模型

- **绝对分钟**：所有时刻从第 1 天 00:00 起算（如第 2 天 02:30 = 1590），跨午夜无需特判。
- **固定处理时间**：每个终端有 `handlingMin`。货物到站后**不能立即转运**，必须满足
  `下一班发车时刻 >= 货物可用时刻 + 处理时间`；起点处可用时刻 = `ready`。
- **合车不超载**：同一班次上各批重量之和 `<= capacity`。
- **费用**：班次固定成本 `cost`（启用即收，合车摊薄）+ `costPerUnit × 实际载重`（可缺省）。

## 算法

1. 班次时间沿路径严格递增 ⇒ 换乘图是 DAG，对每批货 DFS 枚举全部时间可行路径。
2. 分支限界为每批货选一条路径：目标为已启用班次固定成本并集 + 各批重量附加费，
   合并时检查容量；以下界（各批必付的重量附加费后缀和）剪枝。10 批规模精确求解，实测 < 0.2s。

正确性已与指数级暴力枚举对拍（30 组随机用例全部一致）。

## 使用

```ts
import { solve, renderPlan, demo } from "./planner";

const plan = solve(demo);          // demo 可换成自定义 Problem
console.log(renderPlan(demo, plan));
```

输出包含：装卸时间轴（每次装车/卸货的绝对分钟时刻、班次、载重）与
各批衔接明细（每段的处理时间、衔接余量、发到时刻）。

直接运行示例：`npx ts-node planner.ts`，或 `tsc planner.ts && node planner.js`。

## 输入定义

```ts
interface Terminal { id: string; name: string; handlingMin: number }
interface Trip { id: string; from: string; to: string; depart: number; arrive: number;
                 capacity: number; cost: number; costPerUnit?: number }
interface Batch { id: string; origin: string; dest: string; weight: number; ready: number }
```

不可行时（某批无路径 / 合车必超载 / 批次超 10）返回 `ok: false` 及原因。
