# kernel-switcher

Runtime FIR kernel replacement with state migration and crossfade.
Pure Python 3.11, standard library only; tests use `unittest`.
Sample arrays and the sample rate come from local fixtures
(`fixtures.py`, 48000 Hz); no audio device is involved.

## Files

- `kernel_switcher.py` — the switcher (voices, crossfade, transition
  graph, snapshot/restore).
- `fixtures.py` — deterministic sample arrays (impulse / constant /
  sine / steps) and a kernel library.
- `test_kernel_switcher.py` — direct time-domain reference tests.
- `demo.py` — runnable end-to-end sample.

## Model

All voices (kernel versions) share one input delay line, so replacing
a kernel never discards state:

- A switch ramps the outgoing voices' weights linearly to zero and the
  new kernel's weight to one over `[at_sample, at_sample + fade_samples]`
  in absolute sample indices. The old kernel's tail rings out smoothly.
- The new voice immediately sees the full input history (state
  migration), so equal-DC-gain kernels on a constant signal produce no
  step.
- A switch issued during a fade re-targets every live voice from its
  current weight to zero; nothing is dropped.
- Voice count is bounded by `max_voices` (default 4). When a switch
  would exceed it, the two lowest-weight voices are composed into one
  exactly equivalent voice (`w_c = w_a + w_b`,
  `h_c = (w_a*h_a + w_b*h_b)/w_c`) — old-kernel state is merged, never
  discarded. Merges and switches are recorded in the transition graph
  (`sw.graph`: nodes = kernel versions, edges = events with absolute
  sample indices).

## Declared properties

- **Latency:** 0 samples (strictly causal direct-form FIR, no lookahead).
- **Numerical tolerance:** float64; tests assert against 1e-9 absolute,
  observed deviation <= ~1e-13.
- **Memory:** `voices <= max_voices`; shared delay line fixed at
  `max_kernel_length - 1` samples (constructor parameter, default 1024).
  Longer kernels are rejected, so installing a longer kernel never reads
  out of bounds and never sees already-discarded history.
- **Per-sample cost:** `<= max_voices * (longest installed kernel)`
  multiply-adds + O(1); independent of fade length and switch rate.
- **Safe points:** every sample boundary. `snapshot()` is
  JSON-serialisable; `restore()` resumes bit-exactly.
- **Block invariance:** switches fire at absolute sample indices, so
  any block partitioning yields bit-identical output.
- **Output metadata:** `process_block` returns a `BlockResult` with
  `samples`, per-sample source-kernel `weights` (version -> weight),
  `start_sample`, and exact `times` (`(start+i)/sample_rate`).

## Rules for switches during a fade

1. Every live voice keeps its current weight as its new start weight
   and ramps to 0 over the new fade; the new kernel ramps 0 -> 1.
   Weights stay normalised (sum = 1) at every sample.
2. If `max_voices` would be exceeded, the two lowest-weight fading
   voices are composed first (exact, see above).
3. Two switches at the same sample are applied in schedule order; the
   last one wins (a superseded kernel never gains weight, so no tail
   is lost).

## Run

```sh
python3.11 -m unittest -v     # test suite
python3.11 demo.py            # runnable sample
```
