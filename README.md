# taxcalc

题内税额计算与反算库（Go）。税阶与抵扣规则完全由输入给出，金额一律用整数分，
不涉及任何真实税法。

## 模型

税阶为从 0 开始连续的半开区间 `[Lower, Upper)`，最后一阶 `Upper = Inf` 无上限。
每个税阶对落入其中的部分按有理数税率 `Rate{Num, Den}` 计税，**舍入发生在每个
税阶末尾**（逐阶各自舍入成分）。再用当前所在税阶的抵扣额（速算扣除数风格）
从累计税额中扣除，税额下限为 0：

```
tax(x) = max(0, Σ round(rate_i · portion_i(x)) − deduction(x))
net(x) = x − tax(x)
```

由于税率与抵扣是任意输入，税后金额**不保证严格递增、甚至不保证单调**：
舍入平台、零税段、100% 税阶、抵扣跳变都会造成平坦、下降或跳点。

## API

- `Rules.Validate()`：校验税阶连续性、税率合法性等。
- `Rules.Tax(gross)` / `Rules.Net(gross)`：正向计算税额 / 税后金额。
- `Rules.InverseNet(target)`：反算。返回使税后金额等于 `target` 的**全部**
  税前区间（有序、不重叠的闭区间列表）；空结果表示该金额不可达。

反算不依赖单调性假设：对每个税阶分别求解舍入方程
`round(rate·t) = t + d`（整数线性不等式，big.Int 精确运算），
再合并相邻区间，因此跳点、零税段、负税率外的任意输入都被正确处理。

## 示例

```go
rules := taxcalc.Rules{Brackets: []taxcalc.Bracket{
    {Lower: 0, Upper: 1000, Rate: taxcalc.RatePercent(10)},
    {Lower: 1000, Upper: taxcalc.Inf, Rate: taxcalc.RatePercent(10), Deduction: 300},
}}
rules.Net(999)            // 899
rules.Net(1000)           // 1000（抵扣跳变，900..999 不可达）
rules.InverseNet(950)     // nil —— 不可达金额
rules.InverseNet(1000)    // [{1000 1000}]
```

## 测试

```
go test ./...
```

包含手工构造的跳点 / 零税段 / 平台 / 超 100% 税率用例，以及随机规则下
与暴力扫描的对拍验证。
