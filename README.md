# bootstrap_ci

Python 3.11，仅标准库；测试使用 `unittest`。按**观测单位**重采样的
bootstrap 置信区间库。

## 设计保证

- **按观测单位重采样**：重采样单位是完整观测、完整配对、层内观测或连续
  区块，绝不逐行独立抽样，单位内相关性在构造上得以保持。
- **配对样本成对抽**：`PairedPlan` 中配对的两个分量永不拆分。
- **分位数定义显式**：Monte Carlo 分布用 Hyndman–Fan type 7（线性插值）；
  精确枚举的离散分布用逆 CDF（type 1），权重为精确有理数
  （`fractions.Fraction`）。
- **退化分布单独报告**：`result.degenerate=True`，区间退化为一点
  （相同值样本 → 点区间）。
- **百分位与 BCa 分开实现**：`intervals.percentile_interval` 与
  `intervals.bca_interval` 互不依赖；jackknife 退化或加速度不可估时抛出
  `DegenerateJackknifeError` / `BcaNotEstimableError`，明确失败而非返回
  误导性区间。
- **无效重采样不静默丢弃**：默认 `invalid_policy="raise"` 立即中止；
  显式选择 `"omit"` 时结果中报告 `invalid_count` 与
  `effective_resamples`，不会暗中改变有效 B 从而影响置信度。
- **随机源由调用方注入**：`RandomSource(seed)`；每个 replicate 的随机流
  由 `(seed, sample_id)` 经 SHA-256 派生，与求值顺序无关——并行/乱序
  执行不改变结果。每个结果记录 `seed` 与 `n_units`。
- **精确与经验分别标注**：`result.kind` 为 `"exact"`（全枚举，无 Monte
  Carlo 误差）或 `"monte_carlo"`（经验估计）。

## 重采样计划（`bootstrap_ci.plans`）

| 计划 | 重采样单位 | 边界规则 |
|---|---|---|
| `IIDPlan(values)` | 单个观测 | 有放回抽 n 次 |
| `PairedPlan(pairs)` | 整个配对 | 成对有放回抽 n 次 |
| `StratifiedPlan(strata)` | 层内观测 | 层不混合、无跨层迁移、各层样本量保持不变 |
| `MovingBlockPlan(series, L, boundary)` | 长度 L 的连续块 | `"circular"`：起点均匀于 0..n-1，下标模 n 回绕；`"truncate"`：起点均匀于 0..n-L，不回绕；拼接后截断至长度 n |

BCa 的 delete-one jackknife 对 IID / 配对 / 分层有定义；对移动块无定义，
请求 BCa 会抛出 `UnsupportedMethodError`（明确失败）。

## 用法

```python
from bootstrap_ci import IIDPlan, PairedPlan, RandomSource, bootstrap, bootstrap_exact

rng = RandomSource(seed=42)                      # 随机源由调用方注入
plan = IIDPlan([2.1, 2.5, 3.3, 3.7, 4.1, 4.4])
stat = lambda xs: sum(xs) / len(xs)

r = bootstrap(plan, stat, n_resamples=5000, rng=rng, method="percentile")
r = bootstrap(plan, stat, n_resamples=5000, rng=rng, method="bca")

pairs = PairedPlan([(68.0, 66.9), (72.5, 70.8), (70.1, 69.5)])
r = bootstrap(pairs, lambda ps: sum(a - b for a, b in ps) / len(ps),
              n_resamples=5000, rng=RandomSource(7))

exact = bootstrap_exact(IIDPlan([1.0, 2.0, 3.0, 4.0, 5.0]), stat)  # kind="exact"
```

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 66 个测试
python3.11 examples/run_examples.py           # 运行样例
```

## 测试结果

```
Ran 66 tests in 9.809s
OK
```

覆盖：分位数定义（type 7 / 逆 CDF 精确有理边界）、随机流按样本 ID 分流
的乱序不变性、配对联合重采样、分层大小保持与不跨层、移动块两种边界
规则、BCa 与百分位分离及其全部明确失败路径（z0 无穷、jackknife 退化、
调整分母为零）、相同值样本点区间、无效重采样两种策略、精确枚举与手算
分布一致、Monte Carlo 向精确解收敛（容差一个支撑点间距）。

## 样例输出（节选）

```
[monte_carlo] percentile 95% CI (iid plan): (3.83313, 5.86687) | estimate=4.83333 n_units=12 B=5000 invalid=0 seed=20261002
[monte_carlo] bca 95% CI (iid plan): (3.85, 5.89167) | estimate=4.83333 n_units=12 B=5000 invalid=0 seed=20261002
[monte_carlo] bca 95% CI (paired plan): (1.05, 1.9125) | estimate=1.35 n_units=8 B=5000 invalid=0 seed=11
[monte_carlo] percentile 95% CI (stratified plan): (0.48, 1.62) | estimate=1.06 n_units=10 B=5000 invalid=0 seed=12
[monte_carlo] percentile 95% CI (moving_block plan): (-0.32018, 0.0633974) | estimate=-0.129684 n_units=80 B=5000 invalid=0 seed=13
[exact] percentile 95% CI (iid plan): (1.8, 4.2) | estimate=3 n_units=5 support=21
[monte_carlo] percentile 95% CI (iid plan): (4.2, 4.2) | estimate=4.2 n_units=8 B=1000 invalid=0 seed=15, DEGENERATE (point interval)
BCa fails explicitly: all jackknife replicates are identical; the BCa acceleration constant is not estimable
default policy 'raise': statistic returned a non-finite value (nan) on replicate 1
explicit policy 'omit': ... B=500 invalid=36 ... -> effective B = 464
```

## 目录结构

```
bootstrap_ci/
  rng.py        # RandomSource：调用方注入，按样本 ID 分流（SHA-256 派生）
  quantile.py   # type 7 与逆 CDF 两种显式分位数定义
  plans.py      # IID / 配对 / 分层 / 移动块重采样计划
  intervals.py  # 百分位与 BCa（分开实现），jackknife 加速度
  core.py       # Monte Carlo 引擎，无效重采样策略，结果记录 seed/样本量
  exact.py      # 小样本全枚举精确 bootstrap 分布（Fraction 精确权重）
  errors.py     # 全部显式失败类型
tests/          # 66 个 unittest 测试
examples/run_examples.py
```
