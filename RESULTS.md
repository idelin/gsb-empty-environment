# Recorded test run

Date: 2026-09-25 08:30:22 CST
Python: Python 3.11.15

```
$ python3.11 -m unittest discover -v
test_all_tied_evidence_reported (tests.test_engine.TestBasicQueries.test_all_tied_evidence_reported) ... ok
test_crossing_distance_zero (tests.test_engine.TestBasicQueries.test_crossing_distance_zero) ... ok
test_delete_nearest_finds_next_candidate (tests.test_engine.TestBasicQueries.test_delete_nearest_finds_next_candidate) ... ok
test_shared_endpoint_not_duplicated (tests.test_engine.TestBasicQueries.test_shared_endpoint_not_duplicated) ... ok
test_zero_length_segment_as_point (tests.test_engine.TestBasicQueries.test_zero_length_segment_as_point) ... ok
test_empty_group_yields_empty_result (tests.test_engine.TestExclusionsAndRadius.test_empty_group_yields_empty_result) ... ok
test_exclusion_skips_pair (tests.test_engine.TestExclusionsAndRadius.test_exclusion_skips_pair) ... ok
test_intra_group_query_rejected (tests.test_engine.TestExclusionsAndRadius.test_intra_group_query_rejected) ... ok
test_radius_threshold (tests.test_engine.TestExclusionsAndRadius.test_radius_threshold) ... ok
test_all_mode_reports_interval (tests.test_engine.TestParallelModes.test_all_mode_reports_interval) ... ok
test_representative_mode_stable_point (tests.test_engine.TestParallelModes.test_representative_mode_stable_point) ... ok
test_bbox_dist2_exact (tests.test_geometry.TestBBox.test_bbox_dist2_exact) ... ok
test_bbox_overlap_zero (tests.test_geometry.TestBBox.test_bbox_overlap_zero) ... ok
test_collinear_overlap_distance_zero_interval (tests.test_geometry.TestClosestParams.test_collinear_overlap_distance_zero_interval) ... ok
test_crossing_at_rational_point (tests.test_geometry.TestClosestParams.test_crossing_at_rational_point) ... ok
test_crossing_segments_distance_zero_exact_params (tests.test_geometry.TestClosestParams.test_crossing_segments_distance_zero_exact_params) ... ok
test_endpoint_clamping (tests.test_geometry.TestClosestParams.test_endpoint_clamping) ... ok
test_minimizer_canonical_order (tests.test_geometry.TestClosestParams.test_minimizer_canonical_order) ... ok
test_parallel_antiparallel_orientation (tests.test_geometry.TestClosestParams.test_parallel_antiparallel_orientation) ... ok
test_parallel_equidistant_interval (tests.test_geometry.TestClosestParams.test_parallel_equidistant_interval) ... ok
test_parallel_equidistant_single_projection_point (tests.test_geometry.TestClosestParams.test_parallel_equidistant_single_projection_point) ... ok
test_rational_coordinates_exact (tests.test_geometry.TestClosestParams.test_rational_coordinates_exact) ... ok
test_shared_endpoint_touching_distance_zero (tests.test_geometry.TestClosestParams.test_shared_endpoint_touching_distance_zero) ... ok
test_two_zero_length_segments (tests.test_geometry.TestClosestParams.test_two_zero_length_segments) ... ok
test_zero_length_segment_is_point (tests.test_geometry.TestClosestParams.test_zero_length_segment_is_point) ... ok
test_deterministic_results_across_identical_streams (tests.test_interleaving.TestEventInterleaving.test_deterministic_results_across_identical_streams) ... ok
test_random_event_stream_matches_oracle (tests.test_interleaving.TestEventInterleaving.test_random_event_stream_matches_oracle) ... ok
test_avl_balance_under_sequential_inserts (tests.test_tree.TestLocalRepair.test_avl_balance_under_sequential_inserts) ... ok
test_move_only_rebuilds_path_nodes (tests.test_tree.TestLocalRepair.test_move_only_rebuilds_path_nodes) ... ok
test_old_version_unchanged_after_edits (tests.test_tree.TestLocalRepair.test_old_version_unchanged_after_edits) ... ok
test_live_count_matches_reachable (tests.test_tree.TestResourceAccounting.test_live_count_matches_reachable) ... ok
test_old_version_released_after_snapshot_close (tests.test_tree.TestResourceAccounting.test_old_version_released_after_snapshot_close) ... ok
test_rebalance_does_not_reclaim_inflight_nodes (tests.test_tree.TestResourceAccounting.test_rebalance_does_not_reclaim_inflight_nodes) ... ok
test_historical_snapshot_query (tests.test_tree.TestSnapshotQueries.test_historical_snapshot_query) ... ok

----------------------------------------------------------------------
Ran 34 tests in 41.083s

OK

interleaving stats: {'set': 179, 'del': 34, 'excl': 47, 'qall': 74, 'qpair': 41, 'snap': 62, 'cancel': 40, 'cancelled': 39}
```
