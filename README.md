# ts-segment-downsample

Segment-aware min/max-per-pixel-column downsampling for time series with
missing values, written in dependency-free TypeScript.

## Guarantees

- **Segments**: `null` / `undefined` / `NaN` values split the series into
  segments. Every sampled point carries a `segmentId`; polylines must break
  when it changes, so missing gaps are never connected (`verifyNoGapCrossing`).
- **Pixel-column envelope**: per column and segment, the min and max points
  are kept, plus the first/last visible point of every segment. Spikes
  survive (`verifyEnvelope` recomputes the exact raw envelope per column).
- **Original order**: output is sorted by original time; identical
  timestamps fall back to the original index.
- **Evidence**: every output point keeps the original-index range it stands
  for (`evidence.fromIndex`/`toIndex` = its column bucket's index span).
- **Viewport edges**: viewport queries additionally emit the in-segment
  neighbor points just outside the edges (`edge-before` / `edge-after`) so
  lines connect across the viewport boundary. Columns align to a fixed
  `origin`, so viewport extremes match a full-domain downsample exactly.

## Multi-zoom engine

`DownsampleEngine` adds zoom levels and safe asynchronous recomputation:

- Level `k` uses column width `baseColumnWidth * 2^k` on a shared grid.
- Each segment owns one lazily built summary shared by all zoom levels and
  viewports: level 0 is computed from raw points, coarser levels are merged
  from the previous level (identical to direct computation).
- `updateValues(start, values)` performs a local edit: it bumps `version`,
  recomputes breakpoints, and invalidates only the summaries of the
  segments the edit touched — untouched segments keep their cached levels.
- `scheduleDownsample` captures an immutable snapshot; `flushPending`
  completes pending tasks. A task whose version is no longer current
  resolves with `stale: true` and never overwrites a newer applied result
  (`getLatestResult`), regardless of completion order.

## Usage

```ts
import { DownsampleEngine, downsample, verifyEnvelope } from './src/index.ts';

const sampled = downsample(points, { columnWidth: 60, origin: 0 });
const view = downsample(points, {
  columnWidth: 60, origin: 0, fromTime: t0, toTime: t1, includeEdgeNeighbors: true,
});

const engine = new DownsampleEngine(points, { baseColumnWidth: 1, origin: 0 });
engine.updateValues(120, [null]);        // local edit, bumps version
const task = engine.scheduleDownsample(3, { fromTime: t0, toTime: t1 });
engine.flushPending();                    // complete async zoom tasks
const { stale, result } = await task;     // apply only when !stale
```

## Tests

Requires Node.js >= 23.6 (native TypeScript type stripping):

```sh
node --test
```

Covers per-column envelope checks (spikes, duplicate timestamps,
single-point segments), gap-crossing safety, index-range evidence, viewport
edge neighbors cross-checked against full-domain downsampling, shared
multi-level summaries, local-edit invalidation, stale-async-task
protection, and a deterministic randomized property test.
