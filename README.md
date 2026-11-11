# 差分隐私离散噪声校验

隐私服务使用有限离散噪声表，配置变化后可能不再满足声称的隐私界。写供Go验证宿主调用的核验包，噪声概率全为有理数，相邻数据集的真实计数差一；精确计算给定乘性因子下最小加性松弛及最坏输出集合。处理截断与边界折返，用穷举输出概率验证，不用浮点近似冒充证明。

## 用法

```go
tbl := netsec.NoiseTable{Entries: []netsec.Entry{
    {Offset: -1, Prob: big.NewRat(1, 4)},
    {Offset: 0, Prob: big.NewRat(1, 2)},
    {Offset: 1, Prob: big.NewRat(1, 4)},
}}
spec := netsec.Spec{Lo: 0, Hi: 3, Mode: netsec.Truncate} // 或 netsec.Wrap

// 精确计算乘性因子 rho=2 下的最小加性松弛 delta 及最坏输出集合
res, _ := netsec.Analyze(tbl, spec, big.NewRat(2, 1))
// res.Delta == 1/4, res.Set == [0], res.Count == 0, res.Direction == Forward

// 核验声称的 (rho, delta) 界是否在配置变化后仍然成立
ok, minDelta, _ := netsec.CheckClaim(tbl, spec, big.NewRat(2, 1), big.NewRat(1, 4))
```

## 语义

- 真实计数 c ∈ [Lo, Hi]，相邻数据集计数为 (c, c+1)，对每个相邻对、两个方向分别精确计算输出分布。
- `Truncate`：越界概率质量累积到最近边界输出；`Wrap`：按区间长度取模折返。
- 最小加性松弛为曲棍球棒散度 δ = max Σ_o max(0, p(o) − ρ·q(o))，最坏集合为 {o : p(o) > ρ·q(o)}。
- 全部计算使用 `math/big.Rat` 精确有理数；`ExhaustiveDelta` 通过枚举全部输出子集独立验证最坏集合的最优性（支持集 ≤ 25）。
