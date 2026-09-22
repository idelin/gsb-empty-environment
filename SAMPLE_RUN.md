# 运行样例输出

```

=== 1. LIMIT 1 不完整扫描 1,000,000 行（batch_size=1，逐行即停） ===
结果行: [(0, 0, 0)]
实际生成行数: 1（不是 1,000,000），耗时 0.02 ms
scan 已关闭: True | 生成器游标已关闭: True
批量读取（batch_size=64）时也最多只读第一批: 64 行，随后立即关闭上游

=== 2. 扫描 -> 过滤 -> 哈希连接 -> 分组聚合 -> 排序 Top-3（百万行） ===
   (20, 'cust_020', 5239, 248617)
   (41, 'cust_041', 5239, 248617)
   (62, 'cust_062', 5239, 248617)
输出行数: 3，输出批次数: 1
所有算子已关闭: True True True True True

=== 3. NULL 与多重集合语义（events 表） ===
   ('a', 4, 3, 5, 1.6666666666666667, 1, 3)
   ('b', 2, 2, 30, 15.0, 10, 20)
   (NULL, 2, 2, 15, 7.5, 7, 8)
  注：NULL 分组键显示为 NULL；COUNT(v) 忽略 NULL，SUM/AVG 同。

=== 3b. WHERE v > 5：NULL 行既不匹配也不报错（Unknown 不通过） ===
  通过行数: 4

=== 4. 空批次 != EOF：EmptyInjector 先吐空批再透传数据 ===
  收到空 Batch（bool(batch) = True ），但仍继续拉取
  收到空批次: True | 最终真实行数: 8

=== 5. 外部取消：消费若干批后 cancel，上游随即停止并释放 ===
  捕获 CancelledError: consumer lost interest
  取消前消费行数: 512 | scan 已关闭: True
  生成器实际产出行数: 512

=== 6. 下游出错 -> 关闭上游（百万行表不会继续被扫描） ===
  捕获 OperatorError: operator 'Exploder' failed: RuntimeError: boom from downstream operator
  原始异常: RuntimeError: boom from downstream operator
  scan 已关闭: True | 生成器已关闭: True
  实际产出行数: 128 （读了第一批即报错，远小于百万）

=== 6b. 重复 close 安全 ===
  三次额外 close 均无异常；release_count 只计一次: 1

=== 7. CSV 文件表：提前停止时文件句柄随游标释放 ===
  取到 5 行，首行: ('0', '0')
  CSV 游标已关闭（文件句柄已释放）: True

所有样例断言通过。
```

# 测试结果（python3.11 -m unittest discover -s tests -v）

