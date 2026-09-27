# dp_audit：合成邻接数据上的差分隐私经验审计工具

> **重要声明**：本工具只做**经验（统计）审计**。统计实验可以**证伪**一个隐私声明
> （发现违规），但**永远不能证明**一个机制满足形式化差分隐私：任何有限实验只覆盖
> 一对邻接数据集、一个输出事件和有限次随机抽样，且每个结论都带有抽样误差。
> 本工具**只处理合成数据**，不涉及任何真实数据。

纯 Python 3.11 标准库实现，无第三方依赖；测试使用 `unittest`。

## 定义

- **数据集**：合成的 0/1 记录元组，例如 `D = (0,)*10`。
- **邻接关系**：加一（注入）邻接，`D' = D + (injected_record,)`。
- **事件 E**：机制输出上的谓词，默认为 `output >= t`。
- **ε-DP 约束**：机制 M 满足 ε-DP，当且仅当对**所有**邻接对和**所有**事件：
  `P(M(D) ∈ E) ≤ exp(ε) · P(M(D') ∈ E)`（两个方向都要成立）。

## 方法

1. 对 `D` 与 `D'` 各独立采样 `n_trials` 次（seed 派生两个独立随机流），
   估计 `p = P(M(D) ∈ E)` 与 `p' = P(M(D') ∈ E)`。
2. 用 **Clopper-Pearson 精确区间**（置信度 `1-α`）给出 `p`、`p'` 的置信范围。
3. 取比值 `max(p,p')/min(p,p')` 的保守置信区间（两个 `1-α` 区间的交集，
   联合覆盖率 ≥ `1-2α`）。经验 ε 估计为 `ln(比值)`。
4. **判定**：若比值置信下界 > `exp(claimed_ε)`，则判定**违规**，报告中给出
   可复核事件（事件定义 + seed + 试验次数 + α），退出码为 1。
5. **零观测处理**：`k = 0` 绝不当作真实概率 0。报告单侧 Clopper-Pearson 上界
   `1 - α^(1/n)`（即"rule of three"，约 `3/n`），并在报告中附加说明。

## 内置机制（均为计数查询，可注入）

| 机制 | 说明 | 是否满足其 ε 声明 |
|---|---|---|
| `laplace` | `count(D) + Laplace(1/ε)` | 是（加一邻接下 ε-DP） |
| `leaky` | 以概率 `leak_probability` 直接输出精确计数 | 否（用于演示违规检测） |
| `deterministic` | 精确计数，无噪声 | 否（同时演示零观测处理） |

## 参数

| 参数 | 默认 | 含义 |
|---|---|---|
| `--mechanism` | `laplace` | 被审计机制 |
| `--epsilon` | `1.0` | 机制声称的 ε |
| `--leak-probability` | `0.2` | `leaky` 机制的泄漏概率 |
| `--base-size` | `10` | 基础数据集的合成 0 记录条数 |
| `--injected-record` | `1` | 注入的记录（0 或 1） |
| `--threshold` | `sum(D)+injected` | 事件阈值 t（事件为 `output >= t`） |
| `--trials` | `20000` | 每个数据集的蒙特卡洛试验次数 |
| `--alpha` | `0.05` | 置信区间显著性水平 |
| `--seed` | `20260928` | 主种子，完整记录在报告中 |
| `--json` | 关 | 同时输出完整 JSON 报告 |

seed、试验次数、α、事件定义、数据集描述全部写入报告，实验可完整复现。

## 运行样例（实际输出）

### 1. 合规机制：未发现违规（注意：这不是隐私证明）

```
$ python3.11 -m dp_audit.cli --mechanism laplace --epsilon 1.0 --trials 20000 --seed 20260928
=== Empirical DP audit (synthetic data only; NOT a formal privacy proof) ===
Mechanism          : laplace_count {'epsilon': 1.0}
Adjacency          : add-one (injection); base dataset of 10 synthetic records; D' = D + (1,)
Event E            : output >= 1
Trials per dataset : 20000 (seed=20260928, alpha=0.05, confidence=95%)
P(M(D)  in E)      : 0.1831  CI [0.177761, 0.188531]  (k=3662)
P(M(D') in E)      : 0.49885  CI [0.491896, 0.505804]  (k=9977)
Ratio max/min      : 2.72447  CI [2.6091, 2.84541] (conservative)
Empirical epsilon  : 1.00227 (lower bound 0.959006) vs claimed 1
Verdict            : no violation detected (not a proof of privacy)
```

