# 测试结果

命令：`python3 -m unittest discover -s tests -v`（Python 3.13，兼容 3.11+）

```
test_query_matches_time_expanded_brute_force (test_transit.BruteForceConsistencyTest.test_query_matches_time_expanded_brute_force) ... ok
test_reconstructed_journeys_hit_frontier_labels (test_transit.BruteForceConsistencyTest.test_reconstructed_journeys_hit_frontier_labels) ... ok
test_cancel_mid_journey_trip_requires_replan (test_transit.EvidenceInvalidationTest.test_cancel_mid_journey_trip_requires_replan) ... ok
test_update_trip_invalidates_evidence (test_transit.EvidenceInvalidationTest.test_update_trip_invalidates_evidence) ... ok
test_graph_isolated_vertices_and_parallel_edges (test_transit.ParetoQueryTest.test_graph_isolated_vertices_and_parallel_edges) ... ok
test_overnight_trip (test_transit.ParetoQueryTest.test_overnight_trip) ... ok
test_tied_journeys_same_label_both_returned (test_transit.ParetoQueryTest.test_tied_journeys_same_label_both_returned) ... ok
test_two_non_dominated_routes (test_transit.ParetoQueryTest.test_two_non_dominated_routes) ... ok
test_replay_transcript (test_transit.ReplaySampleTest.test_replay_transcript) ... ok
test_expired_token_after_restart (test_transit.ReservationTest.test_expired_token_after_restart) ... ok
test_interval_left_closed_right_open (test_transit.ReservationTest.test_interval_left_closed_right_open) ... ok
test_partial_failure_rolls_back_no_leak (test_transit.ReservationTest.test_partial_failure_rolls_back_no_leak) ... ok
test_release_valid_token_frees_exactly_once (test_transit.ReservationTest.test_release_valid_token_frees_exactly_once) ... ok
test_stale_token_release_does_not_add_capacity (test_transit.ReservationTest.test_stale_token_release_does_not_add_capacity) ... ok
test_two_requests_compete_last_seat_only_one_wins (test_transit.ReservationTest.test_two_requests_compete_last_seat_only_one_wins) ... ok
test_arrive5_transfer2_cannot_board6_can_board7 (test_transit.TransferRuleTest.test_arrive5_transfer2_cannot_board6_can_board7) ... ok
test_boarding_rule_boundaries (test_transit.TransferRuleTest.test_boarding_rule_boundaries) ... ok

----------------------------------------------------------------------
Ran 17 tests in 0.017s

OK
```

另：以 20 个种子 x 100 个随机小规模实例（共 2000 例）做帕累托前沿与
时间展开穷举的交叉验证，0 例不一致。

可重放样例输出见 `samples/transcript.txt`（`python3 -m samples.replay` 可复现）。
