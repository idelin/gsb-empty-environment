# Multi-Channel Delay Aligner (Python 3.11, stdlib only)

Per-channel integer + fractional delay alignment of sample arrays onto a
common output time base. No audio devices: sample arrays, sample rate,
fractional-delay FIR coefficients, declared delays and tolerances are all
provided by the local fixture (`fixture.py`).

## Model

Output index `n` aligns to input index `n` on the common time base. Each
channel `c` applies a declared, possibly time-varying delay `d_c(n)`:

```
y_c[n] = sum_k h(frac)[k] * x_c[n - I - k]
I      = floor(d_c(n) - group),  frac = d_c(n) - group - I,  group = (K-1)/2
```

* Fractional-delay coefficients `h` come from the fixture (windowed sinc,
  K = 16 taps, group delay 7.5); the aligner never designs its own filter.
* Delay changes are driven by absolute sample positions and ramp linearly
  over a declared transition, so the read position `n - d_c(n)` never jumps:
  no sudden repeat/skip of undeclared samples (step changes and ramp slopes
  > 1 sample/sample are rejected).
* Emitted output is confirmed; scheduled changes ahead of the emit frontier
  are tentative and can be retracted (`retract(version)`).
* Outputs needing unavailable input are zero-filled and flagged; outputs
  whose window touches missing input are flagged contaminated (missing
  samples contribute zero). The aligner reports the common valid interval
  plus both masks.
* `checkpoint()` / `load_checkpoint()` snapshot filter history, the
  missing-data contamination mask and the delay version together; a
  restored aligner continues bit-identically.
* Memory per channel is bounded by `ceil(max_delay - group) + K` history
  samples (73 for the declared max delay 64.0 and K = 16), independent of
  stream length.
* Channels are fully independent: no crosstalk.

## Files

* `fixture.py`    — kernels, sample arrays, sample rate, declared delays, tolerances
* `aligner.py`    — streaming `MultiChannelAligner`
* `reference.py`  — whole-segment reference convolution (validates any chunking)
* `test_aligner.py` — unittest suite
* `run_demo.py`   — run sample

## Run sample (`python3.11 run_demo.py`)

```
sample rate      : 48000 Hz (from fixture)
channels         : 3 x 600 samples
declared delays  : [12.0, 9.25, 30.5] samples
kernel length    : 16 taps (group delay 7.5)
tolerance        : 1e-09
memory bound     : 73 samples/channel (ceil(max_delay - group) + kernel_length)
delay changes    : v1 ch1 @200 -> 16.0 (48), v2 ch2 @320 -> 40.0 (64), v3 retracted
checkpoint       : restored at input 308, frontier 312, delay_version 4
output           : 604 samples/channel on the common time base
common valid     : (38, 603)
zero-filled      : 73 channel-samples
contaminated     : 0 channel-samples
max history used : 71 samples/channel (bound 73)
reference check  : max |err| = 0 (<= 1e-09), masks identical
OK
```

## Test results (`python3.11 -m unittest -v`)

```
test_checkpoint_carries_history_mask_and_version ... ok
test_checkpoint_roundtrip_bit_identical ... ok
test_chunking_does_not_change_output ... ok
test_matches_whole_segment_reference ... ok
test_impulse_moves_to_target_position ... ok
test_no_channel_crosstalk ... ok
test_memory_bound_from_max_delay_and_kernel_length ... ok
test_missing_data_contamination_mask ... ok
test_retract_confirmed_region_raises ... ok
test_retract_unconfirmed_region ... ok
test_delay_change_moves_impulse_to_declared_positions ... ok
test_delay_trajectory_is_continuous_and_bounded ... ok
test_unsmooth_change_rejected ... ok
test_overlapping_changes_match_reference ... ok
test_tail_flush_drains_everything ... ok
test_common_valid_interval_and_zero_fill_marks ... ok

Ran 16 tests in ~29s
OK
```
