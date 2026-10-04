# 维修规划器（maintenance-planner）

预算约束下的设备维修/拆解最优规划：备件齐套才能修复设备；也可以拆解旧设备回收备件，
但拆解会损失残值、回收有损耗，且不能循环产料。输出总价值最高的方案及
“来源 -> 用途”的备件流水。设备规模上限 18 台，精确求解。

## 模型

每台设备三选一（互斥）：

- **修复**：需备件齐套（库存 + 拆解回收 + 外购），花费 `repairCost`，获得 `repairedValue`；
- **拆解**：损失全部 `residualValue`，按 `floor(contains × recoveryRate)` 回收备件（有损耗），
  可选 `dismantleCost` 占用预算；
- **闲置**：保留残值 `residualValue`。

目标：最大化 `Σ修复设备 repairedValue + Σ闲置设备 residualValue`（被拆解设备残值归零）。
约束：`Σ修复费 + Σ拆解费 + Σ外购备件费 ≤ budget`。

**不能循环产料**：修复与拆解互斥，回收备件只流向被修复设备，被修复设备不会再被拆解，
因此备件流向是“来源（库存/采购/拆解机）→ 用途（修复机）”的有向无环图，
求解结果会再做拓扑校验。

## 输入格式（JSON）

```json
{
  "budget": 120,
  "recoveryRate": 0.8,
  "parts": { "电机": { "stock": 1, "price": 15 }, "电池": { "stock": 2 } },
  "devices": [
    {
      "id": "D01", "repairCost": 18, "repairedValue": 90, "residualValue": 12,
      "needs": { "电机": 1 }, "contains": { "电池": 1 },
      "dismantleCost": 0, "recoveryRate": 0.8
    }
  ]
}
```

- `parts.<名>.price` 缺省表示该备件不可外购，只能靠库存与拆解回收；
- `recoveryRate` 全局默认 1，设备级可覆盖；金额与数量均为非负整数；
- `devices` 最多 18 台。

## 运行

需要 Node ≥ 22.6（利用原生 TypeScript 类型擦除运行，无需编译）：

```bash
node src/cli.ts examples/case.json          # 人类可读报告
node src/cli.ts examples/case.json --json   # 机器可读结果（含 flows）
npm test                                    # 单元测试 + 与暴力枚举对拍
```

## 算法

精确分支限界 DFS（每台设备 3 个分支），关键点：

- 设备按修复净收益降序排列，后缀净收益和作上界剪枝；
- 搜索中备件余额允许为负（欠账），叶节点统一结算缺口外购——
  记账与决策顺序无关，保证不错过“先修复、后拆解供件”的全局方案；
- 用“剩余设备全拆解”的后缀回收量估计欠账可行性/外购花费下界进行剪枝；
- 每层按 `(剩余预算, 备件向量)` 记忆化去重。

正确性由 `test/solver.test.ts` 中 60 组随机小规模案例与 3^n 暴力枚举对拍保证。

## 输出

- 决策：每台设备 修复 / 拆解 / 闲置；
- 外购清单、预算使用、恢复总价值；
- 备件流水：`库存/采购/拆解机 --备件×数量--> 修复机`，以及回收剩余（报废）。
