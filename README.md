# Multi-resolution min/max/energy index

Pure Python 3.11, standard library only. Sample arrays and sample rates
come from local fixtures (`fixtures.py`); no audio device is touched.

## Design

An immutable AVL concat tree (balanced rope) over the sample array. Every
node caches an exact aggregate of its subtree: min, max, sum, sum of
squares, valid count, missing count. Each tree level is therefore a coarser
resolution of the signal. Aggregates keep extrema, never averages, so a
single-sample spike stays visible at every level.

Immutability gives versioned edits for free: an edit copies only the
O(log n) nodes on the affected path and shares everything else with older
versions, so `undo()` is O(1) and the copy counter proves maintenance is
local.

## API

- `MultiResIndex(samples, sample_rate)` — build; `sample_rate` may be a `Fraction`.
- `summary(start, end)` — count, missing, min, max, energy, mean, rms,
  boundary info (`first_index`, `last_index`, `covers_full_series`).
  Edges are exact: partial buckets are descended to individual samples.
- `peaks(start, end, kind="max"|"min")` — `(value, [(s, e), ...])` with
  every tied source range; `(None, [])` for empty/all-missing ranges.
- `buckets(start, end, width)` — grid-aligned buckets; edge buckets carry
  `coverage` and `partial=True` and are never treated as full buckets.
- `replace(start, end, samples)` / `insert(pos, samples)` / `cut(start, end)`
  — versioned edits; `undo()` restores the previous version.
- `resample(num, den)` / `change_rate(num, den)` — rational sample-rate
  change with exact `Fraction` positions and linear interpolation.
- `last_op` — `OpStats(visits, copies)` of the last operation.

## Declared latency

| operation                    | cost                         |
|------------------------------|------------------------------|
| build                        | O(n) time, ~2n node copies   |
| range summary / energy       | O(log n) node visits         |
| peak source collection       | O(log n + t), t = tied hits  |
| cut / insert / replace       | O(log n + k) node copies     |
| undo                         | O(1)                         |

Measured on this machine (n = 100,000): build ≈ 2.3 s, 100-sample range
query ≈ 0.2 ms (33 node visits), 10-sample replace ≈ 0.9 ms (≈ 73 copies
out of ≈ 200,000 nodes).

## Numeric tolerance

- min / max / counts: exact.
- energy / mean / rms: relative 1e-9 (floating-point summation order only).
- resample: absolute 1e-9 (exact rational positions, linear interpolation).

## Run

```
python3.11 demo.py            # run example
python3.11 -m unittest -v     # test suite (15 tests)
```
