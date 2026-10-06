# settle

Go 结算库：按事件时间把有理数用量分配到费率半开区间，跨段线性用量按时长分摊，
总金额半偶舍入到分；追溯费率变更输出差额而非重记总额。全程使用 `math/big.Rat`
精确有理运算，舍入只在最终输出发生一次。

## 模型

- `Segment{Start, End, Rate}`：半开区间 `[Start, End)` 的单价；`End` 为零值表示开放端（+inf）。
  `NewBook` 校验区间有序、不重叠、时长为正、费率非空。
- `Instant{At, Qty}`：事件时间用量，按 `Segment.Contains`（左闭右开）归属区间。
- `Linear{Start, End, Qty}`：线性用量在 `[Start, End)` 上均匀分布，每个重叠区间
  分得 `Qty * overlap/total`（精确有理比例）。零时长/负时长在 `NewLinear` 处拒绝。
- `Book.Settle(usages)`：返回 `Statement{Items, ExactTotal, Total}`；`Total` 是
  `ExactTotal` 半偶舍入到分的结果。任一用量超出费率覆盖范围则整体报错。
- `Book.Revise(seg, usages)`：追溯替换与 `[Start, End)` 精确匹配的区间费率，返回
  `Delta{Quantity, ExactAmount, Amount}`（差额 = 受影响用量 × (新价 − 旧价)），
  不重记总额。反复修订的差额累计与「最终总额 − 初始总额」精确对账。

## 舍入

`RoundHalfEven(r, decimals)` / `RoundToCents(r)`：银行家舍入（ ties-to-even ），
负数对称。`Money(r)` 把已舍入到分的有理数格式化为两位小数字符串。

## 测试

```sh
GO111MODULE=on go test ./...
```

测试以有理积分模型核对：端点归属（边界事件归右区间）、零时长拒绝与零泄漏、
跨段分摊的配分归一性（分配量之和精确等于总量）、只舍入一次（逐项舍入会差 1 分）、
追溯差额与反复修订的累计对账。
