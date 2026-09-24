# walkgraph — 版本化图游走计数与字典序第 k 条查询

纯标准库实现（Python 3.11+），支持：

- **版本化多重有向图**：顶点、边均为字符串 ID；保留孤立点与多重边；
  每次变更提交新版本，旧版本通过 `graph.at(v)` 继续查询。
- **长度区间计数**：`count_walks(s, t, lo, hi)` 统计长度落在左闭右开
  区间 `[lo, hi)` 内的 s→t 游走数；默认任意精度大整数，可选 `mod`
  模数。矩阵幂块按 `(version, mod)` 缓存，边增删只失效新版本的相关
  块，旧版本缓存保留。
- **无限长度模式**：`hi=None` 时若存在"从 s 可达且能通向 t"的环，
  返回 `infinite=True` 及 `CycleEvidence`（环节点/边、可达前缀、
  通往终点后缀）；无关环（到不了 t）不影响判定。
- **字典序第 k 条游走**：`kth_walk(s, t, length, k)` 按游走签名
  `(v0, e0, v1, e1, …, vL)` 的字典序选取。选择始终使用本查询内部
  重算的精确大整数 DP，**绝不复用计数结果（尤其取模结果）做排序选择**。
- **输出轨迹预算**：`max_length` 限制 DP 深度与轨迹长度，超出返回
  `KthStatus.BUDGET_EXCEEDED`，不会误报为 `NO_PATH`。
- 每个结果都附带起终点可达切片 `reachable_slice`（从 s 可达且能到 t
  的顶点集）。

## 运行

```bash
python3.11 examples/demo.py          # 可运行样例
python3.11 -m unittest discover -v   # 测试（暴力枚举交叉核验）
```

## 快速上手

```python
from walkgraph import VersionedGraph, KthStatus

g = VersionedGraph()
with g.batch():                       # 批量变更 = 一次版本提交
    g.add_edge("e_sa", "S", "A")
    g.add_edge("e_aa", "A", "A")      # 自环 -> 无限游走
    g.add_edge("e_at", "A", "T")

v1 = g.version
res = g.at(v1).count_walks("S", "T", 0, None)
assert res.infinite and res.evidence.cycle == ("A",)

with g.batch():
    g.remove_edge("e_aa")             # 批量删自环 -> 无限性撤销
assert g.at().count_walks("S", "T", 0, None).count == 1
assert g.at(v1).count_walks("S", "T", 0, None).infinite   # 旧版本保留

kth = g.at().kth_walk("S", "T", length=2, k=0)
assert kth.status is KthStatus.OK and kth.edges == ("e_sa", "e_at")
```

## 语义要点

- 长度 0：仅当 `s == t` 时恰有一条（空）游走。
- 区间为空（`hi == lo`）计数为 0；`hi < lo` 抛 `ValueError`。
- 多重边是不同游走；第 k 条按 `(边ID, 目标点ID)` 的候选顺序展开。
- `k` 越界返回 `K_OUT_OF_RANGE`（附精确总数 `total`），无路径返回
  `NO_PATH`，超预算返回 `BUDGET_EXCEEDED`，三者严格区分。
