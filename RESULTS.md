# 实际运行结果

```
$ /opt/homebrew/bin/python3.11 -m unittest discover -s tests -t . -v
test_burst_does_not_become_permanent_delay (tests.test_drift_vs_jitter.TestDriftVsJitter.test_burst_does_not_become_permanent_delay) ... ok
test_concealment_stays_bounded (tests.test_drift_vs_jitter.TestDriftVsJitter.test_concealment_stays_bounded) ... ok
test_drift_estimate_converges (tests.test_drift_vs_jitter.TestDriftVsJitter.test_drift_estimate_converges) ... ok
test_playout_is_monotonic (tests.test_drift_vs_jitter.TestDriftVsJitter.test_playout_is_monotonic) ... ok
test_shrink_drops_forward_and_never_replays (tests.test_epoch_and_monotonic.TestDelayShrinkNoReplay.test_shrink_drops_forward_and_never_replays) ... ok
test_late_packet_does_not_change_played_output (tests.test_epoch_and_monotonic.TestLatePacketCannotRewrite.test_late_packet_does_not_change_played_output) ... ok
test_old_epoch_packet_is_rejected_and_ignored (tests.test_epoch_and_monotonic.TestOldEpochRejected.test_old_epoch_packet_is_rejected_and_ignored) ... ok
test_true_and_compensated_samples_balance (tests.test_restore_and_conservation.TestConservation.test_true_and_compensated_samples_balance) ... ok
test_delivery_order_does_not_change_schedule (tests.test_restore_and_conservation.TestRandomDeliveryOrder.test_delivery_order_does_not_change_schedule) ... ok
test_snapshot_restore_is_bit_identical (tests.test_restore_and_conservation.TestSaveRestore.test_snapshot_restore_is_bit_identical) ... ok

----------------------------------------------------------------------
Ran 10 tests in 54.427s

OK

[delay] base=25.0 ms  pre-burst target=33.0 ms  burst peak target=65.6 ms  final target=33.0 ms

[loss] concealed samples during drift+bursts: 1440

[drift] true=150.0 ppm  estimated=132.2 ppm  (tol +/-30)

[shrink] dropped_samples=2796  min_advance=384.0  final target=20.0 ms  final level=28.5 ms

[late] late_packets=1 late_samples=480 concealed=17643

[epoch] old_epoch_packets=1  epoch_resets=1

[conservation]
  sent_samples             384480
  net_lost_samples         6240
  arrived_samples          385920
  dup_injected_samples     7680
  accepted_samples         361920
  duplicate_samples        7200
  overflow_samples         13920
  late_samples             2880
  emitted_true_samples     354577
  dropped_samples          7343
  buffered_now_samples     0
  emitted_synth_samples    58223
  conceal_missing_samples  37928
  grow_synth_samples       17415
  preroll_synth_samples    2880
  output_samples           412800
  check: sent + dup_injected == arrived + net_lost      OK
  check: accepted == emitted_true + dropped + buffered  OK
  check: output == emitted_true + emitted_synth         OK
  check: output == ticks * block                        OK
  check: synth == conceal + grow + preroll              OK

[reorder] traces identical=True  concealed=0  underrun-free

[restore] ticks: continued=660 restored=660 baseline=660
```
