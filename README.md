# Sliding-Window NCC Multi-Channel Delay Estimation

Pure Python 3.11 (standard library only) relative-delay estimator: sliding-window
normalized cross-correlation (NCC) with whole-trajectory dynamic-programming
path selection. Sample arrays and the sample rate come from local fixtures
(`fixtures.py`); no audio devices are used.

## Files

- `delay_estimation.py` — library: NCC, candidate extraction, Viterbi DP,
  exhaustive reference, `DelayEstimator` (revision + checkpoint/restore).
- `fixtures.py` — synthetic two-channel fixtures; all declared ground truth
  (delays, drift, sample rate) and numeric tolerances live here.
- `test_delay_estimation.py` — `unittest` suite (11 tests).

## Conventions

- Delay `d` (samples): `y[t] ~= polarity * x[t - d]`; positive `d` = `y` lags `x`.
- Sub-sample delays via parabolic interpolation of the NCC peak.
- Missing data: boolean masks; only pairwise-valid samples enter the NCC.
- Polarity inversion: candidates come from NCC peaks (polarity +1) and
  troughs (polarity -1).
- Equal-height peaks (within `tie_tol`) are all kept as candidates; a tied
  window is reported with `unique=False` and inflated uncertainty — never as
  a single answer. Candidate truncation never splits a tied group.
- Trajectory: Viterbi DP maximizes `sum(score) - step_penalty * |dDelay|`
  with a hard `max_jump` bound (anomalous peaks cannot cause unbounded jumps)
  and an explicit null state (low-information windows stay `delay=None`).
- Uncertainty: curvature-based peak width; for tied windows plus half the
  distance to the nearest tied competitor. Evidence = chosen NCC score.
- Late revisions (`revise_observation`) re-run the DP, so a correction can
  retroactively change later windows; `checkpoint()`/`restore()` is
  bit-identical to a continuous run (DP is deterministic in the observations).

## Declared parameters and tolerances (fixtures.py)

| Parameter      | Value   | Parameter        | Value        |
|----------------|---------|------------------|--------------|
| SAMPLE_RATE    | 8000 Hz | DELAY0           | 3.0 samples  |
| WINDOW / HOP   | 512/256 | DRIFT            | 0.0015 samp/samp |
| MAX_LAG        | 64      | NOISE_STD        | 0.02         |
| MIN_OVERLAP    | 64      | TRAJ_TOL         | 0.75 samples |
| MIN_SCORE      | 0.2     | INT_DELAY_TOL    | 0.5 samples  |
| step_penalty   | 0.10    | max_jump         | 2.0 samples  |
| null_penalty   | 0.5     | TIE_SCORE_TOL    | 1e-6         |

## Tests

- Periodic signal -> multiple equal-height peaks kept, `unique=False`.
- Silence / insufficient pairwise overlap -> unknown (`delay=None`).
- Missing-data masks: gap windows unknown, partial-overlap windows still track.
- Synthetic fractional delay + linear drift: per-window trajectory within
  `TRAJ_TOL`; polarity-inverted channel recovers delay with polarity -1.
- Outlier peaks (far and near): trajectory stays bounded and correct.
- Late revision retroactively flips ambiguous later windows; revised run
  equals a fresh continuous run.
- Viterbi DP matches exhaustive enumeration of all candidate combinations.
- Checkpoint/restore + post-restore revision matches continuous run exactly.

## Actual run results (Python 3.11.15, `python3.11 -m unittest -v`)

```
test_checkpoint_restore_matches_continuous_run ... ok
test_dp_matches_exhaustive_reference ... ok
test_fractional_delay_with_drift_trajectory ... ok
test_late_revision_matches_continuous_run ... ok
test_polarity_inversion_recovers_delay ... ok
test_insufficient_overlap_returns_unknown ... ok
test_missing_data_mask_windows ... ok
test_silence_returns_unknown ... ok
test_periodic_signal_reports_multiple_equal_peaks ... ok
test_revision_retroactively_updates_path ... ok
test_outlier_peak_does_not_cause_unbounded_jump ... ok
----------------------------------------------------------------------
Ran 11 tests in 39.136s
OK
[drift] windows=22 max_err=0.207 mean_err=0.117 samples (tol=0.75)
```

Measured drift-track accuracy: max error 0.207 samples, mean 0.117 samples,
well inside the declared 0.75-sample tolerance.
