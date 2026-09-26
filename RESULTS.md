# 实际运行结果

环境: Python 3.11.15 (macOS, arm64)，命令: `python3.11 -m unittest discover -v`

```
test_burst_not_memorised_as_permanent_delay (tests.test_clock_drift_vs_jitter.ClockDriftVsJitterTest.test_burst_not_memorised_as_permanent_delay) ... ok
test_clock_tracks_drift (tests.test_clock_drift_vs_jitter.ClockDriftVsJitterTest.test_clock_tracks_drift) ... ok
test_drift_absorbed_by_rate_not_by_drops (tests.test_clock_drift_vs_jitter.ClockDriftVsJitterTest.test_drift_absorbed_by_rate_not_by_drops) ... ok
test_jitter_estimate_not_poisoned_by_drift (tests.test_clock_drift_vs_jitter.ClockDriftVsJitterTest.test_jitter_estimate_not_poisoned_by_drift) ... ok
test_playout_position_monotonic (tests.test_clock_drift_vs_jitter.ClockDriftVsJitterTest.test_playout_position_monotonic) ... ok
test_sample_conservation_report (tests.test_determinism_and_conservation.ConservationTest.test_sample_conservation_report) ... ok
test_random_delivery_order_same_trajectory (tests.test_determinism_and_conservation.RandomDeliveryOrderTest.test_random_delivery_order_same_trajectory) ... ok
test_snapshot_restore_identical (tests.test_determinism_and_conservation.SaveRestoreTest.test_snapshot_restore_identical) ... ok
test_old_epoch_rejected (tests.test_epoch_and_monotonic.EpochTest.test_old_epoch_rejected) ... ok
test_late_packet_cannot_rewrite_played_interval (tests.test_epoch_and_monotonic.LatePacketTest.test_late_packet_cannot_rewrite_played_interval) ... ok
test_delay_shrink_never_replays (tests.test_epoch_and_monotonic.ShrinkTest.test_delay_shrink_never_replays) ... ok
test_duplicate_traced (tests.test_epoch_and_monotonic.TraceabilityTest.test_duplicate_traced) ... ok
test_missing_concealed_and_marked (tests.test_epoch_and_monotonic.TraceabilityTest.test_missing_concealed_and_marked) ... ok
test_overflow_traced (tests.test_epoch_and_monotonic.TraceabilityTest.test_overflow_traced) ... ok

----------------------------------------------------------------------
Ran 14 tests in 0.283s

OK

--- conservation report (scenario: loss/dup/burst/drift) ---
source packets generated : 600
network: lost=22 dup_copies=13 delivered=591
receiver: stored=520 late=50 dup=11 overflow=10 old_epoch=0
missing packets concealed: 80
real samples played      : 82080
compensated samples      : 14184 (conceal=11680, grow=2504)
dropped by shrink        : 1120
skipped missing (shrink) : 1120
output samples           : 96264 = real 82080 + synth 14184
compensation ratio       : 14.7345%
conservation: 96000 generated = 82080 real + 11680 concealed + 1120 dropped + 1120 skipped
```

## 测试1补充指标（漂移 5000ppm + 突发抖动场景）

```
slope estimate (end)       : 1.005010  (true 1.005000)
headroom pre-burst  [3,5)s : max   40.0 ms
headroom burst      [6,9)s : max   81.2 ms
headroom post      [18,20]s: max   40.0 ms
jitter EWMA pre-burst      : max   6.42 ms
jitter EWMA post-burst     : max   5.71 ms
drops before burst         : 0 samples
missing=0 overflow=0 late=0
```
