# pareto-paths

在总成本不超过预算、不重复顶点的约束下，枚举源点到汇点的简单路径，
输出帕累托最优的 (成本, 时延) 组合。Python 3.11，仅标准库，测试用 unittest。

## 语义约定

- **图模型**：有向多重图。顶点、边均为字符串 ID；孤立点保留；
  同一对顶点间的多重边按边 ID 区分并全部保留。
- **二维非负权**：每条边有 `cost >= 0` 与 `delay >= 0`，负权抛 `ValueError`。
- **时间区间左闭右开**：边的可用窗口为 `[start, end)`，到达该边起点
  的累计时延 `t` 满足 `start <= t < end` 才可通行；默认 `[0, +inf)`。
- **预算边界**：`cost <= budget`，等于预算时允许。
- **支配规则**：A 支配 B 当且仅当 `A.cost <= B.cost` 且 `A.delay <= B.delay`
  且至少一项严格更小。被两维同时支配的路径不输出；因此
  (成本1, 时延9) 与 (成本5, 时延2) 互不支配、可同在前沿上。
- **无可行路**：返回空集 `paths == ()`，且 `truncated == False`（空集是完整结果）。
- **结果上限与截断**：`max_results` 限制返回的前沿条数，`max_labels`
  限制搜索规模（默认 100000）。任一上限截断枚举时 `truncated=True`、
  `is_complete=False` —— 截断结果是部分前沿，**不能称为完整**。

## 用法

```python
from pareto_paths import Graph, pareto_paths

g = Graph()
g.add_vertex("lonely")                       # 孤立点保留
g.add_edge("e1", "S", "A", cost=2, delay=4)
g.add_edge("e2", "S", "B", cost=4, delay=1)
g.add_edge("e3", "A", "T", cost=2, delay=5)
g.add_edge("e4", "A", "B", cost=1, delay=1)
g.add_edge("e5", "B", "T", cost=1, delay=1)
g.add_edge("e6", "B", "T", cost=0, delay=6)  # 多重边 B->T

result = pareto_paths(g, "S", "T", budget=5, max_results=None)
for p in result.paths:        # 按成本升序
    print(p.cost, p.delay, p.vertices, p.edges)
assert result.is_complete     # 截断时此断言会失败
```

## 运行样例（`python3 demo.py`）

```
vertices (isolated kept): ['A', 'B', 'S', 'T', 'lonely']
edges (multi-edges kept): ['e1', 'e2', 'e3', 'e4', 'e5', 'e6']

full frontier [budget=5] -> COMPLETE
  cost=3 delay=11  vertices: S -> A -> B -> T  edges: ('e1', 'e4', 'e6')
  cost=4 delay=6   vertices: S -> A -> B -> T  edges: ('e1', 'e4', 'e5')
  cost=5 delay=2   vertices: S -> B -> T       edges: ('e2', 'e5')

capped at 2 results [budget=5] -> TRUNCATED (partial, not complete)
  cost=3 delay=11  vertices: S -> A -> B -> T  edges: ('e1', 'e4', 'e6')
  cost=4 delay=6   vertices: S -> A -> B -> T  edges: ('e1', 'e4', 'e5')

budget too small [budget=2] -> COMPLETE
  (empty set: no feasible path)
```

说明：路径 (成本4, 时延9)（S->A->T）与 (成本4, 时延7)（S->B->T 经 e6）
被 (成本4, 时延6) 两维支配，故不输出。

## 测试结果（`python3 -m unittest test_pareto_paths -v`）

23 个测试全部通过（OK），覆盖：

- 前沿语义：两个极端点同在前沿、被支配路径剔除、预算边界取等、
  无可行路返回空集、源点即汇点；
- 图结构：字符串 ID、多重边保留、孤立点保留、路径无重复顶点；
- 时间窗口：左闭（到达时刻 == start 可通行）、右开（== end 拒绝）；
- 截断：`max_results` / `max_labels` 触发截断且 `is_complete` 为 False，
  未截断时为完整结果；
- 参数校验：负权、倒置窗口、重复边 ID、非字符串 ID、未知顶点、负预算。

```
Ran 23 tests in 0.001s
OK
```

## 文件

- `pareto_paths.py` — 图结构与帕累托搜索实现
- `test_pareto_paths.py` — unittest 测试（23 例）
- `demo.py` — 运行样例
