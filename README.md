# hyperfold — 分层图折叠与超边投影

纯 Python 3.11 标准库实现。顶点 / 边 / 组均为字符串 ID，支持：

- **折叠投影**：组折叠为超节点，跨组边聚合为超边；组内边绝不计入虚假的跨组自环。
- **边证据回溯**：每条超边保留原始边 ID 多重集合（`View.hyperedge_sources`），
  反向索引 `View.locate` / `View.edge_loc`。
- **嵌套与移动**：组可嵌套；`Graph.move` 原子修订超边重数、原始边反查与嵌套成员计数
  （`Graph.member_count`），且只访问移动节点的祖先链与关联边（`View.reindexed` 可观测）。
- **多视图与快照**：同一图上多个独立 `View`；`snapshot()` / `restore()` 历史快照，
  图变更后恢复会基于当前图重投影，快照内保留历史索引作为证据。
- **原子批处理**：`Graph.apply([...])` 先整体校验（ID 冲突、层次成环、未知引用），
  任一失败整批拒绝，不产生部分写入。
- **时间区间**：边在 `[start, end)`（左闭右开）上活跃；孤立点与多重边完整保留。
- **撤回**：`Graph.undo()` 撤回移动，`View.undo()` 撤回折叠 / 展开。

## 运行

```bash
python3 demo.py            # 运行样例
python3 -m unittest -v     # 测试（含随机展开/移动/撤回与重投影的差分对照）
```

## 最小示例

```python
from hyperfold import Graph, View, AddVertex, AddGroup, AddEdge, Move

g = Graph()
g.apply([AddVertex("A"), AddVertex("B"), AddVertex("C"), AddGroup("G"),
         Move("A", "G"), Move("B", "G"),
         AddEdge("ab", "A", "B"), AddEdge("e1", "A", "C"), AddEdge("e2", "B", "C")])
v = View(g)
v.collapse("G")
v.hyperedge_sources("G", "C")   # ['e1', 'e2'] —— 合并后仍可回溯两条原始边
v.locate("ab")                  # None —— 组内边不计入外连边
```
