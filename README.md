# taxcalc

题内税额计算与反算库（Go）。税阶与抵扣规则完全由输入给出，金额为整数分，
不涉及任何真实税法。

## 计算模型

- 税表 `Schedule` 由若干**连续税阶**组成，从 0 开始无缝衔接，最后一阶
  上界必须为 `taxcalc.Inf`（无界）。
- 每个税阶有精确有理数税率 `Rate{Num, Den}` 和固定抵扣额 `Ded`（分）。
- 设 gross 落在第 i 阶，`d = gross - Lower_i`，则

  ```
  tax(gross) = Σ_{j<i} (round(width_j * rate_j) - ded_j)
             + round(d * rate_i) - ded_i
  net(gross) = gross - tax(gross)
  ```

- **舍入发生在每个税阶末尾**：每一阶的税额子项独立舍入到整数分
  （`RoundingMode`：HalfUp / HalfEven / Down / Up），税阶之间不共享分位小数。
- 抵扣额在进入该阶时全额生效，因此 `net` 在税阶边界可能**跳变**；
  税率 > 1 时 `net` 可**下降**，税率 = 1 时 `net` 为**平台**。
  调用方不得假设税后金额严格递增。

## 反算

`Schedule.Invert(targetNet)` 返回全部满足 `net(gross) == targetNet` 的
税前区间（升序、互不相交、闭区间 `Interval{Lo, Hi}`）。空切片表示该税后
金额不可达。

实现要点：每个税阶内 `net = 常数 + d - round(d*rate)`，而
`g(d) = round(d*rate) - d` 关于 d 单调（rate ≥ 1 不降，rate < 1 不增），
故每阶的解是单个连续整数区间，用二分查找求得，再跨阶合并相邻区间。
搜索在 `big.Int` 上进行，且按 `d <= den*(|k|+1)/|num-den|` 收紧窗口，
大税率或无界顶阶都不会溢出或退化。

## 示例

```go
s, _ := taxcalc.New([]taxcalc.Bracket{
    {Lower: 0, Upper: 100, Rate: taxcalc.Rate{Num: 1, Den: 10}},
    {Lower: 100, Upper: taxcalc.Inf, Rate: taxcalc.Rate{Num: 1, Den: 5}, Ded: 5},
}, taxcalc.RoundHalfUp)

s.Tax(250)          // 35
s.Net(250)          // 215
s.Invert(215)       // []taxcalc.Interval{{Lo: 250, Hi: 250}}
```

## 测试

```
go test ./...
```

覆盖：手算正算用例、跳点与不可达金额、零税段、100% 平台段、
税率 > 1 导致的多区间反解、四种舍入模式差异、参数校验，
以及随机税表对暴力枚举的完备性/正确性对照。
