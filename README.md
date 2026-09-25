# Rolling Noise-Floor Model (per scene, per band)

Python 3.11, standard library only. No audio devices: sample arrays and the
sample rate (`48_000 Hz`, hop `512`) come from local fixtures
(`noise_floor/fixtures.py`).

## Layout

- `noise_floor/quantile.py` — `SortedMultiset`: exact sliding-window quantile
  backbone. Hand-rolled binary search, every comparison counted; **no full
  re-sort per frame**. `oracle_quantile` (independent full sort) exists only
  for small-scale cross-checks.
- `noise_floor/model.py` — `BandModel` (one scene/band pair) and
  `NoiseFloorBank` (scene switching, lazy per-band models, persistence).
- `noise_floor/limits.py` — declared latency and numeric-tolerance contract.
- `noise_floor/replay.py` — deterministic, replayable op script.
- `tests/test_noise_floor.py` — `unittest` suite.

## Declared limits (`LIMITS`)

- Quantile absolute tolerance: `1e-9`.
- Update latency: `O(log n)` comparisons per add/mark/expiry (n = live
  window size); queries are `O(1)` index access on maintained sorted storage.
- Reclamation bound: retained history never reaches below
  `min(query_watermark, window_edge)`; in-window samples are never reclaimed.

## Semantics

- Samples are `valid` / `transient` / `missing`; only valid, in-window
  samples contribute. Late marks retract or restore contributions already
  incorporated — an extreme peak included then marked transient leaves the
  baseline exactly as if never included.
- Zero valid samples => estimate/threshold is `None` (not estimable), never 0.
- Marks and identical re-adds are idempotent (`noop`, no version bump).
- Every estimate/threshold carries the model `version`; `detect()` binds
  detections to the threshold version and `correct_detections()` re-decides
  stale detections after late marks.
- `set_watermark(frame)` reclaims expired history bounded by the active
  query watermark; marks against reclaimed samples report `unknown`.
- Frames may arrive out of order or in chunks; `to_dict`/`from_dict`
  round-trips preserve estimates, versions and future behaviour.

## Run

```sh
python3.11 -m unittest discover -s tests -v   # test suite
python3.11 -m noise_floor.replay              # replayable example, 4 modes
```

The replay runs the same op script in `sequential`, `chunked`, `shuffled`
and `persist` (save/restore each phase) modes and asserts identical query
logs, including a detection correction triggered by a late transient mark.
