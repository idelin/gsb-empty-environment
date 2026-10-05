# fxsettle — 换汇核算库

按**有理汇率**逐步换算账单，全程保持精确分数（`math/big.Rat`），
**只在显式声明的舍入节点**才舍入到分，绝不提前舍入。
支持来源追踪、逐笔 vs 汇总口径对比、反向更正，并可输出可复核的
分数中间值与每步舍入损益。

## 核心概念

- **Step / 流水线**：`FX(label, rate)` 为不舍入的换算步；
  `FXRound(label, rate, scale, mode)` 为舍入节点（先精确乘汇率，
  再按 `RoundHalfUp` / `RoundHalfEven` / `RoundDown` / `RoundUp`
  舍入到 `scale` 位小数）。
- **Trace**：`Convert(entry, toCur, steps)` 返回换算轨迹，每个
  `Node` 记录 `In / Rate / Exact / Rounded / RoundPL / Out`，
  中间值全部是精确分数。
- **来源链**：每个 `Entry` 携带 `Prov []ProvRec`，记录产生该值的
  操作（input/convert/round/sum/correct）、输入条目与精确值。
- **Compare**：`Compare(entries, toCur, steps)` 同时跑两种口径——
  逐笔（每笔在各舍入节点分别舍入）与汇总（各节点仅对合计舍入一次），
  输出每个舍入节点及最终的差额；`CorrectionEntry` 生成使两种口径
  对齐的更正分录。
- **Reverse**：`Reverse(trace)` 沿轨迹倒推——换算步乘汇率倒数，
  舍入节点生成显式更正步加回 `−RoundPL`，可**精确还原**原始金额；
  `CorrectionEntries` 把更正步落成可入账分录。
- **报告**：`Trace.Report()` / `Comparison.Report()` /
  `ReverseTrace.Report()` / `ProvReport(entry)` 输出分数中间值、
  十进制近似与每步舍入损益，供审计复核。

## 示例

```go
rate := fxsettle.Must(fxsettle.ParseRat("7.1234"))
steps := []fxsettle.Step{
    fxsettle.FX("USD->CNY 中间换算", rate),
    fxsettle.FXRound("CNY 入账舍入", fxsettle.Rat(1, 1), 2, fxsettle.RoundHalfUp),
}
entry := fxsettle.NewEntry("bill-1", "USD", fxsettle.Must(fxsettle.ParseRat("0.007")), "零钞")
out, tr := fxsettle.Convert(entry, "CNY", steps)
fmt.Print(tr.Report())      // 精确中间值 249319/5000000，舍入损益 681/5000000
rt := fxsettle.Reverse(tr)  // rt.Recovered == 7/1000，精确还原
```

完整可运行示例见 `example_test.go`。

## 测试

```sh
GOCACHE=/tmp/gocache go test ./...
```
