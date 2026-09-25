# 实际运行结果

- 日期(UTC+8): 2026-09-26 06:46:31 CST
- Python: Python 3.11.15 (/opt/homebrew/bin/python3.11)
- 平台: Darwin 27.0.0
- 命令: PYTHONPATH=src python3.11 -m unittest -v tests.test_delay_estimation

```
test_anomalous_peak_cannot_cause_unbounded_jump (tests.test_delay_estimation.AnomalousPeakTests.test_anomalous_peak_cannot_cause_unbounded_jump) ... ok
test_end_anomaly_is_refused_instead_of_dragging_path (tests.test_delay_estimation.AnomalousPeakTests.test_end_anomaly_is_refused_instead_of_dragging_path) ... ok
test_checkpoint_roundtrip_preserves_evidence (tests.test_delay_estimation.CheckpointTests.test_checkpoint_roundtrip_preserves_evidence) ... ok
test_resume_matches_continuous_run_and_allows_more_windows (tests.test_delay_estimation.CheckpointTests.test_resume_matches_continuous_run_and_allows_more_windows) ... ok
test_tolerances_and_units_are_declared (tests.test_delay_estimation.DeclarationTests.test_tolerances_and_units_are_declared) ... ok
test_matches_brute_force_on_drift_fixture (tests.test_delay_estimation.ExhaustiveReferenceTests.test_matches_brute_force_on_drift_fixture) ... ok
test_matches_brute_force_on_random_evidence (tests.test_delay_estimation.ExhaustiveReferenceTests.test_matches_brute_force_on_random_evidence) ... ok
test_polarity_flipped_channel (tests.test_delay_estimation.FractionalDriftTests.test_polarity_flipped_channel) ... ok
test_trajectory_tracks_fractional_delay_and_linear_drift (tests.test_delay_estimation.FractionalDriftTests.test_trajectory_tracks_fractional_delay_and_linear_drift) ... ok
test_late_window_revises_earlier_path (tests.test_delay_estimation.LateWindowRevisionTests.test_late_window_revises_earlier_path) ... ok
test_late_window_revises_future_path_across_gap (tests.test_delay_estimation.LateWindowRevisionTests.test_late_window_revises_future_path_across_gap) ... ok
test_constant_channel_is_silent_even_with_overlap (tests.test_delay_estimation.LowInformationTests.test_constant_channel_is_silent_even_with_overlap) ... ok
test_masked_insufficient_intersection_returns_unknown (tests.test_delay_estimation.LowInformationTests.test_masked_insufficient_intersection_returns_unknown) ... ok
test_silence_returns_unknown (tests.test_delay_estimation.LowInformationTests.test_silence_returns_unknown) ... ok
test_three_channels_selected_pair (tests.test_delay_estimation.MultiChannelTests.test_three_channels_selected_pair) ... ok
test_periodic_signal_has_tied_equal_peaks_not_unique (tests.test_delay_estimation.PeriodicSignalTests.test_periodic_signal_has_tied_equal_peaks_not_unique) ... ok

----------------------------------------------------------------------
Ran 16 tests in 1.383s

OK
```
