# 版本轴动态图：历史连通性与分量大小批量查询

Python 3.11，仅标准库。在版本轴上支持边的增删，并可批量查询**任意历史版本**的
连通性与连通分量大小；删除只影响后续版本，且**不为每个查询复制整张图**。

## 语义约定

- 顶点、边均使用字符串 ID；边 ID 在存活期内唯一，删除后可复用。
- 孤立点始终保留（分量大小为 1），可显式 `add_vertex` 注册。
- 允许重边：不同边 ID 连接同一对顶点，删一条不影响另一条。
- 时间区间**左闭右开**：`version` 加的边在 `version` 即可见；
  `version` 删的边从 `version` 起消失（存活区间为 `[add, remove)`）。
- 同版本查询互相不可见、与登记顺序无关；结果按登记顺序返回。

## 算法：线段树套时间 + 可回滚并查集（离线）

1. 收集全部操作，把每条边的存活期表示为左闭右开区间 `[add, remove)`；
2. 将区间插入版本轴线段树，每条边只出现 O(log V) 次（V 为版本数）；
3. DFS 线段树，进入节点时合并边、离开时回滚（按大小合并、不做路径压缩，
   回滚精确撤销）；
4. 到达叶子（某版本）时，并查集恰好是该版本的图，批量回答该版本全部查询。

复杂度：时间 O((E log V + Q) · α(N))，空间 O(E log V + N)。
对比"每查询复制整图"的 O(Q · (N + E))，边只多存 log V 份，查询共享一次扫描。

## 文件

- `versioned_graph.py` — 核心模块（`VersionedGraph`、`RollbackDSU`）
- `main.py` — 运行样例（题目序列 + 孤立点/重边场景）
- `test_versioned_graph.py` — unittest，含与暴力重放实现的随机对拍

## 用法

```python
from versioned_graph import VersionedGraph

g = VersionedGraph()
g.add_edge("e1", "A", "B", version=1)
g.add_edge("e2", "B", "C", version=2)
g.remove_edge("e1", version=3)          # 只影响版本 3 及以后

g.query_connected("A", "C", version=2)  # 批量登记历史查询
g.query_connected("A", "C", version=3)
g.query_component_size("A", version=3)

for r in g.solve():                     # 一次性离线求解，按登记顺序返回
    print(r)
# v2: connected(A, C) = True
# v3: connected(A, C) = False
# v3: component_size(A) = 1
```

## 运行

```bash
python3 main.py                          # 运行样例
python3 -m unittest test_versioned_graph # 运行测试
```
