# Dynamic Multigraph Decomposition

维护无向**多重图**的桥（bridge）、割点（articulation point）与双连通块
（biconnected component），支持边的**批量添加 / 删除**，并可查询"某条边
失效后会彼此分开的顶点集合"。仅使用 Python 3.11 标准库，测试基于
`unittest`。

## 语义约定

- **字符串 ID**：顶点与边均以字符串标识；边是独立实体，可存在**平行边**
  （同一对顶点间多条边）与**自环**。
- **多重图语义**：平行边中的每一条都不是桥；删掉一条后，剩余的一条变成
  桥。自环永远不是桥、不影响割点，且自环自身构成一个单元素双连通块。
- **孤立点保留**：`add_vertex` 加入的顶点、以及删除边后失去所有关联边的
  顶点，都会保留在图中（出现在连通分量里），不会被自动清除。
- **时间区间左闭右开**：每条边带有生效区间 `[start, end)`，在时刻 `t`
  生效当且仅当 `start <= t < end`。查询传入 `t=None` 时忽略区间、视为
  全部生效。
- **批量更新与全量一致**：`add_edges` / `remove_edges` 批量修改图后，
  所有查询结果等价于对当前图重新运行全量算法（内部按版本缓存；测试中用
  独立暴力实现 `reference.py` 交叉验证）。

## 文件

- `dynamic_graph.py` — 主实现 `DynamicMultigraph`（迭代式 Tarjan，多重图安全）
- `reference.py` — 暴力全量参考算法（删边/删点数连通分量 + 块的定义判定）
- `test_dynamic_graph.py` — `unittest` 测试套件
- `demo.py` — 运行样例

## API 摘要

```python
from dynamic_graph import DynamicMultigraph

g = DynamicMultigraph()
g.add_vertex("Z")                                   # 保留孤立点
g.add_edges([("e1", "A", "B"),                      # (id, u, v)
             ("e2", "A", "B", 3, 6)])               # (id, u, v, start, end)
g.remove_edges(["e1"])                              # 批量删除按边 ID

g.bridges(t=None)                # frozenset[边ID]
g.articulation_points(t=None)    # frozenset[顶点ID]
g.biconnected_components(t=None) # tuple[frozenset[边ID]]，自环/桥为单元素块
g.connected_components(t=None)   # tuple[frozenset[顶点ID]]，含孤立点
g.separated_by("e2", t=None)     # 桥 -> (frozenset, frozenset)；非桥 -> None
```

`separated_by(edge_id, t)`：若该边在时刻 `t` 是桥，返回删掉它之后两个
端点各自可达的顶点集合（二者瓜分了原连通分量）；否则返回 `None`。

## 运行

```bash
python3.11 demo.py            # 运行样例
python3.11 -m unittest -v     # 运行测试（19 个用例）
```

### 样例输出（节选）

```text
--- two parallel edges A<=>B (multigraph semantics) ---
bridges:              []
biconnected blocks:   [['e1', 'e2']]
separated if e1 fails: None

--- after removing e1 ---
bridges:              ['e2']
separated if e2 fails: ['A'] | ['B']

--- triangle A-B-C with tail C-D ---
bridges:              ['cd']
articulation points:  ['C']
biconnected blocks:   [['ab', 'bc', 'ca'], ['cd']]
separated if cd fails: ['A', 'B', 'C'] | ['D']

t=2: bridges=['e1']   # [start, end) 之外只有一条 A-B 边
t=3: bridges=[]       # 平行边生效，二者均非桥
t=6: bridges=['e1']   # 平行边在 end=6 处失效（右开）
```

### 测试结果

```text
Ran 19 tests in 0.52s
OK
```

覆盖：平行边非桥 / 删一条后变桥、三角形接尾边、自环、孤立点保留、
区间左闭右开、批量增删，以及 120 步随机批量更新与暴力全量算法在
9 个时刻上的逐项一致性（桥、割点、双连通块、连通分量、边失效分离集）。
