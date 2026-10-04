# 切割规划器（Cutting Stock Planner）

一维下料优化：给定多种「长度 + 单价」的原料棒和一批订单需求（总件数 ≤ 22），
计入固定锯缝，求**采购成本最低**、其次**切割次数最少**的切割方案，
并输出每根棒的切法与余料去向。

## 建模规则

- 原料棒不限量，按根计价；需求按「长度 × 件数」给出。
- 每切一刀消耗固定锯缝 `kerf`，**锯缝从可用长度中扣除，绝不可计入可复用余料**。
- 一根棒切出 k 件：若恰好用尽（余料为 0），最后一件免切，切割次数 = k − 1；
  否则切割次数 = k。
- 棒上余料只要还够放下后续订单件（含锯缝），搜索时就会继续利用。
- 最终余料去向三分类：
  - `可复用余料`：余料 ≥ 复用门槛（默认最短订单件长度，可用 `reusableMinLength` 覆盖）→ 留存备用；
  - `废料`：余料不足复用门槛；
  - `无剩余`：恰好用尽。
- 目标为字典序：先 `总采购成本` 最小，再 `总切割次数` 最少。

## 算法

需求件按长度降序做深度优先搜索，每件有两个分支：放进某根已开棒的余料
（相同剩余容量去重以消除对称），或新购一根某种规格的原料棒。
用「剩余材料长度 × 最低单位长度价格」与「最长相待放件所需最便宜的棒」
做成本下界剪枝。总件数 ≤ 22 时可快速求得精确最优解。

## 运行

需要 Node.js ≥ 22.6（直接运行 TypeScript，无需编译）：

```bash
node src/cli.ts examples/sample.json   # 求解并打印方案
node src/cuttingPlanner.test.ts        # 运行测试
```

## 输入格式

```json
{
  "kerf": 3,
  "stocks": [{ "length": 6000, "price": 50 }],
  "demands": [{ "length": 1200, "quantity": 4 }],
  "reusableMinLength": 800
}
```

- `kerf`：固定锯缝宽度（≥ 0）。
- `stocks`：原料棒规格列表，`length` 长度、`price` 单根价格。
- `demands`：订单需求列表，`length` 件长、`quantity` 件数；总件数 ≤ 22。
- `reusableMinLength`：可选，余料可复用的最小长度，默认取最短订单件长度。

## 作为库调用

```ts
import { solveCuttingProblem } from './src/cuttingPlanner.ts';

const plan = solveCuttingProblem({
  kerf: 3,
  stocks: [{ length: 6000, price: 50 }],
  demands: [{ length: 1200, quantity: 4 }],
});
// plan.bars[i]：{ stockLength, price, pieces, cuts, leftover, leftoverUse }
```
