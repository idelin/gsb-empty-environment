# Test results

```
$ /opt/homebrew/bin/python3.11 --version
Python 3.11.15

$ /opt/homebrew/bin/python3.11 -m unittest discover -s tests -v
test_far_future_predicate_uses_explicit_bound (test_clock.TestHybridLogicalClock.test_far_future_predicate_uses_explicit_bound) ... ok
test_logical_overflow_leaps_physical (test_clock.TestHybridLogicalClock.test_logical_overflow_leaps_physical) ... ok
test_newer_remote_epoch_leaps_physical (test_clock.TestHybridLogicalClock.test_newer_remote_epoch_leaps_physical) ... ok
test_receiving_leading_remote_is_overtaken (test_clock.TestHybridLogicalClock.test_receiving_leading_remote_is_overtaken) ... ok
test_restart_floor_dominates_behind_wall_with_new_epoch (test_clock.TestHybridLogicalClock.test_restart_floor_dominates_behind_wall_with_new_epoch) ... ok
test_same_millisecond_events_strictly_increase (test_clock.TestHybridLogicalClock.test_same_millisecond_events_strictly_increase) ... ok
test_wall_followed_then_wall_backward_keeps_monotonic (test_clock.TestHybridLogicalClock.test_wall_followed_then_wall_backward_keeps_monotonic) ... ok
test_ordering_is_physical_epoch_logical (test_clock.TestTagOrdering.test_ordering_is_physical_epoch_logical) ... ok
test_wire_roundtrip_and_rejection (test_clock.TestTagOrdering.test_wire_roundtrip_and_rejection) ... ok
test_after_publish_restart_does_not_reuse_tag (test_replay.TestCrashWindowsDirect.test_after_publish_restart_does_not_reuse_tag) ... ok
test_after_sync_republishes_same_tag_and_peer_dedupes (test_replay.TestCrashWindowsDirect.test_after_sync_republishes_same_tag_and_peer_dedupes) ... ok
test_before_sync_leaves_nothing_and_restart_advances_epoch (test_replay.TestCrashWindowsDirect.test_before_sync_leaves_nothing_and_restart_advances_epoch) ... ok
test_far_future_is_quarantined_and_clock_unaffected (test_replay.TestCrashWindowsDirect.test_far_future_is_quarantined_and_clock_unaffected) ... ok
test_artifact_is_serializable_json (test_replay.TestMixedClockScenario.test_artifact_is_serializable_json) ... ok
test_exact_tag_chains (test_replay.TestMixedClockScenario.test_exact_tag_chains) ... ok
test_full_trace_validates (test_replay.TestMixedClockScenario.test_full_trace_validates) ... ok
test_replay_is_deterministic (test_replay.TestMixedClockScenario.test_replay_is_deterministic) ... ok
test_validator_detects_reused_tag_and_bad_edge (test_replay.TestValidatorIndependence.test_validator_detects_reused_tag_and_bad_edge) ... ok
test_duration_units_and_arithmetic (test_time_model.TestTimeModel.test_duration_units_and_arithmetic) ... ok
test_instant_geometry_keeps_types_separate (test_time_model.TestTimeModel.test_instant_geometry_keeps_types_separate) ... ok
test_offset_table_precise_conversion (test_time_model.TestTimeModel.test_offset_table_precise_conversion) ... ok
test_synthetic_clock_wall_goes_backward_via_table (test_time_model.TestTimeModel.test_synthetic_clock_wall_goes_backward_via_table) ... ok
test_wall_reading_is_raw_only (test_time_model.TestTimeModel.test_wall_reading_is_raw_only) ... ok

----------------------------------------------------------------------
Ran 23 tests in 1.817s

OK

$ /opt/homebrew/bin/python3.11 bin/replay_demo.py
scenario   : mixed_clock
validation : OK
events=40 published=29 edges=32 duplicates=1 quarantined=1 crashes=3 restarts=6
  00 a restart    hlc/f4240.0.0/a  initial incarnation
  01 b restart    hlc/f4240.0.0/b  initial incarnation
  02 c restart    hlc/f4240.0.0/c  initial incarnation
  03 a local      hlc/f4240.1.0/a  
  04 a local      hlc/f4240.2.0/a  
  05 b local      hlc/f4240.1.0/b  
  06 a send       hlc/f4240.3.0/a  
  07 b receive    hlc/f4240.4.0/b  
  08 b local      hlc/f4240.5.0/b  
  09 b local      hlc/f4240.6.0/b  
  10 c local      hlc/f4240.1.0/c  
  11 c local      hlc/f4240.2.0/c  
  12 c local      hlc/f4240.3.0/c  
  13 c local      hlc/f4240.4.0/c  
  14 c local      hlc/f4240.5.0/c  
  15 c local      hlc/f4240.6.0/c  
  16 c local      hlc/f4240.7.0/c  
  17 c local      hlc/f4241.0.0/c  
  18 c send       hlc/f4241.1.0/c  
  19 b receive    hlc/f4241.2.0/b  
  20 a local      hlc/f4240.4.0/a  
  21 a send       hlc/f4240.5.0/a  
  22 c receive    hlc/f4241.2.0/c  
  23 c send       hlc/f4df8.0.0/c  
  24 a receive    hlc/f4df8.1.0/a  
  25 a local      hlc/f4df8.2.0/a  
  26 b quarantine hlc/2541b2640.0.0/attacker  tag physical 10001000000 exceeds wall 1000000 + skew 500000
  27 b local      hlc/f4241.3.0/b  
  28 c crash      hlc/f4df8.1.0/c  before_sync (tag never durable)
  29 c restart    hlc/f4df8.0.1/c  recovered incarnation #1
  30 c send       hlc/f4df8.1.1/c  
  31 c crash      hlc/f4df8.1.1/c  after_sync (durable, unpublished; outbox will republish)
  32 c restart    hlc/f4df8.0.2/c  recovered incarnation #2
  33 a receive    hlc/f4df9.0.0/a  
  34 a send       hlc/f4df9.1.0/a  
  35 a crash      hlc/f4df9.1.0/a  after_publish (ack never synced)
  36 a restart    hlc/f4df9.0.1/a  recovered incarnation #1
  37 b receive    hlc/f4df9.2.0/b  
  38 b duplicate  hlc/f4df9.1.0/a  message already consumed durably
  39 a local      hlc/f4df9.1.1/a  

$ /opt/homebrew/bin/python3.11 -W error -m unittest discover -s tests
.......................
----------------------------------------------------------------------
Ran 23 tests in 0.907s

OK
```
