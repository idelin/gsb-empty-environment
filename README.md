# recipex

生产配方替代求解库（Go）。

## 模型

- 每个需求（订单行）有整数数量 `Quantity`，可由若干替代方案（`Option`）之一整体满足。
- 每个替代方案按整数比例 `Ratio` 消耗主物料（每单位需求消耗 `Ratio` 单位），
  并可额外按 `AuxRatio` 消耗辅助物料（每单位主物料消耗 `AuxRatio` 单位）。
- 库存有限，主物料与辅助物料共享同一库存表，因此多个需求会被同一辅助物料耦合。

## 目标

1. 最大化完整订单数（需求要么整体满足，要么放弃）；
2. 在订单数最大的方案中，最小化总成本。

## 用法

```go
res, err := recipex.Solve(demands, inventory)
```

- 最多 10 个需求（`MaxDemands`），超出返回错误。
- `res.Evidence` 输出每种物料的消耗量与剩余量（消耗证据）。
- `res.Assignments` 记录每个被满足需求选用的替代方案及实际消耗。

## 验证

`BruteForceSolve` 对全部「需求 × 替代」指派做全枚举，与 `Solve`
的分支限界搜索独立实现；测试（`TestCrossCheckBruteForce`）在数百个小规模
随机实例上交叉验证两者结果一致，`TestAuxCoupling` 专门验证辅助物料耦合：
主物料充足但共享辅助物料只够一单时，订单数与成本均正确。
