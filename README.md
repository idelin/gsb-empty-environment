# billing — 订阅月中升级结算库

Go 实现的订阅结算库：支持月中升级、升级追溯覆盖本月用量、已付款不重复扣除，
并在所有合法变更路径中求**最终最低应付金额**及对应的**合法变更路径**。

## 结算模型

- 所有金额一律使用**整数分**（`int64`），杜绝浮点误差。
- 每个套餐定义：月费（分）、包含量、超量单价（分/单位）。
- 月中升级后，新套餐的包含量**追溯覆盖整个账期用量**：
  `应付 = 最终套餐月费 + max(0, 用量 - 包含量) × 超量单价`。
- 升级只补差价（新套餐月费 − 已付月费），月中已结的超量费等已付款项
  通过 `Ledger` 登记后只抵扣、**绝不重复扣除**；已付超出应付的部分记为应退额度。
- 月末对账恒等式（`Settlement.Verify` 强制校验）：
  `已付 + 追补 − 应退 == 应付`。
- **不允许降级绕过限制**：月费降低的升级边在 `NewCatalog` 阶段即被拒绝；
  手工构造的非法/降级路径也会被 `SettlePath` 拒绝。

## 用法

```go
c, _ := billing.NewCatalog(
    []billing.Plan{
        {ID: "basic", MonthlyFeeCents: 1000, IncludedUnits: 100, OverageCentsPerUnit: 20},
        {ID: "pro",   MonthlyFeeCents: 3000, IncludedUnits: 500, OverageCentsPerUnit: 10},
        {ID: "max",   MonthlyFeeCents: 8000, IncludedUnits: 2000, OverageCentsPerUnit: 5},
    },
    []billing.Upgrade{
        {From: "basic", To: "pro"},
        {From: "pro", To: "max"},
    },
)

// 本月用量 800，从 basic 起步，求最低应付与合法路径：
s, _ := c.MinSettlement("basic", 800, nil)
// s.Path          = ["basic", "pro"]   （升级到 pro 最优）
// s.TotalDueCents = 6000               （3000 月费 + 300 超量 × 10 分）
// s.PaidCents     = 3000               （1000 月初 + 2000 升级差价）
// s.BackPayCents  = 3000               （月末追补，已付不重复扣）
// s.Verify()      = nil

// 月中已按旧套餐结过超量费？登记到 prior，结算时自动抵扣/退款：
prior := &billing.Ledger{}
prior.Charge(billing.PaymentMidMonthOverage, "basic", 8000, "mid-month overage bill")
s2, _ := c.MinSettlement("basic", 800, prior)
// s2.CreditCents = 5000 （已付 11000 超出应付 6000，应退而非再扣）
```

## API 一览

- `NewCatalog(plans, upgrades)`：构建目录，校验重复/未知/自环/降级边。
- `Plan.Due(usage)` / `Plan.OverageUnits(usage)`：单套餐全月应付与超量。
- `Catalog.MinSettlement(start, usage, prior)`：BFS 枚举可达套餐，
  选应付最低者（平局取跳数少、字典序小，结果可复现），返回完整结算。
- `Catalog.SettlePath(path, usage, prior)`：校验指定路径合法性并结算。
- `Settlement.Verify()`：整数分校验 `已付 + 追补 − 应退 == 应付`。

## 测试

```sh
go test ./... -v
```

覆盖：升级追溯、已付款抵扣与退款、不变更最优、链式/分叉升级图、
降级绕过拒绝（目录层与路径层）、全量用量扫描下的对账恒等式与最优性。
