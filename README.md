# ecdfcal — ECDF 统计量与置换校准（无正态近似）

Python 3.11，仅标准库 + unittest。对两个离散样本集合计算：

- **经验 CDF（ECDF）**：右连续阶梯函数；并列（ties）在同一取值处**同时跳跃**，
  跳跃质量 = 该值全部重复观测的权重之和。观测的**重数（identity 计数）**与
  **权重质量**分别显式保存（`multiplicities` vs `masses`）。
- **总变差 TV** = ½·Σ|pₐ(x) − p_b(x)|（ pooled support 上，精确分数运算）。
- **经验 CDF 最大差（KS）** = maxₓ|Fₐ(x) − F_b(x)|。
- **置换校准 p 值**：小样本全枚举（精确结论），大样本 Monte Carlo（经验估计，
  附精确 Clopper-Peerson 二项区间，不用正态近似）。

## 设计要点

- **交换性由输入设计给出**：`Design.UNPAIRED_EXCHANGEABLE`（成组，参考集为全部
  C(n+m, n) 种标签重排）、`Design.PAIRED_EXCHANGEABLE`（配对，参考集为全部 2^m
  种符号翻转）、`Design.NOT_EXCHANGEABLE`（**拒绝输出校准 p 值**，仍报告观测统计量）。
- **精确 / 经验分别标注**：`CalibrationResult.kind ∈ {"exact", "monte_carlo"}`；
  精确结果给出分数 p 值，MC 结果给出 (exceed+1)/(B+1) 与 Clopper-Peerson 区间。
- **随机源由调用方注入**：`rng=random.Random(...)` 或 `seed=...`；结果记录
  `seed` 与抽样数 `reference_size`。未注入随机源时 MC 直接报错。
- **排名分片并行枚举**：组合按字典序 rank/unrank（组合数系统），`shard_ranges`
  把 [0, C(n+m,n)) 切成互不相交的连续区间，`ProcessPoolExecutor` 并行统计，
  不重不漏（测试中与 `itertools` 暴力枚举逐一核对）。
- **标签重新排序不改变结果**：输入先规范化为排序后的典范形，再计算/抽样。
- **空样本拒绝**：`ECDF.from_samples([])` 与空样本 `calibrate` 均抛 `ValueError`。

## 运行

```bash
python3.11 examples/run_example.py     # 运行样例
python3.11 -m unittest discover -s tests -v   # 32 个测试
```

## 运行样例输出（节选）

```
== 1. ECDF with ties (simultaneous jumps, full multiplicity) ==
sample A: n=5 (sorted support with ties)
   value  mult     mass     F(x)
       1     1   0.2000   0.2000
       2     2   0.4000   0.6000
       3     1   0.2000   0.8000
       5     1   0.2000   1.0000
KS  = 13/30
TV  = 19/30

== 2. Exact unpaired permutation calibration (all C(11,5)=462 re-labellings) ==
  p-value = 97/231 (~0.4199)          # kind = exact

== 3. Monte Carlo calibration (caller-injected RNG, seed recorded) ==
  p-value = 719/1667 (~0.4313)        # kind = monte_carlo
  95% Clopper-Peerson interval: [0.4174, 0.4451]
  injected RNG seed = 20261002, draws = 5000

== 4. Paired design: exact sign-flip calibration (2^6 = 64) ==
  p-value = 7/8 (~0.8750)

== 5. Non-exchangeable design: calibrated p-value refused ==
  p-value: REFUSED (exchangeability is not justified by the input design)

== 6. Rank-sharded parallel enumeration == serial enumeration ==
  identical: True

== 7. Label reordering invariance ==
  identical: True
```

## 测试结果

```
Ran 32 tests in 0.379s
OK
```

覆盖：ECDF 并列同时跳跃/权重/重数、空样本拒绝、KS/TV 已知值、
rank↔unrank 全量往返、分片不重不漏、精确枚举 vs itertools 暴力核验、
分片并行 == 串行、MC 可复现性与乱序不变性、MC 区间覆盖精确值、
配对精确 vs 符号翻转暴力核验、非交换设计拒绝输出、精确/MC 标签分离、
Clopper-Peerson 边界与已知值。

## 文件结构

- `ecdfcal/ecdf.py` — ECDF（并列、权重、重数）
- `ecdfcal/statistics.py` — 频数表、TV、KS（精确分数）
- `ecdfcal/combinatorics.py` — 组合 rank/unrank 与分片
- `ecdfcal/calibration.py` — 置换校准（精确 / MC / 拒绝）、Clopper-Peerson
- `tests/test_ecdfcal.py` — unittest 套件
- `examples/run_example.py` — 运行样例
