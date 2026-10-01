# stftpipe

Incremental STFT pipeline in pure Python 3.11 (standard library only,
tests via `unittest`). Supports variable hop, frame loss, and local
spectral editing.

## Declarations

- **Algorithmic latency**: one window length (`size` samples). A frame's
  covered interval can only be emitted once the whole window has arrived;
  see `pipeline.algorithmic_latency_samples`.
- **Numerical tolerance**: `NUMERICAL_TOLERANCE = 1e-9`
  (pure-python float64 recursive FFT).
- **Input**: sample arrays and sample rates come from local fixtures
  (`stftpipe.fixtures`); no audio device is ever touched.

## Design

- ISTFT normalizes each sample by the *actual* summed squared window
  weights: `out[n] = sum(w * x_i) / sum(w^2)`. No fixed COLA coefficient
  is used, so variable hop schedules work unchanged.
- Samples whose weight falls to zero (e.g. after dropping a frame) are
  reported through an unrecoverable `mask` / `unrecoverable_regions()`;
  they are never zero-filled and called lossless.
- Spectral edits must preserve the real-signal conjugate symmetry
  `X[k] = conj(X[N-k])`: mirror bins are auto-filled; inconsistent mirror
  pairs, complex DC/Nyquist, and out-of-range bins raise
  `SpectralEditError`.
- Edits / drops / restores rebuild only the affected output interval
  `[starts[lo], starts[hi] + N)`; untouched regions keep published values.
- Channels share one frame schedule and stay time-aligned; mismatched
  sample rates are rejected.
- `checkpoint()` preserves the unclosed overlap tail (`open_tail`, from
  the last frame start on); `from_checkpoint()` + `extend()` resume a
  stream bit-exactly against a one-shot run.
- `apply_batch()` is transactional: any failed correction rolls back and
  leaves `published_version` unchanged.

## Layout

- `stftpipe/fft.py` — radix-2 FFT/IFFT (naive DFT fallback)
- `stftpipe/reference.py` — direct O(n^2) DFT/IDFT reference
- `stftpipe/windows.py` — hann / hann_sym / hamming / rect
- `stftpipe/core.py` — frame schedule, kernel, latency/tolerance constants
- `stftpipe/pipeline.py` — incremental pipeline (drop/restore/edit,
  checkpoint, transactions, multi-channel)
- `stftpipe/fixtures.py` — deterministic local signals and sample rates
- `examples/replay_demo.py` — replayable end-to-end demo
- `tests/` — unittest suite

## Run

```sh
python -m unittest discover -s tests -v   # test suite
python examples/replay_demo.py            # replayable demo, exit 0 on pass
```
