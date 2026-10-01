# bounded-mean: 有界随机变量均值估计（可审计保证）

纯 Python 3.11 标准库实现，测试基于 `unittest`。核心原则：**只在误差目标被认证时停止；预算耗尽则明确报告未达标，并保存全部停止证据。**

## 设计要点

- **分阶段检查（不偷看）**：仅在预先声明的检查点评估误差目标，每阶段使用
  zeta-2 分配的误差预算（`stage_delta(delta, j) = delta / (zeta(2) j^2)`，
  无穷级数和恰为 `delta`）。反复窥视绝不直接套单次固定样本 Hoeffding 界。
- **置信序列**：`cs_radius` 通过二进几何拼接（dyadic stitching）给出
  时间一致（time-uniform）半径，对任意自适应停止/自适应选变量策略安全。
- **多变量自适应采样**：每个变量预声明误差份额 `delta_i`（校验
  `sum(delta_i) <= delta`），联合保证是单纯的 union bound——**不假设独立性**，
  相关或共享样本不会被误当独立证据。`joint_sampler` 模式显式支持共享样本。
- **有限 Bernoulli 精确验证**：`finite.py` 用二项递推在声明的有限域上
  精确计算覆盖率（非模拟）；边界 `p ∈ {0,1}` 覆盖率恒为 1（严格推导）。
- **常数变量快速收敛**：`low == high` 时零样本精确收敛；观测值全部等于
  端点时启用严格的 Bernoulli 端点界 `width * (1 - delta^(1/n))`（O(1/n)），
  与 Hoeffding 平分该阶段预算，远快于 O(1/sqrt(n))。
- **随机源注入**：估计器从不自建随机源；`RecordingRandom` 记录 seed 与
  抽取次数。结论标注 `exact` / `empirical`。
- **参数校验**：`delta ∈ (0,1)`、`epsilon > 0`、检查点严格递增且不超预算、
  分配和不超过 `delta`、采样值必须在声明界内，否则抛错。

## 运行

```bash
python3.11 -m unittest discover -s tests -t . -v   # 38 个测试
python3.11 examples/run_demo.py                     # 运行样例，证据写入 examples/output/
```

## 运行样例输出（`examples/run_demo.py`）

```
== Part 1: staged single-variable estimation ==
  [MET] estimate=0.2891 radius=0.0473 after n=2048 samples (seed=42, kind=empirical)
  [NOT_MET] epsilon=0.01 with budget=100: radius=0.2043 -> target NOT certified, evidence kept (8 stages)
  [MET] all-zero observations: converged at n=256 via rule=bernoulli-endpoint (Hoeffding alone needs >1600)
== Part 2: adaptive multi-variable estimation ==
  [MET] draws_used=18432 (budget=200000)
    const_0.7    n=     0 estimate=0.7 radius=0.0 target=0.01 [MET/exact]
    bern_0.5     n=  2048 estimate=0.50244140625 radius=0.0483 target=0.05 [MET/empirical]
    bern_0.1     n= 16384 estimate=0.10089111328125 radius=0.0175 target=0.02 [MET/empirical]
  [MET] correlated shared samples: n_x=2048, n_y=2048 (union bound, no independence assumed)
== Part 3: exact finite-domain Bernoulli verification ==
  Hoeffding radius n=50, delta=0.05: radius=0.1921, min coverage on 201-point grid=0.9933 at p=0.48 -> ok=True (kind=exact)
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -t .
......................................
----------------------------------------------------------------------
Ran 38 tests in 0.722s

OK
```

覆盖：参数范围校验、Hoeffding/置信序列性质、逐样本窥视下阶段预算和
不超 `delta`、预算耗尽报 `NOT_MET` 且证据完整、端点规则快速收敛、
自适应选择集中于最难变量、相关共享样本的联合采样、二项递推与
`math.comb` 暴力值一致、有限网格上 Hoeffding 覆盖率的精确验证。

## 代码结构

- `bounded_mean/bounds.py` — 固定样本 Hoeffding、置信序列、阶段/端点界、参数校验
- `bounded_mean/single.py` — 单变量分阶段估计器（MET / NOT_MET + 证据）
- `bounded_mean/multi.py` — 多变量自适应估计器（预声明份额 + 置信序列）
- `bounded_mean/finite.py` — 有限域 Bernoulli 覆盖率精确递推验证
- `bounded_mean/rng.py` — 调用方注入随机源，记录 seed 与抽取次数
- `bounded_mean/evidence.py` — 停止证据 JSON 持久化
- `examples/run_demo.py` — 端到端样例；`tests/` — 38 个 unittest

## 用法摘要

```python
from bounded_mean import RecordingRandom, StagedEstimator

rng = RecordingRandom(seed=42)
est = StagedEstimator(
    name="coin", low=0.0, high=1.0, epsilon=0.05, delta=0.05,
    budget=20000, sampler=lambda r: 1.0 if r.random() < 0.3 else 0.0, rng=rng,
)
res = est.run()          # res.status in {"MET", "NOT_MET"}; res.stages 为全部证据
```
