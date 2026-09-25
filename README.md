# Rational-Ratio Polyphase FIR Resampler

Pure Python 3.11, standard library only. No audio devices: sample arrays
and sample rates come from local deterministic fixtures (`fixtures.py`).

## Files

- `resampler.py` — core: `Resampler`, `SharedClock`, `Output`, flags.
- `fixtures.py` — seeded kernels, tone/impulse arrays, sample rates.
- `test_resampler.py` — `unittest` suite (15 tests).
- `demo.py` — replayable end-to-end scenarios (`python3 demo.py`).

## Model

Output `n` sits at source-domain position `t_n` (exact `Fraction`); each
output is

```
y(t) = sum_j h_c(f*L + j*L) * x[floor(t) - j],   f = t - floor(t)
```

where `h_c` is the caller-provided kernel sampled on the polyphase grid,
with linear interpolation between adjacent branches for off-grid positions
(only reachable after a ratio switch at a non-integer phase).

## Declared properties

- **Latency**: group delay `(N-1)/(2L)` source samples for kernel length `N`
  and upsampling factor `L`. Timestamps are delay-compensated
  (`latency_samples`, `latency_seconds`); early outputs carry negative
  pre-ring timestamps.
- **Numeric tolerance**: grid-aligned outputs match a direct
  upsample-convolve-downsample reference to float rounding (< 1e-12);
  off-grid outputs use linear branch interpolation. `TOLERANCE = 1e-9`.
- **Bounded resources**: <= `2*ceil((N+1)/L)` multiply-adds per output;
  delay line trimmed to `O(N/L + M/L)` samples regardless of stream length.
- **Ratio switching**: `set_ratio(up, down, at_source_index, kernel=None)`;
  the switch point is a (possibly fractional) source-sample position, the
  new grid is anchored exactly on it, and output times are guaranteed
  strictly increasing (never repeated, never backwards).
- **Filter tail**: `flush()` zero-pads the input end and emits outputs until
  the delay-compensated output time reaches the end of the input; tail
  outputs are flagged `FLAG_TAIL`. Nothing is dropped.
- **Gaps**: `process(chunk, gaps=[(start, length), ...])` zero-fills the
  gap (never holds the previous value) and flags every output whose
  convolution window overlaps it with `FLAG_CONTAMINATED`; merged ranges
  are in `contaminated_ranges`.
- **Checkpoint/restore**: `checkpoint()` returns a JSON-friendly dict;
  `Resampler.restore(state)` continues bit-exactly, including mid-tail.
- **Shared clock**: channels created from one `SharedClock` derive exact
  rational timestamps from a single origin, so different ratios and
  different chunking/processing rates cannot drift apart.

## Usage

```python
from fractions import Fraction
import fixtures
from resampler import Resampler, SharedClock

clock = SharedClock(fixtures.FS_AUDIO)
r = clock.channel(fixtures.lowpass_kernel(25, 3, 2), up=3, down=2)
r.set_ratio(1, 2, at_source_index=Fraction(21, 2))   # optional, runtime
outs = r.process(chunk, gaps=[(120, 16)])            # gaps optional
state = r.checkpoint()                               # resumable
outs += r.flush()                                    # emit the tail
```

## Tests

```
python3 -m unittest test_resampler -v
python3 demo.py
```
