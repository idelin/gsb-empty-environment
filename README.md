# Versioned Graph：版本轴上的动态连通性

边在版本轴上增删，离线批量回答**过去任意版本**的连通性与连通分量大小查询。
纯 Python 3.11 标准库实现，测试使用 `unittest`。

## 语义约定

- 顶点、边均为字符串 ID；允许孤立点、多重边（平行边）与自环。
- 边按 ID 删除；删除一条平行边不影响其余平行边。
- 时间区间**左闭右开**：版本 `a` 加入、版本 `d` 删除的边存活于 `[a, d)`，
  删除只影响 `d` 及之后的版本。
- 顶点自注册（或被边引用）的版本起一直存在；查询不存在的顶点返回 `False` / `0`。
- 查询先注册、后批量执行；查询是只读的，同版本内注册顺序不影响结果。

## 算法

不为每个查询复制整张图。采用**版本轴线段树 + 可回滚并查集**（离线动态连通性）：

1. 每条边 / 每个顶点的存活区间挂到线段树的 O(log T) 个节点上（版本坐标先压缩）。
2. 对线段树做一次 DFS：进入节点应用修改，离开节点回滚。
3. 在叶子处用当前并查集状态回答该版本的全部查询。

复杂度（E 边事件、V 顶点、Q 查询、T 不同版本数）：
时间 `O((V + E) log T + Q α)`，空间 `O(V + E log T + Q)`。

## 用法

```python
from versioned_graph import VersionedGraph

g = VersionedGraph()
g.add_edge("AB", "A", "B", version=1)
g.add_edge("BC", "B", "C", version=2)
g.remove_edge("AB", version=3)

g.query_connected(2, "A", "C")       # 批量注册
g.query_connected(3, "A", "C")
g.query_component_size(3, "B")
print(g.execute())                   # [True, False, 2]
```

## 运行

```bash
python3.11 demo.py        # 运行样例
python3.11 -m unittest -v # 运行测试
```

## 文件

- `versioned_graph.py` — 实现（`VersionedGraph` + 内部可回滚并查集）
- `test_versioned_graph.py` — unittest，含与逐版本重建暴力解的随机对照测试
- `demo.py` — 题目场景的运行样例
