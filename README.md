# arclen — 多段折线与共享接缝的精确等弧长采样

纯标准库（Python 3.11+），全部计算基于 `fractions.Fraction` 精确有理数，
不使用任何 epsilon 修补。

## 核心契约

- **坐标**：只接受 `int` / `Fraction`；传入 `float` 直接 `TypeError`，杜绝静默舍入。
- **有理里程**：段长必须是有理数（平方长度为有理数平方，如 3-4-5、轴向段）。
  无理段长（如单位对角线）抛出 `NonRationalLengthError` —— 这是明确定义的行为，
  不是误差兜底。全局里程 = 各 run 长度的精确 Fraction 累加；run 内跨段余量精确结转。
- **锚点与 run**：路径起终点、`key=True` 顶点、断点两侧、共享边端点都是锚点。
  锚点把路径切成 run；每个 run 内部从 0 起按间距等弧长布点，锚点本身作为
  `kind="key"` 样本始终保留（保留关键点）。
- **样本内容**：`id`、`mileage`（精确里程）、`point`（精确坐标）、`kind`、
  `seg` + `t`（来源段与精确参数）、`direction`（未归一化的精确方向向量）。
- **稳定样本 ID**：`K{顶点}`（锚点）、`G{锚点}.{序号}`（网格点）、
  `E{边号}.{规范序号}`（共享边）。ID 与绝对里程解耦，局部修改后未受影响 run 的
  样本 ID 与坐标逐位不变。
- **局部补丁**：`move_vertex(i, p)` 只重采样受影响 run，返回
  `Patch(removed, added, mileage_delta, insert_at, affected_range)`；
  下游 run 不做重采样，仅施加常量里程平移。`undo(patch)` 精确还原（LIFO）。
  独立全量采样可核验局部更新结果逐位一致。
- **预算游标**：`sample(budget=...)` 预算耗尽时返回可续 `Cursor`
  （run 位置 + 偏移 + 版本），绝不静默减点；编辑后使用旧游标抛
  `StaleCursorError`。
- **共享接缝**：`SeamRegistry` 以端点的精确元组序确定规范方向，网格只计算一次
  并缓存；反向遍历取规范点的逆序（`t -> 1-t`、方向取反），多条折线对同一条边
  的采样逐位一致，不存在各自舍入。
- **闭合路径**：固定从顶点 0 出发，终点不重复发起点。

## 退化情形定义

| 情形 | 行为 |
| --- | --- |
| 空路径 | 0 个样本，总长 0 |
| 单顶点 | 1 个 `K0` 样本，里程 0，`seg/direction` 为 None |
| 零长段 | 不产生插值、不拥有样本；落在其里程上的网格点归属下一非零段（`t=0`） |
| 断点 | 零里程；两侧各自成 run、各自发锚点样本；无跨断点插值 |
| 网格点落在段边界 | 归属下一段（`t=0`）；run 末端边界由锚点 key 样本覆盖 |
| 间距 > 总长 | 仅首尾锚点样本 |
| 间距 ≤ 0 / 非有理数 | `ValueError` / `TypeError` |
| 无理段长（构造或移动后） | `NonRationalLengthError`，采样器状态不变 |

## 运行测试

```bash
python3.11 -m unittest discover -s tests -v
```

## 实际运行结果

```
test_budget_returns_resumable_cursor (test_arclen.TestBudgetCursor.test_budget_returns_resumable_cursor) ... ok
test_cursor_invalidated_by_edit (test_arclen.TestBudgetCursor.test_cursor_invalidated_by_edit) ... ok
test_generous_budget_completes (test_arclen.TestBudgetCursor.test_generous_budget_completes) ... ok
test_fixed_start_and_no_duplicate (test_arclen.TestClosedPath.test_fixed_start_and_no_duplicate) ... ok
test_all_zero_length_segments (test_arclen.TestDegenerateCases.test_all_zero_length_segments) ... ok
test_empty_polyline (test_arclen.TestDegenerateCases.test_empty_polyline) ... ok
test_invalid_inputs_are_rejected (test_arclen.TestDegenerateCases.test_invalid_inputs_are_rejected) ... ok
test_irrational_length_after_move_is_a_defined_error (test_arclen.TestDegenerateCases.test_irrational_length_after_move_is_a_defined_error) ... ok
test_irrational_length_is_a_defined_error (test_arclen.TestDegenerateCases.test_irrational_length_is_a_defined_error) ... ok
test_single_vertex (test_arclen.TestDegenerateCases.test_single_vertex) ... ok
test_spacing_larger_than_length (test_arclen.TestDegenerateCases.test_spacing_larger_than_length) ... ok
test_3d_points (test_arclen.TestEqualArcLengthGrid.test_3d_points) ... ok
test_cross_segment_carry_is_rational (test_arclen.TestEqualArcLengthGrid.test_cross_segment_carry_is_rational) ... ok
test_fractional_spacing_is_exact (test_arclen.TestEqualArcLengthGrid.test_fractional_spacing_is_exact) ... ok
test_length10_spacing3 (test_arclen.TestEqualArcLengthGrid.test_length10_spacing3) ... ok
test_move_key_vertex_repairs_only_adjacent_runs (test_arclen.TestKeyVertexLocality.test_move_key_vertex_repairs_only_adjacent_runs) ... ok
test_initial_grid (test_arclen.TestMoveVertexPatch.test_initial_grid) ... ok
test_local_update_matches_full_resampling (test_arclen.TestMoveVertexPatch.test_local_update_matches_full_resampling) ... ok
test_move_midpoint_produces_additions (test_arclen.TestMoveVertexPatch.test_move_midpoint_produces_additions) ... ok
test_undo_requires_lifo_order (test_arclen.TestMoveVertexPatch.test_undo_requires_lifo_order) ... ok
test_undo_restores_exact_state (test_arclen.TestMoveVertexPatch.test_undo_restores_exact_state) ... ok
test_edge_is_computed_once_for_all_polylines (test_arclen.TestSharedEdges.test_edge_is_computed_once_for_all_polylines) ... ok
test_reverse_traversal_gives_reversed_identical_points (test_arclen.TestSharedEdges.test_reverse_traversal_gives_reversed_identical_points) ... ok
test_shared_edge_requires_registry (test_arclen.TestSharedEdges.test_shared_edge_requires_registry) ... ok
test_no_cross_interpolation (test_arclen.TestZeroLengthAndBreakpoints.test_no_cross_interpolation) ... ok

----------------------------------------------------------------------
Ran 25 tests in 0.005s

OK
```
