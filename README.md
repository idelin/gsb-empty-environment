# Multiband Dynamics Pipeline (Python 3.11, stdlib only)

Three-band FIR crossover -> per-band envelope compression -> final lookahead
limiter. All filter coefficients are supplied explicitly by the caller
(see `fixtures.py`); the DSP modules never design filters. Sample arrays and
the sample rate come from local fixtures — no audio devices are touched.

## Layout

- `dsp_fir.py` — streaming FIR (explicit coeffs), integer delay, linear-phase
  group-delay detection.
- `dsp_crossover.py` — N-band crossover; recombination aligns every band to
  the slowest band with integer delays (group-delay compensation).
- `dsp_dynamics.py` — peak envelope follower (attack/release) + compressor
  static curve, one follower per channel.
- `dsp_limiter.py` — lookahead limiter: linked channels, hot-updatable
  smoothed threshold, cancel/restore, over-limit evidence.
- `pipeline.py` — multiband processor: shared control gain across channels,
  independent audio, trajectories, latency reporting.
- `fixtures.py` — explicit coefficient sets + deterministic signals.
- `run_demo.py` — replayable scenarios (exit 0 on success).
- `test_pipeline.py` — 12 unittest cases.

## Declared latency

| stage      | latency (samples)        | note                        |
|------------|--------------------------|-----------------------------|
| crossover  | max band group delay     | 31 @ 48 kHz with fixtures   |
| compressors| 0                        | gain applied in place       |
| limiter    | lookahead                | 240 = 5 ms @ 48 kHz         |
| **total**  | **sum = 271 (5.65 ms)**  | verified by impulse test    |

Convention: input sample `k` lands at output index `k + latency`; every
input block yields an equal-length output block; `flush()` emits the final
`lookahead` tail samples. Bypass toggling never changes latency.

## Declared tolerances (fixtures.py)

- `TOL_RECON = 1e-9` — bypass recombination vs delayed reference
  (measured ~3e-16; the bank sums to a pure delay by construction).
- `TOL_PEAK = 1e-6` — limited peak vs current smoothed threshold.
- `TOL_GAIN_RATE = 1e-9` — gain trajectory vs analytic smoothing curve.

## Policies

- **Linked gain, independent audio**: per band and in the limiter, the
  shared control gain is the most conservative per-channel gain; each
  channel keeps its own audio path, only scaled by the shared gain. A
  right-only spike ducks both channels; left content is never mixed across.
- **Threshold hot-update**: `set_threshold_db` ramps linearly in dB over
  `threshold_smooth_ms`. Required gain is evaluated at *output* time against
  the *current* smoothed threshold, so peaks already inside the lookahead
  region follow the new threshold — no missed limiting, no zipper noise.
  Invariant: `|out[t]| <= lin(threshold_trajectory[t])` whenever enabled.
- **Cancel/restore**: bypass only retargets the gain to 1.0 (via release
  smoothing); the delay line keeps running, so restore cannot double the
  latency and re-engagement is instant (lookahead-covered).
- **Unknown samples (NaN)**: NaN propagates through FIR state exactly
  `len(coeffs)` samples; the envelope follower is tainted for
  `5 * max(attack, release)` samples (declared in `dsp_dynamics.py`); the
  limiter marks any output whose lookahead window contains a NaN. Unknown
  in one channel taints the shared gain, marking both channels. Regions are
  contiguous, bounded, and the pipeline recovers automatically.
- **No hard clipping**: output equals delayed input times the recorded gain
  trajectory (asserted in tests). Over-limit evidence records
  `{start, end, peak, threshold_db, min_gain}` per segment, including
  segments that occurred while cancelled.

## Run

```
python3.11 -m unittest test_pipeline -v   # 12 tests
python3.11 run_demo.py                    # 4 replayable scenarios
```

## Test coverage (test_pipeline.py)

- bypass recombination within `TOL_RECON`; declared == measured latency
- group-delay compensation across bands of different lengths
- multiband separation (Goertzel) + zero phase error of the band sum
- right-only spike drives shared gain; left audio independent
- peak exactly at a block boundary is limited; waveform is gain-shaped
- threshold change mid-lookahead still limits; latency unchanged
- cancel/restore: no missed limiting, no double delay, evidence kept
- NaN marking/propagation region exact and recovery afterwards
- envelope attack/release and limiter release match analytic curves
- compressor steady-state gain and attack speed
