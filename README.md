# filterverify

Black-box verifier for streamable audio filter implementations.
Python 3.11, standard library + `unittest` only. No audio devices: all
sample arrays and the sample rate come from local deterministic fixtures
(`filterverify/fixtures.py`).

## Conventions (declared, not implied)

- **Frequencies** are in Hz together with the sample rate `fs`.
  Normalized frequency is `f / fs`; Nyquist is `0.5 * fs` (normalized 0.5).
  Band edges `>= fs/2` are rejected by `BandSpec` (they alias).
- **Window normalization**: tone amplitudes use coherent-gain
  normalization `2*|sum(x*w)| / sum(w)`. Dividing by `N` (or by the
  energy/ENBW norm `sum(w^2)`) mis-reads unity gain as attenuation
  (factor 0.5 for Hann) — that is a measurement bug, not filter loss.
- **Tone fits** are least-squares `a*cos + b*sin`; amplitude `hypot(a,b)`,
  phase `atan2(b,a)`. The Gram determinant is checked: at exactly `fs/2`
  the sin basis vanishes and the fit raises `IllConditionedError` instead
  of returning garbage. Tones at e.g. `0.4875*fs` are measured correctly.
- **Latency**: implementations declare `latency_samples` (group delay).
  The verifier aligns by the declared value and reports
  *measured minus declared* offset separately, against a declared
  tolerance (`Tolerances`).
- **Resolution vs error**: every measurement reports the response error
  and the finite test resolution (tone spacing / grid spacing /
  between-grid margin) as separate fields, plus the actual sample count
  and the measurement method.

## Implementation protocol

```python
class MyFilter:
    latency_samples: int           # declared group delay
    coefficients: list[float]      # optional; enables full-band certificate
    def reset(self) -> None: ...
    def process(self, block: list[float]) -> list[float]: ...  # 1 out per 1 in
```

The verifier feeds the implementation in blocks of varying sizes and
compares against one-shot processing.

## What is measured

| check | signal / method |
|---|---|
| `impulse_response` | unit impulse; compared to declared coefficients if exposed |
| `time_offset` | log chirp + cross-correlation lag vs declared latency |
| `passband_deviation` | multitone, least-squares tone fits; windowed coherent-gain cross-check |
| `passband_phase_residual` | multitone phase after latency alignment (phase misalignment) |
| `stopband_leakage` | stopband multitone, max output/input tone ratio (dB) |
| `sweep_passband_grid` | stepped-sine sweep, per-step fit after settling — grid points only, **no** inter-point claim |
| `fir_conservative_bound` | from coefficients: exact grid + demodulated derivative bound `|G'(f)| <= max|G'_k| + D2*df/2`, valid **between** grid points |
| `near_nyquist_response` | tones at 0.45fs / 0.4875fs, conditioned fit |
| `block_consistency` | random block-partition search + ddmin-style shrink |
| `shift_invariance` | shifted-probe search k=1..32 + minimal-shift shrink |

A bare sweep can never certify a whole band; full-band claims come only
from the coefficient-based conservative bound. Without `coefficients`,
the report states `not certified` explicitly.

## Counterexamples

Failing runs produce replayable `CounterExample` records (seed, signal
length, partition/shift, tolerance, worst error, evidence excerpt),
shrunk before reporting (e.g. a random 10-block partition shrinks to
`[904, 120]`). `ce.replay(impl_factory)` re-runs the exact input; records
round-trip through JSON (`artifacts/counterexample_*.json`).

## Run

```
python3 run_example.py                  # verifies good FIR + 4 injected bad impls
python3 -m unittest discover -s tests   # 25 tests
```

## Sample output (abridged)

```
Verification report: GoodLowpass (reference)
declared latency: 26 samples
overall: PASS
PASS time_offset: 0 (tol 1) [method=log-chirp cross-correlation lag minus declared latency, n=8192, fs=8000, resolution 1 Hz]
PASS passband_deviation: 0.00769412 (tol 0.05) [method=multitone least-squares tone fits; ..., n=4096, fs=8000, resolution 128.571 Hz]
PASS stopband_leakage: -57.4563 (tol -40) [method=stopband multitone; ..., n=4096, fs=8000, resolution 300 Hz]
PASS fir_conservative_bound: 0.00255072 (tol 0.05) [method=coefficient grid + demodulated derivative bound ..., resolution 3.51562 Hz]
full-band claim: certified from FIR coefficients: passband deviation <= 0.002551,
  stopband attenuation >= 53.8 dB everywhere (grid 257 points, between-grid margin 0.000139)

Verification report: BlockResetFIR (drops state per block)
overall: FAIL
FAIL block_consistency: 0.5857 (tol 1e-06) [method=random block-partition search (12 trials) + ddmin shrink, n=1024, fs=8000]
COUNTEREXAMPLE block_inconsistency: max_error=0.5857 at index 913 (tol 1e-06), partition=[904, 120], replay=(seed=8639, n=1024)
  replayed artifacts/counterexample_BlockResetFIR_0.json: still_failing=True, max_error=0.586

Verification report: UndeclaredDelayFIR (latency off by 3)
overall: FAIL
FAIL time_offset: 3 (tol 1) [...]
FAIL passband_phase_residual: 2.3568 (tol 0.1) [...]

Verification report: AttenuatingFilter (gain 0.5)
overall: FAIL
FAIL passband_deviation: 0.5 (tol 0.05) [...]

Verification report: BlockParityFilter (block-size-dependent gain)
overall: FAIL
COUNTEREXAMPLE block_inconsistency: max_error=0.148662 ..., partition=[869, 155]
COUNTEREXAMPLE phase_misalignment: max_error=0.149688 ..., shift=1
```

## Test results

```
$ python3 -m unittest discover -s tests -v
...
Ran 25 tests in 1.841s
OK
```

Coverage: tone-fit accuracy and Nyquist edge cases, window coherent-gain
normalization (unity gain reads 1.0), band-spec validation, FIR bound
bracketing vs dense sampling, good-implementation full pass, and
detection of each injected bad implementation with replayable,
JSON-round-tripped counterexamples.

## Layout

- `filterverify/signals.py` — impulse / log chirp / multitone / stepped sweep / probe
- `filterverify/metrics.py` — tone fits, windowed amplitude, lag, phase utils
- `filterverify/bands.py` — band spec + normalization conventions
- `filterverify/streaming.py` — block streaming adapter, random partitions
- `filterverify/counterexample.py` — search, shrink, replay, JSON evidence
- `filterverify/fir_bounds.py` — conservative full-band bounds from coefficients
- `filterverify/verifier.py` — orchestration, tolerances
- `filterverify/report.py` — measurement records, text/JSON report
- `filterverify/impls.py` — reference FIR + known-bad injected implementations
- `filterverify/fixtures.py` — local deterministic sample arrays + sample rate
- `run_example.py` — end-to-end demo; writes `artifacts/`
- `tests/` — 25 unittest cases
