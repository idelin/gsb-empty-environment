# 七天员工排班器（TypeScript）

零依赖的约束优化排班器：在满足硬约束的前提下最小化带权偏好损失；
无法排班时，输出导致无解的**最小班次与规则集合**。

## 运行要求

- Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译、无第三方依赖）
- 如装有 TypeScript，可执行 `tsc --noEmit` 做类型检查（非必需）

```bash
node src/demo.ts              # 运行示例（一个可行场景 + 一个不可解场景）
node --test test/scheduler.test.ts   # 运行测试
```

## 约束模型

**硬约束（必须满足）**

- 技能：员工必须具备班次要求的技能；
- 人数：每个班次排满 `required` 人，同一员工在同一班次只占一个名额；
- 每日工时：员工每天（按班次开始日计）总工时 ≤ `maxDailyMinutes`；
- 跨日连续休息：同一员工相邻两个班次的「结束 → 开始」间隔 ≥ `minRestMinutes`，
  夜班按真实起止分钟展开为周内绝对时间（如 22:00–06:00 = 480 分钟），跨日、跨周界均正确；
- 不可用时间：班次不得与员工声明的不可用窗口重叠。

**软约束（带权偏好，最小化总损失）**

- `prefer`：未把员工分配到目标班次 → 损失 `weight`；
- `avoid`：把员工分配到了目标班次 → 损失 `weight`；
- 目标可以是单个班次（`shiftId`），也可以是某天（可叠加时间窗）的全部班次。

员工上限 12 人（`MAX_EMPLOYEES`），排班周期 7 天。

## API

```ts
import { schedule } from './src/index.ts';

const result = schedule({
  employees: [{ id: '王芳', skills: ['cashier'], maxDailyMinutes: 480 }],
  shifts: [{ id: 'd0-am', day: 0, startMinute: 480, endMinute: 960, skill: 'cashier' }],
  minRestMinutes: 660,
  preferences: [{ employee: '王芳', type: 'prefer', weight: 5, shiftId: 'd0-am' }],
});
// result.status: 'optimal' | 'feasible' | 'infeasible' | 'unknown'
```

- `optimal`：已证明偏好损失最小；`feasible`：找到解但搜索预算耗尽、未证明最优；
- `infeasible`：返回 `explanation`（最小不可解集合）；`unknown`：预算内无法判定。

可通过 `{ nodeBudget, timeBudgetMs }` 调整搜索预算。

## 求解方法

- 约束传播 + 分支限界：按「剩余候选缺口最小」动态选班次（MRV），
  同班次名额按组合枚举（破对称），前向检查每个班次的候选人数是否仍够；
- 目标下界 = 已发生的 avoid 损失 + 已确定无法满足的 prefer 损失，用于剪枝；
- 不可解解释使用删除法（deletion filter）：逐个尝试移除班次/规则，
  仍不可解则永久移除，最终得到**不可约**集合——再移除任意一项即恢复可行。
  该集合是 minimal（不可约），不保证全局基数最小。

## 不可解解释中的「规则」

每条规则都是可独立放松的原子约束：

- 班次的技能要求（放松后该班次任意员工可上）；
- 全局最小连续休息分钟数；
- 某员工的每日工时上限；
- 某员工的某个不可用窗口。

## 已知限制

- 公平性（如每人班次数量均衡）不是内建约束，可通过偏好权重近似表达；
- 大规模实例（接近 12 人 × 高密度班次）可能需要调大搜索预算；
- 最小不可解集合为不可约集合，而非全局最小基数集合。
