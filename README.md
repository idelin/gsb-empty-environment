# nearest_sources

按加权距离把顶点分配给最近来源（Python 3.11，仅标准库）。

- 边权：非负有理数（`int` 或 `fractions.Fraction`），精确计算，拒绝负值与浮点。
- 顶点、边均为字符串 ID；保留孤立点与多重边。
- 来源活动时间区间左闭右开 `[start, end)`，`end=None` 表示无限。
- 并列归属：到多个来源最短距离相等时，顶点归属来源集合。
- 零权连通块：块内距离相等，并列来源取并集后完整传播到块内每个顶点。
- 来源停用：`deactivate_source` 重算受影响区域，返回 `ChangeReport`
  （逐顶点归属变化、归属变化边界边、新距离的路径证明）。

## 运行

```sh
python3.11 nearest_sources.py      # 运行样例
python3.11 -m unittest -v          # 运行测试
```

## 示例

```python
from fractions import Fraction
from nearest_sources import Graph

g = Graph()
g.add_edge("e_AX", "A", "X", Fraction(3, 2))
g.add_edge("e_XB", "X", "B", Fraction(3, 2))
g.add_source("srcA", "A")
g.add_source("srcB", "B")

snap = g.assignments(0)
snap.owners_of("X")            # frozenset({'srcA', 'srcB'})

report = g.deactivate_source("srcA", 10)
report.changes["X"].new_sources   # frozenset({'srcB'})
report.boundary_edges             # ['e_XB']
report.changes["A"].proof         # B —e_XB(3/2)→ X —e_AX(3/2)→ A，累计 3
```
