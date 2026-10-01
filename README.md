# factor_graph — 有限离散因子图的变量消元推理

Python 3.11，仅标准库，无第三方依赖。

## 功能

- **变量消元求边缘**：`FactorGraph.eliminate(query, evidence, order=None)` 返回精确边缘。
- **消元顺序控制中间表大小**：可传入自定义 `order`；缺省时用贪心 min-size 启发式
  （`greedy_order`），`elimination_width` 可评估任一顺序产生的最大中间因子表。
- **证据约束与零概率**：因子表允许 0 项（可编码确定性约束）；证据通过因子约简生效。
- **归一化常数**：`ExactMarginal.normalization` 即 Z = P(evidence)。
- **证据矛盾报错**：Z = 0 时抛出 `ContradictoryEvidenceError`（精确、枚举、采样路径一致）。
- **域一致性**：同一变量在不同因子中域不一致时抛出 `DomainMismatchError`。
- **随机源注入**：Gibbs 采样要求调用方传入 `random.Random`；结果记录 `seed` 与 `samples`。
- **结论标注**：精确结果为 `ExactMarginal`（`kind="exact"`），经验估计为
  `EmpiricalMarginal`（`kind="empirical"`，含 seed / 样本量 / burn-in）。
- **全枚举对照**：`enumerate_marginal` 提供暴力枚举真值，测试中与小图变量消元逐一核对。

## 文件

- `factor_graph.py` — 库（Factor / FactorGraph / 消元 / 枚举 / Gibbs / 顺序启发式）
- `test_factor_graph.py` — unittest 测试（19 个用例）
- `example.py` — 可运行样例（sprinkler 网络）

## 运行

```bash
python3.11 example.py      # 运行样例
python3.11 -m unittest -v  # 运行测试
```

## 用法速览

```python
import random
from factor_graph import FactorGraph

g = FactorGraph()
g.add(("C",), {"C": (0, 1)}, [0.5, 0.5])
g.add(("R", "C"), {"R": (0, 1), "C": (0, 1)}, [0.8, 0.2, 0.2, 0.8])

exact = g.eliminate(["R"], evidence={"C": 1})
exact.probability(1)      # 精确边缘，kind == "exact"
exact.normalization       # Z = P(C=1)

est = g.sample_gibbs(["R"], evidence={"C": 1},
                     rng=random.Random(42), samples=10000, seed=42)
est.probability(1)        # 经验估计，kind == "empirical"
est.seed, est.samples     # (42, 10000)
```

## 样例输出（`python3.11 example.py`）

```
=== Elimination order (controls intermediate table size) ===
star graph, greedy order ['L0', 'L1', 'L2', 'C', 'L3']: max intermediate table = 4 entries
star graph, center-first  ['C', 'L0', 'L1', 'L2', 'L3']: max intermediate table = 32 entries

=== Exact (variable elimination), kind='exact' ===
P(R | W=1)  exact:      P(R=1) = 0.7079   [kind=exact]
normalization constant Z = P(W=1) = 0.6471

=== Empirical (Gibbs sampling), kind='empirical' ===
P(R=1 | W=1) empirical: 0.7077   [kind=empirical, seed=2024, samples=20000]

=== Cross-check: full enumeration agrees with elimination ===
enumeration P(R=1 | W=1) = 0.7079, Z = 0.6471

=== Error handling ===
contradictory evidence -> ContradictoryEvidenceError: evidence {'A': 1} has zero probability (Z = 0)
domain mismatch        -> DomainMismatchError: variable 'C' already has domain (0, 1), got ('yes', 'no')
```

## 测试结果（`python3.11 -m unittest`）

```
Ran 19 tests in 0.445s

OK
```

覆盖：精确边缘与已知值/全枚举一致（含 30 个随机小图、随机消元顺序）、
联合边缘、零概率确定性因子、归一化常数 Z = P(evidence)、证据矛盾（三条推理路径均报错）、
证据值越界、未知变量、查询/证据重叠、域不一致拒绝、消元顺序对中间表大小的控制
（星形图 4 vs 32）、Gibbs 估计与精确值接近、同 seed 可复现、exact/empirical 标注。
