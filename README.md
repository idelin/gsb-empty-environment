# nearest-sources

按加权最短路把顶点分配给最近来源（允许并列归属），支持来源按时间区间
启停、停用后重算并输出归属变化边界与距离证明。纯标准库，Python 3.11+。

## 规则

- 顶点、边均为字符串 ID；多重边（同对顶点多条边）与孤立点都保留。
- 边权为非负有理数：`int` / `Fraction` / `"3/2"` 字符串均可，`float`
  与负数直接抛错，全程 `Fraction` 精确运算。
- 来源挂在顶点上，可带多个 `[start, end)` 时间区间（左闭右开，
  `end=None` 表示无限）。
- `assign(t)` 返回每个顶点的归属集合（并列时多个来源）与最近距离；
  不可达顶点归属为空、距离为 `None`。
- 零权边两端距离必相等，因此零权连通块共享同一归属集合：块内并列
  来源完整传播到每个成员。
- `deactivate_and_diff(src, t)` 在时刻 `t` 停用来源并重算，输出：
  - `changes`：归属发生变化的顶点（旧集合 → 新集合）；
  - `boundary_edges`：变化区域的边界边（恰好一个端点归属变化）；
  - `owner_borders`：新归属下两侧归属集合不同的区域分界线；
  - `proofs`：每个变化顶点对其每个新来源的最短路证明（边 ID 序列，
    权重累计等于距离，可用 `Assignment.verify_proof` 独立校验）。

## 运行

```sh
python3 nearest_sources.py      # 演示样例
python3 -m unittest -v          # 测试
```

## 示例

```python
from nearest_sources import Network

net = Network()
net.add_edge("e1", "A", "X", 2)
net.add_edge("e2", "X", "B", 2)
net.add_source("SA", "A")          # 默认区间 [0, ∞)
net.add_source("SB", "B")

a = net.assign(10)
a.owners_of("X")                   # frozenset({"SA", "SB"})，等距并列
diff = net.deactivate_and_diff("SA", 10)
diff.after.owners_of("X")          # frozenset({"SB"})
[(c.vertex, c.before, c.after) for c in diff.changes]
diff.boundary_edges                # 变化区域边界
diff.proofs["X"]["SB"]             # B --e2(w=2)--> X，总距离 2
```
