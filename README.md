# 主体级计数的 Laplace 噪声：精确有理离散机制 + 连续 Laplace 数值接口

仅处理**合成数据**（整数计数由调用方直接给定，`demo.py` 用固定随机种子生成合成
人群）。Python 3.11，仅标准库（`fractions`、`random`、`dataclasses`、`math`、
`unittest`）。

## 运行

```bash
/opt/homebrew/bin/python3.11 -m unittest discover -s tests -t . -v
/opt/homebrew/bin/python3.11 demo.py
```

## 定义与参数

- **查询**：对满足某谓词的主体计数 `c ∈ Z`。
- **邻接与主体敏感度**：邻接数据集相差**一个主体**（增/删一个主体）。
  计数查询的主体敏感度 `Δ = 1`；实现接受显式正整数 `sensitivity`。
- **隐私定义（纯 ε-DP）**：机制 `M` 在有限邻接计数域 `C` 与有限输出域
  `X = [lo, hi]` 上满足
  `Pr[M(c)=x] / Pr[M(c')=x] ≤ e^ε`，对所有
  `1 ≤ |c−c'| ≤ Δ` 的 `c,c' ∈ C` 与所有 `x ∈ X` 成立。

### 离散机制（形式保证）— `dpcount/discrete.py`

有限输出整数域上的**截断双侧几何（离散 Laplace）**，衰减参数取精确有理数
`p ∈ (0,1)`：

```
Pr[M(c)=x] = p^|x−c| / Z(c),   Z(c) = Σ_{y∈X} p^|y−c|      (Fraction 精确计算)
```

- 概率是精确 `Fraction`，域内每个输出概率严格为正——**不存在浮点尾部把正概率
  事件变成零概率事件**的问题。
- `rational_decay_for_epsilon(ε, Δ)` 给出分母为 `2^k`、向上取整的有理 `p ≥
  exp(−ε/Δ)`（多噪声方向，隐私保守）；该辅助函数只用浮点**选参数**，不构成证明。
- **无偏整数抽样**：`sample(count, rng)` 用 `rng.getrandbits(1)` 惰性生成公平位，
  以二进区间 `[num/2^b, (num+1)/2^b)` 表示 `[0,1)` 上的均匀实数，仅在区间完全落入
  某个有理 CDF 半开单元时才停止并输出；所有比较均为精确有理比较，因此输出分布与
  定义**逐位相同**（无固定精度舍入、无拒绝重采样）。
- **确定性反 CDF**：`inverse_cdf(count, u: Fraction) -> int` 接受显式有理均匀输入，
  相同输入必然得到相同整数，可手工复算。
- **负值不重采样**：原始整数输出（即使为负）记录在 `NoisyCountRecord.raw`；
  后处理（截断到 `≥ floor`）单独记录在 `postprocessed`，绝不因负值重新抽样。
- **穷举形式验证**：`verify_dp(mech, count_lo, count_hi)` 枚举邻接计数域内
  每个满足 `1 ≤ |c−c'| ≤ Δ` 的有序对与每个输出 `x`，用精确分式计算似然比并取最大
  值 `R`（精确 `Fraction`），返回 `verified_epsilon = ln(R)`。这是有限域上的
  **穷举证明，不是抽样频率检验**。
- **截断注意事项**：有限域下 `Z(c)` 随 `c` 变化，朴素界 `p^−Δ` 在边界处可能不成立
  （例如 `X={0,1,2}, p=1/2` 时比值可达 `16/7 > 2`，见测试）。因此以 `verify_dp`
  的精确最紧比值为准；`demo.py` 用略小的预算选 `p`，使验证 ε = 0.9933 ≤ 1.0。

### 连续 Laplace（数值近似接口，保留原接口）— `dpcount/continuous.py`

教科书形式 `Z ~ Laplace(scale = Δ/ε)`，反 CDF：

```
z =  scale·ln(2u)        u < 1/2
z = −scale·ln(2(1−u))    u ≥ 1/2
```

- **明确标注为浮点数值近似**：IEEE-754 舍入可能使理论尾部不可达，不构成精确 DP
  机制；需要形式保证请使用离散机制。
- `ε ≤ 0` 拒绝；`sensitivity < 0` 拒绝；`u ∈ (0,1)` 为开区间要求，抽到
  `u = 0/1`（零概率但可表示）时**报错而不是偷偷重抽**。
- 原始浮点噪声/原始含噪计数与后处理截断值分开记录（`LaplaceRecord`）。

## 统计实验的地位

`demo.py` 第 6 节与测试中的频率/均值/方差检查仅用于诊断实现是否明显错误，
**不构成也不替代隐私证明**。形式保证只来自 `verify_dp` 的穷举精确比值检验。

## 文件

- `dpcount/discrete.py`：精确有理离散机制、无偏抽样、确定性反 CDF、穷举验证。
- `dpcount/continuous.py`：连续 Laplace 浮点接口（数值近似）。
- `demo.py`：合成数据运行样例。
- `tests/`：26 个 unittest 用例（参数校验、精确概率、反 CDF、可复现性、负值不
  重采样、穷举验证边界反例、连续接口等）。
