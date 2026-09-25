# mesh-topology

从三角面列表建立半边结构，做拓扑诊断、生成局部修复补丁（不自动应用），
并支持事务化编辑。纯 Python 3.11 标准库，测试用 `unittest`。

## 功能

- **半边构建**：有向半边 `(u,v) -> [(face, slot)]`，孪生关系由反向键查表得到；
  无向边 `edge -> [face]`；边界环与顶点扇（vertex fan）连通性随编辑增量维护。
- **诊断**（`diagnose(mesh)`，全部精确判定，无 epsilon）：
  - 边界边：仅 1 个非退化面 incident 的边；
  - 非流形边：≥3 个面共边（一边三面必报）；
  - 方向冲突：两个非退化、非重复面同向遍历公共边；
  - 重复面：顶点集相同的面单独成组报告；重复面共享的边记为
    “重复邻接”，**不计入**正常二面邻接；
  - 退化面（索引重复，如 `(8,8,9)`）与零面积面（精确叉积为零）
    分别报告；退化面不参与任何拓扑结构；
  - 孤立顶点：不 incident 于任何非退化面的顶点，单独报告；
  - 蝴蝶结顶点：顶点扇连通分量 > 1。“每边至多两面”不足以判断顶点
    流形——两个四面体只共享一个顶点时所有边都 ≤2 面，但顶点非流形。
- **修复建议**（`suggest_repairs(mesh)`）：每条建议是独立可执行的
  `Patch`（前置条件 + 操作序列），**不自动应用**；`patch.apply(mesh)`
  在事务内先校验前置条件，失败即回滚，几何坐标一律不改。
- **事务编辑**（`Transaction`）：`insert_face` / `delete_face` /
  `flip_face` / `split_edge` / `split_vertex`。进入事务即快照；体内容
  抛异常或提交时一致性校验失败，都恢复快照——失败必须保留原拓扑，
  旧快照对象不被改动。
- **随机编辑对照全量重建**（`random_edits`）：每步编辑后用
  `verify_consistency()` 把增量维护的半边表、边-面表、边界环缓存、
  顶点扇缓存与从面列表全量重建的结果逐一比对。
- **精确几何**：坐标只接受 `int` / `Fraction`；零面积判定为精确叉积
  模长平方 == 0，拓扑判断不依赖任何 epsilon。

## 运行

```bash
python3.11 examples/run_sample.py   # 运行样例
python3.11 -m unittest discover -s tests -v
```

## 样例输出（节选）

```
=== diagnostics ===
non-manifold edges: 1
  - ((0, 1), 3, (1, 2, 5))
orientation conflicts: 2
duplicate face groups: 1
  - (11, 12)
degenerate faces (repeated index): 1
  - (10, (8, 8, 9))
zero-area faces (exact): 1
  - 13
isolated vertices: 3
  - 8
  - 9
  - 99
bowtie vertices: 1
  - (0, 2)

=== repair suggestions (not applied automatically) ===
[R1] flip face 1 to fix orientation conflict on edge (0, 2)
  preconditions:
    - faces 3 and 1 exist
    - edge (0, 2) is shared by exactly faces (1, 3)
    - both faces still traverse edge (0, 2) same way
  operations:
    - flip_face(1,)
...
[R9] split bowtie vertex 0: move 3 face(s) to new vertex ('split', 0, 1)

=== transactional edit with rollback ===
transaction failed as expected: face 12345 does not exist
topology preserved after failure: True

=== random edits vs full rebuild ===
300 random edits, incremental state matches full rebuild: OK
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests
..............................
----------------------------------------------------------------------
Ran 30 tests in 1.494s

OK
```

覆盖：边界/闭环检测、半边孪生引用、一边三面非流形、方向冲突及翻转
补丁、重复面（含反向重复）不算正常邻接、退化面与孤立顶点分别报告、
蝴蝶结顶点（证明边检查不充分）、精确有理数几何、事务失败回滚与快照
保持、边拆分（边界边/内部边/精确中点）、300 步随机编辑对照全量重建。

## 代码结构

```
mesh_topology/
  geometry.py      精确有理数几何，零面积判定
  mesh.py          HalfEdgeMesh：增量维护 + 快照 + 全量重建对照
  diagnostics.py   诊断与定义
  repairs.py       Patch / 前置条件 / suggest_repairs
  transactions.py  事务（失败回滚）
  fuzz.py          随机编辑驱动
examples/run_sample.py
tests/test_mesh_topology.py
```
