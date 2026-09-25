# 实际运行结果

环境：Python 3.11.15（macOS），命令 `python3.11 -m unittest discover -s tests -v`。

```
test_insufficient_budget_returns_resumable_cursor (test_arcsample.BudgetCursorTest.test_insufficient_budget_returns_resumable_cursor)
预算不足返回可续游标而非静默减点。 ... ok
test_stale_cursor_rejected (test_arcsample.BudgetCursorTest.test_stale_cursor_rejected) ... ok
test_closed_square_fixed_start (test_arcsample.ClosedPathTest.test_closed_square_fixed_start)
闭合路径有固定起点；接缝处不重复采样。 ... ok
test_breakpoint_no_cross_interpolation (test_arcsample.DegenerateTest.test_breakpoint_no_cross_interpolation)
断点两侧独立采样，里程各自从 0 开始，绝不跨断点插值。 ... ok
test_invalid_spacing_and_budget (test_arcsample.DegenerateTest.test_invalid_spacing_and_budget) ... ok
test_single_point_and_all_zero_path (test_arcsample.DegenerateTest.test_single_point_and_all_zero_path) ... ok
test_zero_length_segment_no_cross_interpolation (test_arcsample.DegenerateTest.test_zero_length_segment_no_cross_interpolation)
零长段不产生跨越插值：样本只落在非退化段内部或顶点上。 ... ok
test_keypoint_at_non_grid_mileage_is_kept (test_arcsample.FixedGridTest.test_keypoint_at_non_grid_mileage_is_kept) ... ok
test_length10_spacing3 (test_arcsample.FixedGridTest.test_length10_spacing3)
长度10间距3 -> 里程 0、3、6、9、10。 ... ok
test_grid_ids_stable_across_edit (test_arcsample.LocalRepairTest.test_grid_ids_stable_across_edit)
后续样本 ID 依据原始锚点（间距网格）稳定映射。 ... ok
test_local_repair_only_touches_affected_range (test_arcsample.LocalRepairTest.test_local_repair_only_touches_affected_range)
只修复受影响弧长范围；范围外样本保持原样且 ID 稳定。 ... ok
test_move_point_patch_add_move_undo (test_arcsample.LocalRepairTest.test_move_point_patch_add_move_undo)
移动中点：校验新增、移动补丁，撤销后完全还原。 ... ok
test_cross_segment_accumulation_is_exact (test_arcsample.RationalMileageTest.test_cross_segment_accumulation_is_exact)
跨段累计用有理里程：1/3 + 1/3 + 1/3 恰好等于 1，无浮点误差。 ... ok
test_nonrational_length_raises (test_arcsample.RationalMileageTest.test_nonrational_length_raises)
退化定义：长度为 sqrt(2) 的段不属于有理里程域，明确报错而非舍入。 ... ok
test_degenerate_shared_edge (test_arcsample.SeamTest.test_degenerate_shared_edge) ... ok
test_shared_edge_identical_across_polylines (test_arcsample.SeamTest.test_shared_edge_identical_across_polylines)
两条折线共享一条边（方向相反），边上采样逐点一致。 ... ok
test_shared_edge_reverse_traversal (test_arcsample.SeamTest.test_shared_edge_reverse_traversal)
共享边反向遍历产生逆序同点；只计算一次，不各自舍入。 ... ok

----------------------------------------------------------------------
Ran 17 tests in 0.022s

OK
```
