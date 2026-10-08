# segmented-downsample

TypeScript time-series downsampling for rendering: per-pixel-column selection
that respects missing-value gaps, backed by a shared summary tree so every
zoom level reuses one segmented summary, with a version-guarded async
scheduler for zoom tasks.

Requires Node >= 22.6 (run with `--experimental-strip-types`) or >= 23.6
(native TypeScript type stripping). Zero dependencies.

```
npm test
```

## Model

- Input is `times[]` (non-decreasing; duplicates allowed) and `values[]`
  where `null` / `undefined` / `NaN` marks a missing point.
- Missing points split the series into **segments**: maximal runs of valid
  points. A rendered polyline must never cross a segment boundary, so every
  output point carries its `segmentId`.
- Output is always ordered by `(time, originalIndex)` — equal timestamps keep
  their original index order.

## Per-column downsampling

`downsampleFull(series, t0, t1, width)` (flat reference) and
`queryViewport(series, t0, t1, width)` (summary-tree LOD) both keep, for each
segment and each pixel column: the **first**, **last**, **minimum** and
**maximum** points (extrema ties resolve to the smaller original index).
Both paths derive bucket index ranges from the same boundary computation in
`src/columns.ts`, so the LOD result is identical to a full downsampling —
verified point-for-point in `test/viewport.test.ts`.

Every emitted `SampledPoint` carries **evidence**: `rangeLo`/`rangeHi`, the
inclusive original-index range of the column bucket it summarizes. Viewport
queries additionally emit the nearest in-segment point just outside each
viewport edge (`boundary-left` / `boundary-right` roles) so lines can connect
across the edge.

## Shared summaries, edits, versions

- `TimeSeries` maintains the segment list and a `SummaryTree` (per-node
  count/first/last/min/max over valid points). All zoom levels query this one
  structure.
- `setValue` / `setValues` apply local edits: column extrema re-propagate in
  O(log n) and segment breakpoints split/merge incrementally. Each mutation
  bumps `series.version`.
- `ZoomScheduler` computes zoom levels asynchronously. A task captures the
  version on start; when it finishes, the result is published to the cache
  **only if the version is unchanged** — a stale task can never overwrite
  newer data. Cache lookups also require a matching version.

## Verification

`verifyEnvelope(series, sampled, t0, t1, width)` checks a downsampled output
against the raw series: per-column min/max (spike) and endpoint presence,
`(time, index)` ordering, duplicate-timestamp order, single-point segment
survival, evidence-range containment, and gap integrity (no segment id may
span a missing-value gap).

## Layout

- `src/types.ts` — shared types (`SampledPoint`, `Segment`, roles)
- `src/columns.ts` — pixel-column bucketing shared by both query paths
- `src/segments.ts` — segment computation + incremental repair
- `src/tree.ts` — summary tree (shared by all zoom levels)
- `src/series.ts` — `TimeSeries`: data, segments, tree, version
- `src/downsample.ts` — flat full downsampler (reference) + output assembly
- `src/viewport.ts` — summary-tree viewport query (LOD)
- `src/scheduler.ts` — async zoom scheduler with version guard + cache
- `src/envelope.ts` — per-column envelope verification
