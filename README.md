# fxacct — 换汇核算库

按有理汇率逐步换算账单，只在指定的舍入节点量化到分。所有中间值
保持精确有理数（`math/big.Rat`），绝不提前舍入；每个值都可回溯
到来源账单行与运算步骤。

## 设计

- **精确计算**：金额与汇率均为有理数，`Convert` 节点只做乘法，
  不产生任何精度损失。
- **指定节点舍入**：`Round{MinorUnit: 2}` 是唯一允许损失精度的
  节点（分到 1e-2），支持 `RoundHalfAwayFromZero` 与
  `RoundHalfEven` 两种平局规则。
- **来源追踪**：`Engine.Compare` 返回逐步 `Event`（输入、输出、
  舍入损益），每步都能用分数复核，如 `10773/250000 (0.043092)`。
- **逐笔 vs 汇总**：同一账单分别按「逐笔换算后求和」与「精确
  求和后一次换算」计算；由于换算是线性的，两者差额完全来自
  舍入，`StepPLs` 给出每个舍入节点两侧的损益，恒等式
  `差额 = 逐笔损益之和 − 汇总损益` 可核对。
- **反向更正**：`LineItem.Reverse(id)` 生成与原行精确抵消的更
  正行（金额取负、`ReversalOf` 指回原行），两种口径下都归零。

## 用法

```go
engine := fxacct.Engine{Steps: []fxacct.Step{
    fxacct.Convert{From: "EUR", To: "USD", Rate: fxacct.MustRat("10773/10000")},
    fxacct.Round{MinorUnit: 2, Mode: fxacct.RoundHalfAwayFromZero},
}}
cmp, err := engine.Compare(items)
fmt.Print(cmp.Report()) // 可复核的分数中间值 + 每步舍入损益
```

## 运行

```sh
go test ./...
go run ./cmd/fxdemo
```
