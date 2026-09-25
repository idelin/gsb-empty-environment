# Polyphase FIR Resampler (Python 3.11, standard library only)

Rational-ratio (`interp/decim`) polyphase FIR resampler with runtime
sample-rate switching, per-channel gap marking, a shared drift-free clock,
and checkpointable phase state. No audio devices: sample arrays and rates
come from local fixtures (`fixtures.py`).

## Files

- `resampler.py` — library: `Kernel`, `ResamplerClock`, `Channel`, `Output`
- `fixtures.py` — deterministic local fixtures (signals, kernels, chunkings)
- `demo.py` — reproducible end-to-end scenario (`python3.11 demo.py`)
- `test_resampler.py` — unittest suite (`python3.11 -m unittest test_resampler -v`)

## Model

- All time arithmetic uses `fractions.Fraction` (exact rationals). Output
  sample times live on the source-sample time base; `Output.timestamp`
  (seconds) is `clock.origin + time / input_rate` — this is the timestamp
  calibration anchor, shared by all channels.
- A ratio switch is defined by a **source sample position**:
  `clock.set_ratio(L, M, at=s, kernel=k)`. Outputs with time `< s` keep the
  old ratio; the first new-ratio output is the smallest `1/L` grid point
  `>= s` (and strictly after the previous output), so output times never
  repeat and never go backward, even for fractional `s`. The output time
  base stays continuous and the **filter tail is not discarded**: outputs
  after the switch still convolve the new kernel over pre-switch samples.
- Channels created from one `ResamplerClock` share the ratio schedule and
  derive identical output grids, so channels processed at different speeds
  (different chunkings) cannot drift — verified exactly (0 drift).
- The FIR kernel is caller-provided (`Kernel(taps, interp_rate)`), sampled
  at `interp_rate` × the input rate. Because the output grid is always
  phase-aligned to the active ratio, taps are used exactly; fractional
  positions (API completeness) use linear interpolation.

## Declared latency

`Kernel.latency = (len(taps) - 1) / (2 * interp_rate)` input samples
(centered kernel). An output at source-time `t` is emitted only after all
input up to `floor(t + latency)` has been pushed. Per-channel memory is
bounded by `kernel_span + latency + one output step + margin`, and the
per-output cost is `O(kernel_span)` MACs.

## Declared numerical tolerance

`NUMERICAL_TOLERANCE = 1e-12` (relative) against a direct reference
convolution evaluated at the same output times. Chunked vs. one-shot
processing and checkpoint/restore are **bit-identical** (asserted with `==`).

## Gaps

Push `None` instead of a sample to mark a gap. A gap is never replaced by
a previous value: it contributes exactly `0.0` to the convolution, and
every output whose kernel window overlaps a gap is flagged
`GAP_CONTAMINATED`. `contaminated_ranges(outputs)` merges the flagged
outputs into explicit polluted `(start, end)` time ranges. Outputs whose
window extends before the stream start are flagged `EDGE` (zero padding).

## Checkpoint / restore

`channel.snapshot()` captures the full recoverable state, including the
fractional phase (`next_time`), history buffer and output index;
`channel.restore(state)` resumes bit-identically — verified for a
checkpoint taken inside the filter tail of a ratio switch.

## Usage

```python
from resampler import Kernel, ResamplerClock
import fixtures

clock = ResamplerClock(48000, 2, 3, Kernel(fixtures.design_lowpass(2, 3, 8), 2))
clock.set_ratio(3, 4, at=997, kernel=Kernel(fixtures.design_lowpass(3, 4, 6), 3))
ch = clock.add_channel()
for chunk in stream:                     # any chunking, incl. empty blocks
    for out in ch.process(chunk):        # chunk items: float or None (gap)
        ...                              # out.time / .timestamp / .value / .flags
```

## Test results

`python3.11 -m unittest test_resampler -v` — 16 tests, all passing:

- impulse response == direct reference (exact rational grid, ≤ 1e-12)
- multi-rate output == direct reference across a switch (≤ 1e-12)
- random/empty/1-sample chunks == one-shot (bit-identical)
- checkpoint/restore inside the filter tail == uninterrupted run (bit-identical)
- ratio switch at non-integer and fractional phase: times strictly
  increasing, no repeats, exact rational steps on both sides
- gaps: contaminated range == exact kernel-window overlap; gap contributes
  0.0 (not the previous value); flags are per-channel
- shared clock: two channels, different chunkings → identical timestamps
  (exact rationals, zero drift)
- declared latency bound respected; per-channel memory bounded

`python3.11 demo.py` prints a reproducible report (fixed seeds) and exits
non-zero on any failed check.
