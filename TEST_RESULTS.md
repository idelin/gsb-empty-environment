# Test Results

```
$ python3 -m unittest discover -s tests -v
test_chunk_invalidation_only_touches_related_buckets (test_deltas.TestDeltas.test_chunk_invalidation_only_touches_related_buckets) ... ok
test_delta_application_is_idempotent (test_deltas.TestDeltas.test_delta_application_is_idempotent) ... ok
test_repeated_delta_after_crash_not_applied_twice (test_deltas.TestDeltas.test_repeated_delta_after_crash_not_applied_twice) ... ok
test_retract_delta_restores_entries (test_deltas.TestDeltas.test_retract_delta_restores_entries) ... ok
test_subexpression_cache_bound_to_versions (test_deltas.TestDeltas.test_subexpression_cache_bound_to_versions) ... ok
test_broadcast_outer_product (test_executor.TestExecutor.test_broadcast_outer_product) ... ok
test_budget_abort_marks_incomplete (test_executor.TestExecutor.test_budget_abort_marks_incomplete) ... ok
test_checkpoint_invalidated_when_inputs_change (test_executor.TestExecutor.test_checkpoint_invalidated_when_inputs_change) ... ok
test_checkpoint_resume_after_interruption (test_executor.TestExecutor.test_checkpoint_resume_after_interruption) ... ok
test_huge_dims_without_dense_expansion (test_executor.TestExecutor.test_huge_dims_without_dense_expansion) ... ok
test_matches_oracle_matmul (test_executor.TestExecutor.test_matches_oracle_matmul) ... ok
test_repeated_label_diagonal (test_executor.TestExecutor.test_repeated_label_diagonal) ... ok
test_spill_to_disk_preserves_result (test_executor.TestExecutor.test_spill_to_disk_preserves_result) ... ok
test_executor_matches_dense_oracle (test_oracle.TestOracle.test_executor_matches_dense_oracle) ... ok
test_all_orders_agree_with_oracle (test_planner.TestPlanner.test_all_orders_agree_with_oracle) ... ok
test_axis_mapping_and_stats (test_planner.TestPlanner.test_axis_mapping_and_stats) ... ok
test_planner_picks_smaller_intermediate (test_planner.TestPlanner.test_planner_picks_smaller_intermediate) ... ok
test_three_inputs_have_multiple_orders (test_planner.TestPlanner.test_three_inputs_have_multiple_orders) ... ok
test_replay_is_deterministic (test_replay.TestReplay.test_replay_is_deterministic) ... ok
test_replay_matches_golden_log (test_replay.TestReplay.test_replay_matches_golden_log) ... ok
test_duplicate_output_label (test_validation.TestValidation.test_duplicate_output_label) ... ok
test_empty_axis_gives_empty_result (test_validation.TestValidation.test_empty_axis_gives_empty_result) ... ok
test_illegal_dimensions_rejected_first (test_validation.TestValidation.test_illegal_dimensions_rejected_first) ... ok
test_illegal_label (test_validation.TestValidation.test_illegal_label) ... ok
test_inconsistent_axis_sizes_across_inputs (test_validation.TestValidation.test_inconsistent_axis_sizes_across_inputs) ... ok
test_off_diagonal_entry_rejected (test_validation.TestValidation.test_off_diagonal_entry_rejected) ... ok
test_out_of_bounds_coordinate (test_validation.TestValidation.test_out_of_bounds_coordinate) ... ok
test_rank_mismatch (test_validation.TestValidation.test_rank_mismatch) ... ok
test_same_coordinate_contributions_cancel (test_validation.TestValidation.test_same_coordinate_contributions_cancel) ... ok
test_scalar_result (test_validation.TestValidation.test_scalar_result) ... ok
test_scalar_tensor (test_validation.TestValidation.test_scalar_tensor) ... ok
test_unknown_output_label (test_validation.TestValidation.test_unknown_output_label) ... ok

Ran 32 tests in 6.285s

OK
```

Replayable sample (`python3 -m samples.replay`):

```
plan0: est_total=10.000 first_contract=('j',) est_first=4.000
plan1: est_total=12.000 first_contract=('k',) est_first=6.000
plan2: est_total=18.000 first_contract=() est_first=12.000
chosen: est_total=10.000 axis_map=('i', 'l')->(3, 3)
run1: completed=True entries=(0, 0):19 (0, 2):38 (1, 1):54 (2, 1):96
run1: chunks=4 spilled=0 actual_intermediate=7
delta d1: cache_hits=1 cache_misses=3 entries=(0, 0):19 (0, 1):38 (0, 2):38 (1, 1):54 (2, 1):96
retract d1: restored=True
crash: completed=False reason=interrupted
delta d2 reapply: False version=2
resume: completed=True entries=(0, 0):19 (0, 2):38 (1, 1):54 (2, 0):4 (2, 1):96 (2, 2):8
spill: completed=True spilled_bytes=230
budget: completed=False reason=budget-exceeded
```
