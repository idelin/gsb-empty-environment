# permtest

Go 精确置换检验库，全部使用 `math/big` 精确有理数运算，无浮点误差。

## 两组均值差的精确置换检验

`TwoSampleExact(x, y []float64)` 枚举混合样本全部 `C(nx+ny, nx)` 种分组
（每组至多 12 个样本），按**样本位置**选组——重复值视为不同个体。
双侧尾部定义为 `|均值差| >= |观察值|`。

返回 `TwoSampleResult`：

- `ObservedDiff`：观察均值差（精确 `*big.Rat`）
- `Tail` / `Total`：尾部组合数 / 总组合数（分子、分母）
- `PValue`：已约分的精确 p 值；`NumDen()` 返回约分后的分子分母

## 分层成组随机化

`StratifiedExact(strata []Stratum)` 只枚举合法随机分配：

- 每个 `Block` 是随机化单元，块内样本**整体换组**；
- 每层保持观察到的处理块数不变（层内组合、层间笛卡尔积）；
- 统计量为各层 `Weight * (处理组均值 - 对照组均值)` 的有理权重和；
- 重复值按位置区分，全部精确有理数比较。

返回 `StratifiedResult`：`ObservedStat`、`Tail`/`Total`、约分后的
`PValue`，以及分层计数证据 `Evidence`（每层块数、处理/对照块数、样本数、
组合数 `C(块数, 处理块数)`，各层组合数之积等于 `Total`）。

## 测试

```
GOCACHE=/private/tmp/gocache go test ./...
```

测试包含手算用例（重复值、负数、两组大小不等、多块整体换组、不等处理
块数）以及与独立 float64 暴力枚举参考实现的随机交叉核对，外加
12 vs 12 上限（`C(24,12) = 2704156`）全枚举验证。
