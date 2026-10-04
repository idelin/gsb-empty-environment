# 评委分配器（judge-assignment-solver）

为每份作品安排 **3 位评委** 的精确求解器（TypeScript，无第三方依赖）。

## 问题模型

**约束**

- 每份作品恰好分配 `teamSize`（默认 3）位评委；
- 专业匹配：评委的专业方向须覆盖作品所需专业；
- 回避关系：指定评委不得评审指定作品；
- **评委对联合限制**：任意一对评委共同评审的作品数 ≤ `maxPairShared`（默认 2）。
  该限制是全局联合约束，在搜索与无解见证中都被严格建模，不可忽略。

**目标**

- 最小化工作量最大差：`max(load) - min(load)`，`load` 为评委评审的作品数。

**规模**

- 作品数 ≤ `maxWorks`（默认 12），超出返回 `error`。
  在此规模下求解器做完备搜索，保证最优性与无解结论的正确性。

## 用法

```ts
import { solve } from './src/index.ts';

const result = solve(judges, works, avoidances);
// judges:     { id, specialties: string[] }[]
// works:      { id, specialty: string }[]        （≤ 12 份）
// avoidances: { judgeId, workId }[]
```

返回 `SolveResult`：

- `{ status: 'optimal', assignment, loads, spread, minLoad, maxLoad, stats }`
- `{ status: 'infeasible', witness, stats }` —— 见证 `witness` 有三种：
  - `avoidance`：某作品可用评委不足 3 人（回避见证）；
  - `capacity`：作品子集 `S` 的可用评委池 `T` 满足
    `C(3,2)·|S| > maxPairShared·C(|T|,2)`，即评委对名额需求超过联合限制下的
    总容量（容量见证，枚举全部 2^|S| 子集取缺口最大者）；
  - `exhausted`：以上线性证书均不适用时，由完备穷举全部负载界组合证明无解。
- `{ status: 'error', message }` —— 规模超限、参数非法或搜索预算耗尽。

辅助函数：`validateAssignment`（校验分配是否满足全部约束）、`computeLoads`。

## 求解方法

按最大差 `D = 0, 1, 2, …` 递增枚举负载界 `[lo, lo+D]`（`lo` 覆盖所有可能取值），
对每个界做完备 DFS：MRV 选作品、低负载优先排序，配合负载容量、下界缺口、
评委对名额、下界可达性等健全剪枝。第一个可行解即最优解；
所有界都不可行 ⇔ 实例无解（评委负载上界由联合限制推出：
`load ≤ maxPairShared·(n-1)/(teamSize-1)`，故枚举是有限的）。

可选参数：`teamSize`、`maxPairShared`、`maxWorks`、`nodeBudget`、`timeLimitMs`。

## 运行

要求 Node.js ≥ 22.18（原生运行 TypeScript）；或用 tsc ≥ 5.7 编译到 `dist/`。

```bash
npm test        # node --test "test/*.test.ts"
npm run demo    # 四个场景：正常求解 / 回避见证 / 容量见证 / 规模超限
npm run typecheck
```
