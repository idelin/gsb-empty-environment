# dpcount — 主体级差分隐私计数（仅合成数据）

Python 3.11，仅标准库，测试用 `unittest`。仓库中所有数据均为合成数据。

## 定义与参数

- **查询**：记录计数。邻接关系为**主体级**加/减一个主体。
- **主体敏感度 Δ（`sensitivity`）**：单个主体最多可贡献的记录数，即计数查询的 L1 敏感度。
- **隐私参数 ε（`epsilon`）**：必须 `> 0`，否则两个机制都抛 `ValueError`。
- **有限输出域**：`{0, ..., max_count}`。
- **纯 ε-DP**：对所有相邻数据集 `D, D'` 与所有输出 `y`，
  `P(y|D') / P(y|D) ≤ e^ε`。

## 机制

### 1. 连续 Laplace（`dpcount.laplace.LaplaceMechanism`）— 数值近似

经典接口，噪声 `Laplace(0, Δ/ε)`，经逆 CDF 采样
`F⁻¹(u) = -b·sign(u-½)·ln(1-2|u-½|)`。**明确标记为数值近似**
（`CountResult.exact = False`）：浮点舍入与截断尾部使真实分布只是近似
Laplace，ε 保证仅为近似意义。

- 随机源可注入（`uniform` 回调）；`inverse_cdf(u)` 对固定输入确定，可复算。
- 负噪声**保留**，不重采样；`CountResult` 分开记录 `raw`（原始含噪值）与
  `postprocessed`（后处理值，默认钳位到 ≥ 0）。

### 2. 有限域离散 Laplace（`dpcount.discrete.DiscreteLaplaceMechanism`）— 精确验证

整数域 Z 上的离散 Laplace（双边几何）分布钳位到 `{0..max_count}`：

- 未钳位：`P(y|c) = (1-r)/(1+r) · r^|y-c|`；钳位为确定性后处理，保持纯 DP。
- 基数 `r` 为**精确有理数**，取 `exp(-ε/Δ)` 的可证有理上界
  （Taylor 部分和 `S_n ≤ e^x` ⟹ `1/S_n ≥ e^-x`），因此
  `r^-Δ ≤ e^ε`，保证是保守的。
- 所有概率为 `Fraction`：尾部质量是几何级数的精确有理和；域内**不存在
  零概率输出**（浮点尾部不会把合法输出概率变成 0）。
- 采样为整数上的精确逆 CDF：权重为整数，抽 `k ∈ [0, Z)`（`randbelow` 可注入，
  默认 `secrets.randbelow`），走累积权重。无偏整数抽样，全程无浮点。
  `inverse_cdf(c, k)` 确定可复算。
- 隐私论证（三角不等式，归一化常数与 `c` 无关）：
  `P(y|c')/P(y|c) = r^(|y-c'|-|y-c|) ≤ r^-d ≤ r^-Δ ≤ e^ε`。

### 形式验证（非抽样频率）

`verify_dp()` 对有限邻接域**穷举**所有相邻计数对（`|c-c'| ≤ Δ`）与所有输出，
用精确有理比较检验概率比上界，返回 `VerificationResult`（含最大观测比值、
检查的对数/输出数、反例）。这是有限域上的形式检验；**抽样频率实验仅作
采样器健全性示意，不作为隐私证明**（见 `examples/run_example.py` 第 4 节，
输出中明确标注）。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 22 个测试
python3.11 examples/run_example.py            # 端到端样例（合成数据）
```

## 限制

- 形式验证针对给定有限域与参数穷举成立；更换参数需重新验证。
- 连续 Laplace 接口为浮点数值近似，精确保证请使用离散机制。
- 统计/频率实验不构成形式隐私证明。
