# timeproj — exact index projection onto piecewise sampled streams

Python 3.11, standard library only (`fractions`, `math`, `dataclasses`,
`unittest`).  All time arithmetic is exact (`Fraction`); nothing is ever
rounded through floats.

## Concepts

- **Three kinds of time, kept type-safe** (`timeproj/types.py`):
  `WallClockTime` (a reading on a named clock), `TimePoint` (absolute
  instant), `Duration` (a length of time).  Mixing a wall-clock reading
  with an absolute time point raises `TypeError` — a calibration table is
  required to cross over.
- **`Interval`** has individually open/closed endpoints; intersection and
  subtraction preserve openness exactly.
- **`SamplingBlock`** (`timeproj/blocks.py`): one epoch of constant
  *rational* sample rate covering `[start, end)`, with a set of `missing`
  (dropped) sample indices.  Blocks may overlap.

## Projection (`timeproj/projection.py`)

`project(query, blocks)` returns a `ProjectionResult` — honestly not a
contiguous array:

- `slices`: `IndexSlice` runs `[start_index, end_index)` per block;
- `gaps`: query regions with no coverage at all (reported, **never
  zero-filled**);
- `missing`: samples dropped at the source (cut out of the slices);
- `dedupe`: when two blocks stamp the same instant, the higher
  `(epoch, version)` wins; coincidences across *different* rational rates
  are found by solving the exact integer Diophantine equation
  `k1*p1 - k2*p2 = origin2 - origin1`;
- `evidence`: per-endpoint `RoundingEvidence` (requested value, exact
  rational grid coordinate, ceil/floor direction, resulting index).

Exactness consequences verified by tests: `[0,1]` at period 1 contains two
samples, `[0,1)` one; ceil/floor are correct for negative times.

`result.reverse()` maps each slice back to the interval it actually covers
(`[first_ts, last_ts]`, plus the Voronoi cell) together with the forward
rounding evidence.

## Calibration (`timeproj/calibration.py`)

`OffsetTable` is a synthetic, piecewise-linear, exactly interpolated
offset table (`corrected = raw + offset(raw)`), versioned in a
`CalibrationStore`.  `CalibratedProjector` caches per-block slices per raw
query; after a **local** correction only blocks intersecting the moved
corrected-query endpoints are recomputed, everything else is reused
object-for-object.  A `Cursor` opened earlier keeps the historical table
version forever.

## Composition (`timeproj/compose.py`)

`compose(query, A, B)` = time → A-indices → actually-covered time →
B-indices.  Because every step is exact, multi-layer composition equals
the direct mapping (tested for two and three layers).

## Run

```sh
python3 -m unittest discover -s tests -t . -v   # 36 tests
python3 examples/demo.py                        # guided tour
```

## Layout

```
timeproj/
  types.py        WallClockTime / TimePoint / Duration / Interval
  blocks.py       SamplingBlock (epoch, version, rational rate, missing)
  projection.py   project(), slices+gaps+dedupe+evidence, reverse()
  calibration.py  OffsetTable, CalibrationStore, CalibratedProjector, Cursor
  compose.py      compose(), project_slices()
tests/            unittest suite
examples/demo.py  runnable end-to-end demo
```
