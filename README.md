# factorelim — 有限离散因子图上的变量消元

纯 Python 3.11 标准库实现（测试用 `unittest`），无第三方依赖。

## 功能

- **变量消元求边缘**：`query(graph, query_vars, evidence, order)` 返回归一化后验与归一化常数 `Z`（即 `P(evidence)`；无证据时即配分函数）。
- **消元顺序控制中间表大小**：`order` 由调用方指定，缺省时用 min-size 贪心（`greedy_order`）；`EliminationStats` 记录每一步消元前乘积表的大小，`max_intermediate_size` 即该顺序的代价。
- **证据约束与零概率**：`Factor.restrict` 固化证据变量；表中 0 项原样参与运算，支持确定性（0/1）因子。
- **证据矛盾报错**：`Z == 0` 时抛出 `ContradictoryEvidenceError`（精确、枚举、采样三条路径一致）。
- **同变量不同域拒绝**：`FactorGraph.add_variable` 重复声明不同域、或两个因子同名变量域不一致（`Factor.multiply`）时抛 `DomainConflictError`。
- **随机源由调用方注入**：`estimate_query(..., rng=random.Random(seed), seed=seed)`，本库从不自行 reseed；结果记录 `seed` 与 `n_samples`。
- **精确 / 经验分别标注**：`QueryResult.method == "exact"`（字符串带 `[EXACT]`），`EmpiricalResult.method == "empirical"`（带 `[EMPIRICAL]` 及 seed/样本量）。
- **小图与全枚举一致**：`brute_force_query` 为参考实现，测试中对链式、星形及 30 个随机小图逐一比对边缘与 `Z`。

## 结构

```
factorelim/
  factors.py    # Factor：multiply / sum_out / restrict / normalize
  graph.py      # FactorGraph：变量-域注册表，域冲突拒绝
  eliminate.py  # variable_elimination / greedy_order / query + EliminationStats
  enumerate.py  # brute_force_query（小图参考实现）
  sampling.py   # estimate_query（注入 RNG 的均匀提议重要性加权）
  errors.py     # ContradictoryEvidenceError / DomainConflictError / ...
examples/demo.py
tests/          # unittest，34 个用例
```

## 用法

```python
import random
from factorelim import FactorGraph, query, estimate_query

g = FactorGraph()
for v in ("Rain", "Sprinkler", "WetGrass"):
    g.add_variable(v, (0, 1))
g.add_factor(("Rain",), [0.8, 0.2])
g.add_factor(("Rain", "Sprinkler"), [0.6, 0.4, 0.99, 0.01])
g.add_factor(("Rain", "Sprinkler", "WetGrass"),
             [0.95, 0.05, 0.10, 0.90, 0.20, 0.80, 0.01, 0.99])

exact = query(g, ("Rain",), evidence={"WetGrass": 1})
print(exact.normalizer, exact.probabilities)   # Z = P(WetGrass=1)

rng = random.Random(20261001)                   # 调用方持有随机源
est = estimate_query(g, ("Rain",), evidence={"WetGrass": 1},
                     n_samples=20000, rng=rng, seed=20261001)
print(est.seed, est.n_samples, est.probabilities)
```

## 运行样例

`python3 examples/demo.py` 输出：

```
=== Exact inference (variable elimination) ===
[EXACT] P(Rain)  Z=0.47238
  (0,): 0.660485
  (1,): 0.339515
  elimination order=('Sprinkler',) max_intermediate=4

=== Elimination order controls intermediate table size ===
  star graph, center first: sizes=[32, 16, 8, 4, 2] max=32
  star graph, greedy order ('L0', 'L1', 'L2', 'L3', 'X'): sizes=[4, 4, 4, 4, 2] max=4
  same normalizer either way: Z=1.000000

=== Empirical estimation (caller-injected RNG) ===
[EMPIRICAL] P(Rain)  Z~0.470577  (seed=20261001, samples=20000, nonzero=20000)
  (0,): 0.648511
  (1,): 0.351489

=== Exact vs. empirical side by side ===
  Rain=0: exact=0.6605 empirical=0.6485
  Rain=1: exact=0.3395 empirical=0.3515
  Z: exact=0.472380 empirical=0.470577

=== Contradictory evidence ===
  raised ContradictoryEvidenceError: evidence {'A': 1} has probability 0 (normalizer Z = 0)
```

## 测试

`python3 -m unittest discover -s tests -v`：34 个用例全部通过，覆盖

- 因子乘 / 边缘化 / 证据固化 / 归一化的正确性与零概率保持；
- 同名变量不同域（图注册表与因子相乘两处）拒绝；
- 矛盾证据在精确、枚举、采样路径均抛 `ContradictoryEvidenceError`；
- 消元顺序对中间表大小的影响（星形图 32 vs 4），不同顺序归一化常数一致；
- 30 个随机小图上变量消元与全枚举的边缘和 `Z` 一致（容差 1e-9）；
- 采样结果记录 seed 与样本量、同 seed 可复现、6 万样本下经验边缘与精确解误差 < 0.02。
