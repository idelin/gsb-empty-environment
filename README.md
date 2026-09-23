# 条件依赖图的增量影响分析

Python 3.11 标准库实现。模块维护顶点、带条件的有向多重边、显式事实集和当前时间，并在事实或时间变化后增量更新受影响集合与每个节点的最短解释链。

## 语义

- 字符串 ID：顶点和边都使用非空字符串 ID；同一起终点的多条边全部保留。
- 受影响集合：变更节点自身，加上从任一变更节点沿当前成立边可达的节点。
- 解释链：按“边数最少”选择；同长时按路径键做稳定字典序比较。
- 条件证据：条件求值同时返回使该边成立的最小事实见证；事实撤销会重算证据，而不是只追加新结果。
- 替代证据：当前边失效后，只在没有其他可达证据时让节点退出；另有证据则切换解释链。
- 循环：循环不会成为证据来源；删除唯一根后，纯循环节点全部退出。
- 时间区间：`start`、`end` 定义左闭右开区间 `[start, end)`。
- 孤立点：始终保留在图中，但不会出现在受影响集合中。

## 条件表达式

- `None`：恒成立。
- `"fact-a"`：要求事实 `fact-a` 存在。
- `("all", "a", "b")`：所有子条件成立。
- `("any", "a", ("all", "b", "c"))`：任一子条件成立，并选择最小见证。
- `("not", "blocked")`：要求事实不存在；否定本身不产生事实见证。

## API

```python
from impact_graph import ImpactGraph

graph = ImpactGraph()
graph.add_vertex("A")
graph.add_edge("a-to-b", "A", "B", "deployed", start=0, end=10)
graph.add_change("A")
graph.set_time(5)
graph.update_facts(add=("deployed",))
graph.affected_vertices()
graph.explain("B")
```

`Explanation` 提供：

- `root`：解释链的变更节点。
- `chain`：顶点链。
- `edges`：边 ID 链。
- `facts`：整条链依赖的事实并集。
- `steps`：逐跳解释，每步包含源点、边、终点和该边的事实见证。

## 运行

```bash
python3 demo.py
python3 -m unittest -v
```

## 演示结果

```text
1. Initial graph: isolated vertices stay, gate is closed
affected: compile, root, standby
release: no evidence

2. Facts make compile -> test -> release available
affected: compile, release, root, standby, test
release: root -> compile -> test -> release
edges   : compile-on-merge, test-normal, release-after-test
facts   : gate-open, signed

3. Revoke 'signed': release loses all evidence
affected: compile, root, standby, test
release: no evidence

4. Revoke compile edge fact: short chain disappears
affected: root, standby
test: no evidence

5. t=10 is included by half-open [10, 20): hotfix evidence exists
affected: release, root, standby, test
test: standby -> test
edges   : test-hotfix-window
facts   : (none)

6. t=20 is excluded by [10, 20): hotfix evidence disappears
affected: root, standby
test: no evidence

7. Main evidence restored
affected: compile, release, root, standby, test
release: root -> compile -> test -> release
edges   : compile-on-merge, test-normal, release-after-test
facts   : gate-open, signed
```

## 测试结果

```text
test_alternate_evidence_preserves_vertex (test_impact_graph.ImpactGraphTest.test_alternate_evidence_preserves_vertex) ... ok
test_compound_conditions_update_witness (test_impact_graph.ImpactGraphTest.test_compound_conditions_update_witness) ... ok
test_conditional_propagation_and_explanation (test_impact_graph.ImpactGraphTest.test_conditional_propagation_and_explanation) ... ok
test_cycle_does_not_prove_itself (test_impact_graph.ImpactGraphTest.test_cycle_does_not_prove_itself) ... ok
test_half_open_time_interval (test_impact_graph.ImpactGraphTest.test_half_open_time_interval) ... ok
test_lost_evidence_removes_affected_vertex (test_impact_graph.ImpactGraphTest.test_lost_evidence_removes_affected_vertex) ... ok
test_multiple_edges_are_retained_and_tie_break_is_stable (test_impact_graph.ImpactGraphTest.test_multiple_edges_are_retained_and_tie_break_is_stable) ... ok
test_reachable_node_can_become_a_change_root (test_impact_graph.ImpactGraphTest.test_reachable_node_can_become_a_change_root) ... ok
test_shortest_chain_switches_incrementally (test_impact_graph.ImpactGraphTest.test_shortest_chain_switches_incrementally) ... ok

----------------------------------------------------------------------
Ran 9 tests in 0.001s

OK
```