```
test_batched_rows_bounded (test_batch.TestBatch.test_batched_rows_bounded) ... ok
test_consumer_idiom_distinguishes_empty_from_eof (test_batch.TestBatch.test_consumer_idiom_distinguishes_empty_from_eof) ... ok
test_empty_batch_is_truthy (test_batch.TestBatch.test_empty_batch_is_truthy) ... ok
test_eof_is_none (test_batch.TestBatch.test_eof_is_none) ... ok
test_arith_null_propagation (test_null.TestNull.test_arith_null_propagation) ... ok
test_bool_forbidden (test_null.TestNull.test_bool_forbidden) ... ok
test_comparison_propagates_null (test_null.TestNull.test_comparison_propagates_null) ... ok
test_is_distinct_from (test_null.TestNull.test_is_distinct_from) ... ok
test_null_groups_together_via_hash_identity (test_null.TestNull.test_null_groups_together_via_hash_identity) ... ok
test_singleton_and_repr (test_null.TestNull.test_singleton_and_repr) ... ok
test_truth_tables (test_null.TestNull.test_truth_tables) ... ok
test_where_predicate_rule (test_null.TestNull.test_where_predicate_rule) ... ok
test_global_aggregate_empty_input_emits_one_row (test_operators.TestAggregate.test_global_aggregate_empty_input_emits_one_row) ... ok
test_group_order_preserves_first_appearance (test_operators.TestAggregate.test_group_order_preserves_first_appearance) ... ok
test_group_semantics (test_operators.TestAggregate.test_group_semantics) ... ok
test_string_min_max (test_operators.TestAggregate.test_string_min_max) ... ok
test_injected_empty_batch_is_not_eof (test_operators.TestEmptyBatchSemantics.test_injected_empty_batch_is_not_eof) ... ok
test_nothing_is_immediate_eof (test_operators.TestEmptyBatchSemantics.test_nothing_is_immediate_eof) ... ok
test_filter_all_rejected_reaches_eof (test_operators.TestFilterProject.test_filter_all_rejected_reaches_eof) ... ok
test_filter_three_valued (test_operators.TestFilterProject.test_filter_three_valued) ... ok
test_predicate_error_wrapped (test_operators.TestFilterProject.test_predicate_error_wrapped) ... ok
test_project (test_operators.TestFilterProject.test_project) ... ok
test_inner_join_multiset (test_operators.TestHashJoin.test_inner_join_multiset) ... ok
test_join_empty_side (test_operators.TestHashJoin.test_join_empty_side) ... ok
test_join_releases_both_sides_under_limit (test_operators.TestHashJoin.test_join_releases_both_sides_under_limit) ... ok
test_null_keys_never_join (test_operators.TestHashJoin.test_null_keys_never_join) ... ok
test_sort_nulls_order (test_operators.TestSortUnion.test_sort_nulls_order) ... ok
test_sort_stable_multiset (test_operators.TestSortUnion.test_sort_stable_multiset) ... ok
test_union_all_keeps_duplicates (test_operators.TestSortUnion.test_union_all_keeps_duplicates) ... ok
test_union_schema_check (test_operators.TestSortUnion.test_union_schema_check) ... ok
test_cancel_during_aggregate_build (test_pipeline.TestCancellation.test_cancel_during_aggregate_build) ... ok
test_cancel_from_another_thread (test_pipeline.TestCancellation.test_cancel_from_another_thread) ... ok
test_cancel_from_consumer_thread (test_pipeline.TestCancellation.test_cancel_from_consumer_thread) ... ok
test_checkpoint_idempotent_message (test_pipeline.TestCancellation.test_checkpoint_idempotent_message) ... ok
test_csv_limit_closes_handle (test_pipeline.TestCsvResource.test_csv_limit_closes_handle) ... ok
test_batch_sizes_are_bounded (test_pipeline.TestEndToEnd.test_batch_sizes_are_bounded) ... ok
test_filter_project_join_limit_columns (test_pipeline.TestEndToEnd.test_filter_project_join_limit_columns) ... ok
test_full_pipeline_shape (test_pipeline.TestEndToEnd.test_full_pipeline_shape) ... ok
test_close_without_pull (test_pipeline.TestErrorAndClose.test_close_without_pull) ... ok
test_context_manager_early_break (test_pipeline.TestErrorAndClose.test_context_manager_early_break) ... ok
test_double_close_safe (test_pipeline.TestErrorAndClose.test_double_close_safe) ... ok
test_downstream_error_closes_upstream (test_pipeline.TestErrorAndClose.test_downstream_error_closes_upstream) ... ok
test_error_closes_all_branches (test_pipeline.TestErrorAndClose.test_error_closes_all_branches) ... ok
test_generator_gc_releases_resources (test_pipeline.TestErrorAndClose.test_generator_gc_releases_resources) ... ok
test_run_callback_exception_releases (test_pipeline.TestErrorAndClose.test_run_callback_exception_releases) ... ok
test_close_before_any_read (test_table.TestCursors.test_close_before_any_read) ... ok
test_csv_cursor_closes_file (test_table.TestCursors.test_csv_cursor_closes_file) ... ok
test_csv_header_mismatch (test_table.TestCursors.test_csv_header_mismatch) ... ok
test_generated_cursor_lazy_and_close_releases (test_table.TestCursors.test_generated_cursor_lazy_and_close_releases) ... ok
test_list_cursor_bounded_reads (test_table.TestCursors.test_list_cursor_bounded_reads) ... ok
test_schema_lookup (test_table.TestCursors.test_schema_lookup) ... ok
test_width_validation (test_table.TestCursors.test_width_validation) ... ok
test_limit0_never_pulls (test_table.TestLimitStopsScan.test_limit0_never_pulls) ... ok
test_limit1_never_exceeds_one_batch (test_table.TestLimitStopsScan.test_limit1_never_exceeds_one_batch) ... ok
test_limit1_on_million_rows_generates_one (test_table.TestLimitStopsScan.test_limit1_on_million_rows_generates_one) ... ok
test_limit_smaller_than_first_batch (test_table.TestLimitStopsScan.test_limit_smaller_than_first_batch) ... ok

----------------------------------------------------------------------
Ran 56 tests in 0.171s

OK
```
