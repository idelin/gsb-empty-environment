# gsb — 生产配方替代求解库

每个订单由最多 10 条需求（demand）组成。每条需求可由若干替代物料满足：
替代比例为整数（1 单位需求消耗 `Ratio` 单位替代物料），且使用某替代时
还会额外消耗共享的辅助物料（auxiliary）。在有限库存下，库按字典序求解：

1. **最大化完整订单数**（所有需求都足额满足才算一个完整订单）；
2. 在达到最大订单数的所有方案中**最小化总物料成本**。

## 模型

```go
type Problem struct {
    Materials []Material // 替代物料与辅助物料统一建模：Stock + UnitCost
    Demands   []Demand   // 每条需求：Quantity（每订单所需单位数）+ Options
}
type Option struct {
    Material int      // 替代物料下标
    Ratio    int64    // 整数替代比例，>= 1
    Aux      []AuxUse // 每需求单位额外消耗的辅助物料
}
```

某 option 的单位成本 = `Ratio*物料单价 + Σ 辅助量*辅助单价`。

## 用法

```go
s, err := gsb.Solve(p)      // s.CompleteOrders, s.Cost, s.Lines, s.Usage
fmt.Print(s.Report(p))      // 消耗证据：每条需求走了哪个替代、各物料 used/stock
```

`Solution.Lines` 给出逐需求的消耗证据（需求 × 替代 × 单位数），
`Solution.Usage` 给出逐物料的消耗/库存对照；`Report` 渲染成可读表格。

## 算法

- 可行性对订单数 k 单调（每次替代恰好服务 1 单位需求，k 单的分配方案
  缩减后即见证 k-1 单可行），故对 k 做二分，每层用回溯枚举各需求在
  剩余库存下的整数分配。
- 最大 k 确定后，用分支定界求最小成本：按单位成本升序尝试替代，以
  “剩余需求 × 各自最低单位成本”作为下界剪枝；同成本保留先找到的方案，
  输出确定性。
- 复杂度随 (需求, 替代) 组合数与库存量级指数增长，面向 ≤10 需求、
  中小数量的场景。

## 验证

`BruteForce` 对小数量实例全枚举所有可行消耗向量（允许超量满足，因
成本非负，最优 (订单数, 成本) 与精确 formulation 一致），用于交叉验证：

- `TestAuxiliaryCoupling`：两条需求共享仅 2 单位辅助物料——各自独立看
  都能做 3 单，耦合后只能做 2 单，且最优解必须协调辅助物料的分配；
- `TestRandomCrossValidation`：300 个随机小实例（随机库存/比例/辅助耦合）
  逐一比对 `Solve` 与 `BruteForce` 的 (订单数, 成本)；
- `checkEvidence`：校验解的消耗证据自洽（逐需求足额、逐物料不超库存、
  成本与证据一致）。

```sh
go test ./...
```
