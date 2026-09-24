# graphpatch — 版本化图的可逆补丁与三方重放

纯 Python 3.11 标准库实现（测试用 `unittest`）。对两个图版本做结构化 diff，
产出**可逆、可组合、可序列化**的补丁，并支持把补丁**三方重放**到被独立修改
过的基线上。

## 图模型

- 顶点、边都使用**字符串 ID**；允许**孤立点**与**多重边**（同一对端点间多条边，
  以边 ID 区分，重数有意义），允许自环。
- 顶点与边都带属性字典（JSON 可序列化值）与**左闭右开**时间区间 `[start, end)`：
  `Interval(1, 5).contains(1) == True`，`contains(5) == False`；
  `[0,1)` 与 `[1,2)` 不重叠。`Graph.active_at(t)` 按该语义取快照。
- 属性值比较是**类型敏感**的（`0 != False != 0.0`），避免 Python 的
  `0 == False` 把真实变化吞掉。

## 功能与对应模块

| 需求 | 入口 | 说明 |
| --- | --- | --- |
| 区分新增/删除/属性变化/区间变化 | `graphpatch.diff(base, target)` | 产出分类后的操作序列 |
| 显式重命名映射 + 冲突检测 | `diff(..., renames={...})` | `RenameConflictError.conflicts` 给出结构化冲突（源缺失、目标缺失、目标撞名、重复目标、链式重命名、平凡重命名） |
| 重命名不引发边增删 | `RenameVertex` 操作 | 边端点随重命名自动跟随；纯重命名的 diff 只含 `RenameVertex`，**不会**把边报成删除+新增 |
| 可逆补丁 | `Patch.inverse()` | 每个操作都带 before-image，求逆逐操作反转 |
| 前置版本校验 | `Patch.check_base` / `apply` | 弱摘要（crc32，可注入）只做快速预筛，**内容哈希（sha256）才是裁决**；摘要碰撞时按内容复核，拒绝错误基线 |
| 应用后等于目标图 | `Patch.apply` | 校验目标哈希；`apply(diff(g1,g2), g1) == g2` |
| 错误基线拒绝且原图不变 | `Patch.apply` | 函数式应用：先校验、在副本上执行，失败抛 `PreconditionError`，入参图绝不被修改 |
| 三方重放/重定位 | `graphpatch.replay(patch, alt_base)` | 基线的非冲突修改保留并重定位；冲突以结构化 `Conflict` 返回 |
| 重命名 vs 删除 | `Conflict(kind="rename_vs_delete")` | 结构化冲突 |
| 同名不同身份 | `Conflict(kind="same_name_different_identity")` | 结构化冲突 |
| 组合保留身份/重数/属性来源 | `graphpatch.compose(p1, p2, base)` | 实体按身份跟踪；属性 delta 保留最早 before 与最新 after；边按 ID 跟踪，重数不塌缩 |
| 求逆保留身份/重数/属性来源 | `Patch.inverse()` | 随机图测试验证 `apply(inverse(p), apply(p, g)) == g` |

## 快速开始

```python
from graphpatch import Graph, Vertex, Edge, Interval, diff, compose, replay

g1 = Graph()
g1.add_vertex(Vertex("alice", {"role": "admin"}, Interval(0.0, 10.0)))
g1.add_vertex(Vertex("bob"))
g1.add_edge(Edge("e1", "alice", "bob"))
g1.add_edge(Edge("e2", "alice", "bob"))   # 多重边

g2 = g1.copy()
v = g2.vertices.pop("alice")
g2.vertices["alex"] = Vertex("alex", dict(v.attrs), v.interval)
for eid, e in list(g2.edges.items()):
    g2.edges[eid] = Edge(eid,
                         "alex" if e.src == "alice" else e.src,
                         "alex" if e.dst == "alice" else e.dst,
                         dict(e.attrs), e.interval)

patch = diff(g1, g2, renames={"alice": "alex"})
assert [type(op).__name__ for op in patch.ops] == ["RenameVertex"]  # 无边增删
assert patch.apply(g1) == g2                 # 应用后等于目标图
assert patch.inverse().apply(g2) == g1       # 求逆恢复
text = patch.to_json()                       # 可序列化补丁
```

三方重放：

```python
result = replay(patch, alt_base)   # alt_base 是被独立修改过的基线
if result.ok:
    relocated = result.patch.apply(alt_base)
else:
    for c in result.conflicts:     # 结构化冲突
        print(c.kind, c.entity, c.message)
```

## 运行样例

`python3.11 sample.py` 演示 diff → 可逆应用 → JSON 序列化 → 错误基线拒绝 →
摘要碰撞复核 → 三方重放（重定位与结构化冲突）→ 组合 → 半开区间。节选输出：

```
1. diff(v1, v2) with explicit rename map {alice: alex}
   RenameVertex(old='alice', new='alex')
   RemoveEdge(eid='e2', ...)
   ChangeVertexAttrs(vid='bob', ...)
   note: renaming alice did NOT delete+re-add her edges

2. apply(patch, v1) == v2, and the patch is reversible
   apply(patch, v1) == v2 : True
   apply(inverse, v2) == v1: True

4. baseline verification: wrong base is rejected, graph untouched
   rejected: base digest mismatch: patch was built for a different graph
   wrong baseline left unchanged: True

5. digest collision: weak digest matches, content does not
   weak digests collide: True
   rejected despite collision: base digest matched but content differs
   (digest collision): refusing to apply

7. structured conflicts: rename-vs-delete
   [rename_vs_delete] patch renames 'alice' -> 'alex' but the baseline deleted 'alice'

8. structured conflicts: same name, different identity
   [same_name_different_identity] patch renames 'alice' -> 'alex' but the
   baseline already has a different vertex with that name

9. composition: v1 -> v2 -> v3 collapses to one patch
   apply(composed, v1) == v3: True
   inverse recovers v1: True

10. half-open intervals [start, end)
   Interval(start=1.0, end=5.0) contains 1.0: True, contains 5.0: False
```

## 测试

```
$ python3.11 -m unittest discover -s tests
.......................................
----------------------------------------------------------------------
Ran 39 tests in ~1s

OK
```

覆盖要点：

- **随机图验证**（固定种子）：组合等价 `compose(p12,p23).apply(g1) == g3`、
  逆向恢复 `apply(inverse(p), apply(p, g)) == g`、重放等价、JSON 往返。
- **摘要碰撞**：注入恒碰撞的弱摘要函数，错误基线仍被内容复核拒绝；
  真基线正常通过。
- **纯重命名**：随机图（含自环、多重边）上纯重命名的 diff 只含
  `RenameVertex`，无边增删、无端点变更。
- **身份/重数/属性来源**：组合与求逆保留顶点身份（删后再加同名=修改）、
  平行边重数、属性 delta 的最早 before 值。
- **区间语义**：左闭右开边界、相邻区间不重叠、`active_at` 快照。

## 目录结构

```
graphpatch/
  model.py    # Interval / Vertex / Edge / Graph，规范化序列化与摘要
  ops.py      # 操作类型 + AttrDelta（属性来源），MISSING 哨兵
  patch.py    # Patch：应用、求逆、组合、基线校验、JSON 序列化
  diff.py     # 结构化 diff + 重命名映射冲突检测
  rebase.py   # 三方重放：重定位 + 结构化冲突
tests/        # unittest 测试（随机图 + 定向用例）
sample.py     # 端到端演示
```
