# tinyreduce

Pure-stdlib (Python 3.11+) multi-axis reductions — `sum` / `mean` / `min` /
`max` — with first-class gradients, on a tiny hand-rolled CPU tensor.
No autodiff frameworks, no tensor libraries, no third-party code.

## Layout

- `tinyreduce/tensor.py` — flat-storage + strides `Tensor`, views
  (slicing incl. negative steps, `permute`/`transpose`, `reshape`),
  a minimal reverse-mode engine with storage-level gradient accumulation.
- `tinyreduce/ops.py` — axis normalization, chunked tree reduction engine,
  workspace accounting, gradient builders, non-differentiable point marks.
- `tinyreduce/errors.py` — `AxisError`, `EmptyReductionError`,
  `NonDifferentiableError`.
- `tests/` — unittest suite, including exhaustive small-domain enumeration
  against an independent reference (`tests/reference.py`).
- `examples/demo.py` — runnable tour (`python3 examples/demo.py`).

## Semantics (explicit choices)

- **Axes**: negative axes normalized; duplicates rejected (`AxisError`);
  evaluation order is `sorted(axes)`, so axis permutation changes neither
  values (bitwise identical) nor gradient shapes.
- **Empty slices** (zero-size reduction dims or fully masked):
  `sum -> 0.0` (identity); `mean` / `min` / `max` raise
  `EmptyReductionError` — mean of an empty set is undefined. A zero-size
  *output* (no slices at all) never raises.
- **Masks**: truthy entries are excluded (numpy masked-array convention);
  mask shape must equal input shape.
- **Ties in min/max**: gradient is split equally among *all* tied elements
  of the slice, counted globally — the policy is identical for every
  `chunk_size` (never "first element wins").
- **Non-differentiable points**: tied extrema (and nan extrema) yield only a
  subgradient. Such points are marked (`grad.nondiff_indices`), and any
  higher-order differentiation through them raises
  `NonDifferentiableError` instead of silently inventing a value.
  Elsewhere second derivatives of all four ops are exactly zero.
- **Chunked tree execution**: each output slice's reduction space is
  processed in `chunk_size` chunks and combined with a binary tree
  (pairwise cascade). `ReductionStats` on the result records `elements`,
  `chunks`, `combines` (always `elements - slices`) and `peak_workspace`
  (max simultaneously live partial slots, `O(log2 chunks)` per slice).
- **Aliasing**: gradients accumulate into storage shared by views, so
  overlapping views and repeated use of one tensor accumulate correctly.
- **Special values**: IEEE-754 — `inf`/`-inf` propagate, `inf + -inf = nan`,
  `nan` poisons all four reductions; nan extrema are marked
  non-differentiable.

## Verification

- `tests/test_enumeration.py` enumerates small domains exhaustively
  (`{-inf, -1, 0.5, inf}^4` over `(2,2)` with all 16 masks; `{-inf, -2, 0,
  1.5, inf}^3` over `(3,)` with all 8 masks, each under several chunkings)
  and checks values, gradients and nondiff marks against the independent
  reference — covering empty dims, all-mask slices, ±inf and ties.
- `tests/test_grad.py` adds central finite-difference checks, tie-policy
  cross-chunk consistency, higher-order behavior, and aliased gradient
  accumulation.
- `tests/test_reduce.py` covers axis rules, empty semantics, keepdims,
  permutation invariance, non-contiguous views, and workspace stats.

## Run

```bash
python3 -m unittest discover -s tests -t . -v   # test suite
python3 examples/demo.py                        # guided tour
```
