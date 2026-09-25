# mesh_topology：半边网格拓扑诊断与事务化编辑

纯 Python 3.11 标准库实现（仅用 `fractions`、`dataclasses`、`contextlib`、`unittest`）。
坐标只接受 `int` 或 `Fraction`（精确有理数），所有拓扑/退化判定均为精确算术，
**不使用任何 epsilon 修补**；传入 `float` 会直接抛 `TypeError`。

## 功能

- **半边构建**：从三角面列表建立半边关系，双向引用（twin/next/prev）；
  仅当两侧半边各自唯一时才互认 twin，重复面/非流形情形标记为歧义。
- **拓扑诊断**（`analyze(mesh)`，各缺陷分类独立报告）：
  - 边界边与边界环（含环走向不唯一的歧义边界顶点）；
  - 非流形边：一条边连接 ≥3 个面即标记（一边三面必报）；
  - 面方向冲突：两个相邻面在共享边上同向；
  - 重复面：顶点集相同即为一组，**不计入正常相邻**（其共享边按非流形上报）；
  - 退化三角形：索引重复（组合退化）与零面积（精确叉积）分别标注；
  - 孤立顶点：单独分类报告；
  - 蝴蝶结顶点：顶点面扇经共享边的连通组件 >1，即非流形顶点
    （每边至多两面**不足以**判定顶点流形）。
- **修复建议**（`suggest_repairs(mesh)`）：生成独立可执行的 `RepairPatch`，
  每个补丁带 `preconditions(mesh)` 前置条件检查；**绝不自动应用、不改几何**。
  补丁在事务内执行，前置条件不满足时抛错且网格不变。
- **事务编辑**（`mesh.transaction()`）：面插入/删除、边拆分（精确中点）；
  增量维护半边映射、边界环与顶点扇；块内任何异常整体回滚，
  **失败必须保留原拓扑与旧快照**。
- **全量重建对照**：`mesh.check_consistency()` 把增量维护的映射与
  从面列表全量重建的结果逐项对照；随机编辑模糊测试逐步执行该对照。

## 运行

```bash
python3 demo.py                          # 运行样例
python3 -m unittest discover -s tests    # 测试（31 个用例）
python3 -m unittest discover -s tests -v # 详细输出
```

## 用法示例

```python
from mesh_topology import Mesh, analyze, suggest_repairs

mesh = Mesh([(0,0), (1,0), (0,1), (-1,0), (0,-1)],
            [(0,1,2), (0,3,4)])          # 两个三角形仅共享顶点 0
diag = analyze(mesh)
print(diag.bowtie_vertices)              # [(0, ((0,), (1,)))] 蝴蝶结顶点

for patch in suggest_repairs(mesh):      # 只生成建议，网格不变
    if not patch.preconditions(mesh):    # 前置条件全部满足
        patch.apply(mesh)                # 由用户显式应用

with mesh.transaction() as tx:           # 事务编辑
    m = tx.split_edge(0, 1)              # 精确中点 Fraction(1,2)
    tx.insert_face(0, 1, 3)
    tx.delete_face(2)
    # 块内抛异常 => 整体回滚，原拓扑与快照完整保留
```

## 测试结果

```
$ python3 -m unittest discover -s tests
...............................
----------------------------------------------------------------------
Ran 31 tests in ~2s

OK
```

覆盖：边界环追踪、半边 twin 链接、方向冲突、一边三面非流形、重复面、
退化/孤立分类、蝴蝶结顶点（含双四面体共顶点）、精确零面积判定
（`Fraction(1, 10**12)` 的近 miss 不算退化）、float 坐标拒绝、
事务提交/回滚、补丁前置条件拦截，以及两个随机模糊测试
（400 步随机编辑逐步对照全量重建；120 轮随机事务注入失败验证快照保留）。

## 文件结构

- `mesh_topology/mesh.py` — 核心 `Mesh`：半边结构、事务、增量映射维护
- `mesh_topology/diagnostics.py` — `analyze` 与 `Diagnostics` 分类报告
- `mesh_topology/repair.py` — `RepairPatch` 与 `suggest_repairs`
- `demo.py` — 运行样例（诊断 → 建议 → 手动应用 → 事务 → 回滚）
- `tests/test_mesh_topology.py` — 31 个 unittest 用例
