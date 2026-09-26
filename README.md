# 有限候选子集上的指数机制采样（Python 3.11，仅标准库）

只处理**合成数据**。统计实验仅用于实现正确性的健全性检查，**不构成任何形式的隐私证明**。

## 定义

候选集合 $\mathcal{C}$（有限整数分数 $s_i$、分组 $g_i$），在满足约束的条件下选出大小恰为 `budget` 的子集 $S$。效用为子集分数和：

$$u(S)=\sum_{i\in S} s_i$$

分布定义在**合法子集**（满足分组配额与互斥组合约束）上，而非单个候选项：

$$P[S] = \frac{\exp\!\big(\varepsilon\,u(S)/(2\Delta)\big)}{\sum_{S'\in\mathcal{L}}\exp\!\big(\varepsilon\,u(S')/(2\Delta)\big)}$$

- $\mathcal{L}$：全部合法子集（穷举枚举，固定字典序，用于可重复抽样）
- `epsilon`：有限非负实数；`sensitivity`：有限非负实数，即效用敏感度 $\Delta$，其含义与界由调用方负责，本实现不做隐私论证
- 权重计算全部在**对数空间**进行：log-权重 $\ell_S=\varepsilon u(S)/(2\Delta)$，用 log-sum-exp（减去最大值后 `math.exp`，`math.fsum` 求和）规范化，极端整数分数不会溢出

## 参数

| 参数 | 类型 | 说明 |
|---|---|---|
| `Candidate(cid, score, group)` | 分数必须为 `int`（拒绝 float/bool） | 单项候选 |
| `Constraints(budget, group_quotas, mutex_pairs)` | `budget` 为非负整数 | 配额为各组每子集最大成员数；互斥对为不能共存的候选 id 对 |
| `epsilon` | 有限 float，≥0 | 为 0 时分布均匀 |
| `sensitivity` | 有限 float，≥0 | 为 0 时走显式确定规则，见下 |
| `max_domain_size` | int，默认 200000 | 合法域枚举上限 |

## 边界与错误行为

- **分数全相同**（或 $\varepsilon=0$）：合法子集上严格均匀。
- **敏感度 0**：不做除法。显式规则为——在效用最大的合法子集上均匀（唯一最大值则概率为 1）。
- **重复候选 id**：构造时 `ValueError`。
- **所有候选非法 / 预算不可满足**（`budget > n`、互斥或配额使 $\mathcal{L}=\varnothing$）：抛 `InfeasibleError`；绝不静默放松约束或截断候选后宣称原分布。
- **域过大**：`C(n,k)` 超过 `max_domain_size` 时抛 `InfeasibleError`，同样不截断。
- **极端参数**：$\varepsilon u/(2\Delta)$ 溢出为 `+inf` 时，分布退化为正无穷 log-权重子集上的均匀（即 $\varepsilon\to\infty$ 的极限）；全为 `-inf` 时退化为最大效用子集上的均匀；`u=0` 单独处理以避免 `inf*0=nan`。
- **空预算** `budget=0`：唯一合法子集为空集，概率 1。

## 近似数值误差

- 浮点路径为 float64：单次 `exp` 相对误差约 1 ulp（$\le 2^{-53}$），规范化与 `fsum` 累积后，小域上绝对误差典型为 $10^{-16}\sim10^{-18}$ 量级（demo 实测最大 $6.94\times10^{-18}$）。这是近似参考，**不是**误差形式化证明。
- `exact_probabilities(precision=50)` 用 `decimal.Decimal`（默认 50 位有效数字，`Decimal.exp`）按 float64 参数的精确二进制值重算，作为小域精确参考；`max_probability_error()` 直接返回两者最大绝对差，测试中要求 `< 1e-12`。

## 可审计随机源

`sample(seed)` 使用 `random.Random(seed)`，返回 `SamplingRecord`：`seed`、原始均匀数 `draw`、命中子集及其概率、累计区间 `[before, after)`、域大小。同种子逐位可复现，`mech.verify(record)` 重放并比对整条记录。不提供种子时用 `secrets` 生成种子并同样记录，事后仍可审计。

## 运行

```bash
python3.11 -m unittest -v   # 22 个测试
python3.11 demo.py          # 运行样例
```

## 文件

- `exponential_mechanism.py` — 机制实现（候选/约束、合法域枚举、log-sum-exp、Decimal 精确参考、可审计抽样）
- `test_exponential_mechanism.py` — unittest 套件（22 项，含极端分数、全非法、重复 id、敏感度 0、精确参考比对、频率健全性检查）
- `demo.py` — 运行样例