退出码 0。经验 ε 下界 0.959 ≤ 声称的 1.0，未证伪；理论上界为 `e^1 ≈ 2.718`，
与点估计 2.724 一致。

### 2. 明显违规机制：检出并给出可复核事件

```
$ python3.11 -m dp_audit.cli --mechanism leaky --epsilon 1.0 --leak-probability 0.2 --trials 20000 --seed 20260928
=== Empirical DP audit (synthetic data only; NOT a formal privacy proof) ===
Mechanism          : leaky_laplace_count {'epsilon': 1.0, 'leak_probability': 0.2}
Adjacency          : add-one (injection); base dataset of 10 synthetic records; D' = D + (1,)
Event E            : output >= 1
Trials per dataset : 20000 (seed=20260928, alpha=0.05, confidence=95%)
P(M(D)  in E)      : 0.1471  CI [0.142218, 0.152085]  (k=2942)
P(M(D') in E)      : 0.5975  CI [0.590665, 0.604307]  (k=11950)
Ratio max/min      : 4.06186  CI [3.88377, 4.24917] (conservative)
Empirical epsilon  : 1.40164 (lower bound 1.35681) vs claimed 1
Verdict            : VIOLATION DETECTED
```

退出码 1。比值下界 3.88 > `e^1 ≈ 2.718`，违规成立；可复核事件为
`output >= 1`，`seed=20260928`，`n_trials=20000`，`alpha=0.05`。

### 3. 确定性机制：零观测不等于零概率

```
$ python3.11 -m dp_audit.cli --mechanism deterministic --epsilon 1.0 --trials 20000 --seed 20260928
=== Empirical DP audit (synthetic data only; NOT a formal privacy proof) ===
Mechanism          : deterministic_count {}
Adjacency          : add-one (injection); base dataset of 10 synthetic records; D' = D + (1,)
Event E            : output >= 1
Trials per dataset : 20000 (seed=20260928, alpha=0.05, confidence=95%)
P(M(D)  in E)      : 0  CI [0, 0.000184427]  (k=0)
P(M(D') in E)      : 1  CI [0.999816, 1]  (k=20000)
Ratio max/min      : inf  CI [5421.2, inf] (conservative)
Empirical epsilon  : inf (lower bound 8.59807) vs claimed 1
Verdict            : VIOLATION DETECTED
Zero-observation   : 0 events observed in 20000 trials; the true probability is NOT 0. One-sided Clopper-Pearson upper bound at confidence 95%: 0.000149775 (rule-of-three approximation 3/n = 0.00015).
```

`k=0` 时报告的是上界 `1-(α/2)^(1/n) ≈ 1.84e-4`（双侧 CI）与单侧上界
`1-α^(1/n) ≈ 1.50e-4`，而不是把真实概率当作 0。

## 测试

```
$ python3.11 -m unittest discover -s tests -v
...
----------------------------------------------------------------------
Ran 23 tests in 7.149s

OK
```

测试覆盖：

- `tests/test_stats.py`：二项尾概率、Clopper-Pearson 边界性质（零观测上界、
  单调性、包含 MLE、置信度越高区间越宽）、模拟覆盖率 ≥ 名义水平、rule of three。
- `tests/test_auditor.py`：同 seed 完全复现、seed/试验数完整记录、报告可 JSON
  序列化、合规机制不误报、夸大隐私声明被证伪、leaky 机制违规且给出可复核事件、
  确定性机制的零观测处理、不可能事件判为"无法结论"而非违规。

## 项目结构

```
dp_audit/
  __init__.py
  stats.py       # Clopper-Pearson 精确区间、rule of three（纯标准库）
  mechanisms.py  # 合成可注入机制（laplace / leaky / deterministic）
  auditor.py     # 蒙特卡洛审计、比值置信区间、判定与报告
  cli.py         # 命令行入口：python3.11 -m dp_audit.cli
tests/
  test_stats.py
  test_auditor.py
```

## 局限（再次强调）

- 经验审计只能**证伪**，不能**证明**形式化差分隐私。
- 结果只对所测的邻接对、事件、参数和样本量负责；换一组事件可能暴露新问题。
- 比值区间是两个 `1-α` 区间的交集，联合覆盖率 ≥ `1-2α`，属保守估计。
- 全部实验在合成数据上进行，结论不直接外推到真实数据分布。
