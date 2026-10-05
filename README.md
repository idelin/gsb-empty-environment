# 七天员工排班器（weekly-scheduler）

TypeScript 实现的精确排班器：在满足硬约束的前提下，最小化带权偏好损失。
支持最多 12 名员工、7 天班次、跨午夜夜班（按真实起止分钟计算）、跨日连续休息约束；
无法排班时输出导致无解的**最小班次与规则集合**（删除极小核心）。

## 快速开始

```bash
npm run build                              # 需要任意 tsc（>= 4.5）
node dist/src/cli.js examples/feasible.json     # 可行示例：输出最优排班
node dist/src/cli.js examples/infeasible.json   # 无解示例：输出最小无解核心
npm test                                   # 编译并运行测试
```

也可以作为库使用：

```ts
import { schedule, verify } from './src/scheduler';
const result = schedule(problem);   // ScheduleResult
```

## 输入模型（JSON）

```jsonc
{
  "minRestMinutes": 660,          // 相邻班次的最小连续休息分钟数（跨日按真实时间轴计算）
  "employees": [                  // 1–12 人
    {
      "id": "张三",
      "skills": ["收银"],
      "maxDailyMinutes": 480,     // 每日最大工时（分钟）
      "unavailable": [{ "day": 2, "start": 0, "end": 1439 }],   // 硬约束：不可用时段
      "preferences": [            // 带权偏好：匹配则产生 weight 的损失
        { "day": 6, "weight": 3 },
        { "shiftId": "夜班-安保-周一", "weight": 8 },
        { "start": 1200, "end": 360, "weight": 5 }   // 跨午夜时间窗：20:00–06:00
      ]
    }
  ],
  "shifts": [
    {
      "id": "夜班-安保-周一",
      "day": 0,                   // 0=周一 … 6=周日
      "start": 1320,              // 当天零点起的分钟数
      "end": 360,                 // end <= start 表示跨午夜（夜班），按真实分钟计算时长
      "skill": "安保",
      "required": 1               // 需求人数，默认 1
    }
  ]
}
```

## 约束与目标

**硬约束**
- 覆盖：每个班次的每个名额恰好分配一名员工；
- 技能：员工必须具备班次要求的技能；
- 每日工时：员工当天（按班次开始日计）总工时 ≤ `maxDailyMinutes`；
- 跨日连续休息：同一员工任意两个班次的间隔 ≥ `minRestMinutes`（在 0–7 天的绝对时间轴上计算，夜班跨午夜自然延伸）；
- 时间重叠：同一员工的班次不得重叠（物理约束，不可放宽）；
- 不可用时段：班次不得与员工的 `unavailable` 重叠。

**目标**：最小化总偏好损失 = Σ（被分配到匹配偏好班次的 weight）。

## 无解时的最小核心

当约束不可满足时，`schedule()` 返回 `status: "infeasible"` 及 `core` 字段：

- `core.shifts`：导致无解的最小班次集合——移除其中任意一个，问题恢复可解；
- `core.rules`：涉及的规则集合（`skill` / `dailyHours` / `rest` / `unavailability`）——放宽其中任意一条，问题恢复可解；
- `core.employeeIds`：具备核心班次所需技能的员工。

提取算法为删除式最小化（deletion-based MUS）：逐个尝试移除班次 / 禁用规则，
仍不可解则永久移除，最终得到删除极小的核心。

## 求解算法

精确分支定界（`src/solver.ts`）：
- 班次按 `required` 展开为槽位，同一班次的槽位按员工序号递增分配以消除排列对称；
- 动态 MRV（最少可行候选）选点 + 候选按增量损失排序；
- 下界剪枝：当前损失 + 每个未分配槽位的最小可能损失；
- 默认节点预算 200 万，超预算返回当前最优可行解并标记 `optimal: false`。

## 项目结构

```
src/types.ts      输入模型与类型
src/time.ts       绝对时间轴工具（夜班跨午夜换算）
src/solver.ts     分支定界求解器
src/core.ts       最小无解核心提取
src/scheduler.ts  对外 API：校验、排班、独立结果校验器 verify()
src/cli.ts        命令行入口
test/run-tests.ts 测试（含跨日休息、夜班时长、无解核心等用例）
examples/         可行 / 无解两个示例配置
```
