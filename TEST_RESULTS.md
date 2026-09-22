## 测试命令

```bash
python -m unittest discover -s tests -v
```

## 实际结果

```
test_decision_fault_points_preserve_stable_id (test_faults.FaultTests.test_decision_fault_points_preserve_stable_id) ... ok
test_handover_fault_points_complete_atomically (test_faults.FaultTests.test_handover_fault_points_complete_atomically) ... ok
test_checkpoint_recovers_gaps_acks_and_unconfirmed_decision (test_persistence.PersistenceTests.test_checkpoint_recovers_gaps_acks_and_unconfirmed_decision) ... ok
test_checksum_checkpoint_rejects_corrupt_snapshot (test_persistence.PersistenceTests.test_checksum_checkpoint_rejects_corrupt_snapshot) ... ok
test_middle_corruption_is_rejected (test_persistence.PersistenceTests.test_middle_corruption_is_rejected) ... ok
test_torn_tail_is_truncated_and_replay_remains_stable (test_persistence.PersistenceTests.test_torn_tail_is_truncated_and_replay_remains_stable) ... ok
test_all_idle_sources_retain_previous_boundary (test_scenarios.ScenarioTests.test_all_idle_sources_retain_previous_boundary) ... ok
test_gap_and_explicit_ack_guard_boundary (test_scenarios.ScenarioTests.test_gap_and_explicit_ack_guard_boundary) ... ok
test_handover_is_atomic_and_rejects_old_parent (test_scenarios.ScenarioTests.test_handover_is_atomic_and_rejects_old_parent) ... ok
test_idle_timeout_keeps_unacknowledged_block (test_scenarios.ScenarioTests.test_idle_timeout_keeps_unacknowledged_block) ... ok
test_late_reactivation_does_not_change_closed_result (test_scenarios.ScenarioTests.test_late_reactivation_does_not_change_closed_result) ... ok
test_pending_handover_can_be_cancelled_before_completion (test_scenarios.ScenarioTests.test_pending_handover_can_be_cancelled_before_completion) ... ok
test_local_discrete_event_recovery_and_downstream_dedup (test_simulator.SimulatorTests.test_local_discrete_event_recovery_and_downstream_dedup) ... ok
test_handover_participant_orders (test_state_exploration.StateExplorationTests.test_handover_participant_orders) ... ok
test_watermark_delivery_ack_orders (test_state_exploration.StateExplorationTests.test_watermark_delivery_ack_orders) ... ok

----------------------------------------------------------------------
Ran 15 tests in 1.194s

OK
```
