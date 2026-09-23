# 动态多重图：桥、割点与双连通块

Python 3.11，仅标准库。维护多重图的桥（bridge）、割点（articulation point）
与双连通块（biconnected component），支持边的批量添加 / 删除，并可查询
某条边失效时会分开的顶点集合。

## 语义约定

- 顶点、边均使用字符串 ID；孤立点与多重边（平行边）都会保留。
- 自环：永远不是桥，不影响割点判定，在双连通块中单独成块。
- 平行边：两条平行边互为备份，任何一条都不是桥；删掉一条后剩余边变桥。
- 每条边带生效区间 `[start, end)`，**左闭右开**：`start <= t < end` 时存活。
- 批量更新（`apply_batch`）整批应用后重新全量计算，结果与从头运行
  全量算法完全一致（测试中与朴素删边/删点全量算法交叉验证）。

## API 概览（`dynamic_graph.py`）

| 方法 | 说明 |
| --- | --- |
| `add_vertex(v)` | 添加顶点（允许孤立点） |
| `add_edge(id, u, v, start=0, end=inf)` | 添加边，区间左闭右开 |
| `remove_edge(id)` | 删除边（顶点保留） |
| `apply_batch(adds=(), removes=())` | 批量增删，整批生效 |
| `bridges(t)` / `articulation_points(t)` | 时刻 t 的桥边 ID / 割点集合 |
| `biconnected_components(t)` | 双连通块（边集的列表，自环各自成块） |
| `components(t)` | 连通分量（含孤立点） |
| `separated_sets(id, t)` | 该边失效会分开的两个顶点集合；非桥返回 `None` |

核心算法为迭代式 Tarjan 低链（lowlink）算法，用边 ID 区分父边，
使平行边自然按回边处理；一次 DFS 同时求出桥、割点与双连通块，
复杂度 O(V + E)。

## 运行样例

```
$ python3 demo.py
--- 初始图 (t=0) ---
桥:             ['cd']
割点:           ['C']
双连通块(边集): [['cd'], ['ab', 'bc', 'ca'], ['ef1', 'ef2'], ['gg']]
连通分量(点集): [['A', 'B', 'C', 'D'], ['E', 'F'], ['G'], ['H']]
cd 失效分开:   [['A', 'B', 'C'], ['D']]
ab 失效分开:   None （非桥，分不开）
ef1 失效分开:  None （平行边，分不开）
--- 批量更新后：ef1 变桥；de 尚未生效 (t=0) ---
桥:             ['cd', 'ef1']
割点:           ['C']
双连通块(边集): [['cd'], ['ab', 'bc', 'ca'], ['ef1'], ['gg']]
连通分量(点集): [['A', 'B', 'C', 'D'], ['E', 'F'], ['G'], ['H']]
ef1 失效分开:  [['E'], ['F']]
--- t=15：de 在 [10,20) 内存活，两个分量连成一片 (t=15) ---
桥:             ['cd', 'de', 'ef1']
割点:           ['C', 'D', 'E']
双连通块(边集): [['ef1'], ['de'], ['cd'], ['ab', 'bc', 'ca'], ['gg']]
连通分量(点集): [['A', 'B', 'C', 'D', 'E', 'F'], ['G'], ['H']]
de 失效分开:   [['A', 'B', 'C', 'D'], ['E', 'F']]
--- t=20：de 到期（左闭右开，20 不存活） (t=20) ---
桥:             ['cd', 'ef1']
割点:           ['C']
双连通块(边集): [['cd'], ['ab', 'bc', 'ca'], ['ef1'], ['gg']]
连通分量(点集): [['A', 'B', 'C', 'D'], ['E', 'F'], ['G'], ['H']]
```

## 测试结果

```
$ python3 -m unittest test_dynamic_graph -v
test_randomized (...TestAgainstNaiveFullRecompute...) ... ok
test_batch_add_remove_atomic_view (...TestBatchUpdates...) ... ok
test_batch_equals_incremental_build (...TestBatchUpdates...)
一次性批量构建与逐条添加结果一致。 ... ok
test_errors (...TestBatchUpdates...) ... ok
test_neither_parallel_edge_is_bridge (...TestParallelEdges...) ... ok
test_remaining_edge_becomes_bridge_after_delete (...TestParallelEdges...) ... ok
test_isolated_vertices_kept (...TestSelfLoopsAndIsolatedVertices...) ... ok
test_self_loop_semantics (...TestSelfLoopsAndIsolatedVertices...) ... ok
test_bridge_status_changes_over_time (...TestTimeIntervals...) ... ok
test_left_closed_right_open (...TestTimeIntervals...) ... ok
test_separated_sets_of_tail (...TestTriangleWithTail...) ... ok
test_tail_edge_is_bridge (...TestTriangleWithTail...) ... ok

----------------------------------------------------------------------
Ran 12 tests in 0.075s

OK
```

覆盖要点：A、B 间两条平行边均非桥，删一条后剩余边变桥；三角形尾接 D
时连接 D 的边是桥；批量更新结果与朴素全量算法（删边/删点数连通分量）
在 60 组随机多重图 × 6 个时刻上逐一比对一致；自环、孤立点、平行边、
时间区间左闭右开均有专项测试。
