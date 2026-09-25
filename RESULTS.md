# 运行结果

## 测试（python3.11 -m unittest discover -v）

```
test_inconsistent_calibration_makes_query_infeasible (tests.test_clockmap.ConflictTests.test_inconsistent_calibration_makes_query_infeasible) ... ok
test_rollback_flagged_and_minimal_set (tests.test_clockmap.ConflictTests.test_rollback_flagged_and_minimal_set) ... ok
test_self_inconsistent_anchor_is_singleton_conflict (tests.test_clockmap.ConflictTests.test_self_inconsistent_anchor_is_singleton_conflict) ... ok
test_three_anchors_one_outlier (tests.test_clockmap.ConflictTests.test_three_anchors_one_outlier) ... ok
test_too_fast_flagged (tests.test_clockmap.ConflictTests.test_too_fast_flagged) ... ok
test_duration_uses_anchor_tightened_bounds (tests.test_clockmap.DroppedSampleTests.test_duration_uses_anchor_tightened_bounds) ... ok
test_seq_jump_of_10_keeps_time_interval (tests.test_clockmap.DroppedSampleTests.test_seq_jump_of_10_keeps_time_interval) ... ok
test_duration_across_epochs_rejected (tests.test_clockmap.EpochTests.test_duration_across_epochs_rejected) ... ok
test_reset_epoch_does_not_inherit_old_sequence (tests.test_clockmap.EpochTests.test_reset_epoch_does_not_inherit_old_sequence) ... ok
test_anchor_error_intervals_widen_result (tests.test_clockmap.FeasibleIntervalTests.test_anchor_error_intervals_widen_result) ... ok
test_single_anchor_backward (tests.test_clockmap.FeasibleIntervalTests.test_single_anchor_backward) ... ok
test_single_anchor_forward (tests.test_clockmap.FeasibleIntervalTests.test_single_anchor_forward) ... ok
test_two_anchors_intersect (tests.test_clockmap.FeasibleIntervalTests.test_two_anchors_intersect) ... ok
test_unknown_epoch_raises (tests.test_clockmap.FeasibleIntervalTests.test_unknown_epoch_raises) ... ok
test_late_anchor_revises_mapping_and_keeps_evidence (tests.test_clockmap.LateCalibrationTests.test_late_anchor_revises_mapping_and_keeps_evidence) ... ok
test_repeat_query_without_new_anchor_returns_same_estimate (tests.test_clockmap.LateCalibrationTests.test_repeat_query_without_new_anchor_returns_same_estimate) ... ok
test_exact_conversion_between_control_points (tests.test_clockmap.OffsetTableTests.test_exact_conversion_between_control_points) ... ok
test_from_mapper_and_uncertainty (tests.test_clockmap.OffsetTableTests.test_from_mapper_and_uncertainty) ... ok
test_offset_table_never_uses_float (tests.test_clockmap.OffsetTableTests.test_offset_table_never_uses_float) ... ok
test_round_trip_is_exact (tests.test_clockmap.OffsetTableTests.test_round_trip_is_exact) ... ok
test_float_rejected_everywhere (tests.test_clockmap.TypeSafetyTests.test_float_rejected_everywhere) ... ok
test_huge_neighbouring_timestamps_not_swallowed (tests.test_clockmap.TypeSafetyTests.test_huge_neighbouring_timestamps_not_swallowed) ... ok
test_wall_time_minus_wall_time_is_duration (tests.test_clockmap.TypeSafetyTests.test_wall_time_minus_wall_time_is_duration) ... ok
test_wall_time_plus_duration_commutes (tests.test_clockmap.TypeSafetyTests.test_wall_time_plus_duration_commutes) ... ok
test_wall_time_plus_wall_time_rejected (tests.test_clockmap.TypeSafetyTests.test_wall_time_plus_wall_time_rejected) ... ok

----------------------------------------------------------------------
Ran 25 tests in 0.001s

OK
```

## 样例（python3.11 examples/demo.py）

```
slope bounds: 9999/10000000 .. 10001/10000000 s/sample (nominal 1/1000)

=== 1. Calibration anchors with error intervals (epoch 0) ===
added cal-A: seq 1000 -> wall [340000000199/200, 340000000201/200]
added cal-B: seq 5000 -> wall [340000000999/200, 340000001001/200]

=== 2. Feasible wall-clock interval for seq 3000 (piecewise, not uniform) ===
feasible interval: [4250000007487/2500, 4250000007513/2500]
width: 13/1250 s  (evidence: ('cal-A', 'cal-B'))

=== 3. Dropped samples: seq jumps 2000 -> 2010 ===
9 samples dropped, but 10 periods elapse: duration in [9999/1000000, 10001/1000000] s

=== 4. Late calibration locally revises the mapping ===
revised interval: [170000000299799/100000, 170000000300201/100000] (version 2)
previous estimate superseded: True; kept as evidence chain: v1 -> v2

=== 5. Epoch reset cannot be bridged ===
refused: cannot compute a duration across epochs 0 and 1
epoch 1 seq 10 maps to [1700000100, 1700000100] using only epoch-1 anchors

=== 6. Contradictory calibration -> anomaly + minimal conflict set ===
conflict: cal-A vs cal-bad: wall clock moved backwards beyond slope bounds (rollback)
conflict: cal-late vs cal-bad: wall clock moved backwards beyond slope bounds (rollback)
conflict: cal-bad vs cal-B: same sequence number, disjoint wall intervals
minimal conflict set: ['cal-A', 'cal-bad']
cal-bad flagged anomalous: True

=== 7. Synthetic offset conversion table (exact, epoch 0 before cal-bad) ===
seq 3000 -> wall [340000000599/200, 340000000601/200]
wall 340000000599/200 -> seq in [2990, 3000]

=== 8. Exact arithmetic: huge neighbouring timestamps ===
t2 - t1 = 1/1073741824 s exactly; as float64 they are equal: True
```
