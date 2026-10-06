# time-aligned-stack

TypeScript stacking library that aligns series by time, stacks signed values
from a zero baseline, and publishes revisioned snapshots through a
barrier-synchronized dataflow.

## Core stacking (`stackSeries`)

- **Time alignment**: columns are the sorted union of all visible series'
  timestamps (numeric or string keys).
- **Missing vs zero**: an absent point or an explicit `null` is *missing* —
  it occupies no layer, moves no cursor, and is flagged `missing: true`.
  A real `0` is a zero-height layer at the current cursor, `missing: false`.
- **Signed stacking**: positive values accumulate upward from zero, negative
  values downward from zero, each in input series order.
- **Percent mode**: positive values are normalized by the column's positive
  sum, negative values by the column's absolute negative sum — the two sides
  are normalized independently.
- **Output**: every point carries `{ lower, upper, missing, value }`; every
  column carries `posTotal` / `negTotal` / `posExtent` / `negExtent`.

## Conservation checks (`src/conservation.ts`)

- `checkAllZeroColumn` — an all-zero column leaves every layer at the baseline.
- `checkOrderInvariance` — switching series order preserves each column's
  positive/negative extents and the multiset of layer heights.
- `checkHiddenEquivalence` — hiding a series is indistinguishable from
  removing it from the input.
- `checkPercentConservation` — positive layers sum to 1 (or 0) per column,
  negative layers to -1 (or 0).

## Revision dataflow (`RevisionStacker`)

Each required series publishes data in revisioned batches
(`applyBatch(seriesId, revision, points)`); revisions are unique per series
and batches from different series may arrive in any order.

- **Barrier publication**: a time column is published only once every
  required, non-hidden series has reported it. The column is published at the
  *barrier* — the minimum of those series' latest covering revisions — and
  every value is read as-of the barrier, so a column never mixes one series'
  newer data with another's older data (no half-column updates). Columns that
  have not reached the barrier are listed in `pendingTimes`.
- **Retraction**: `retractSeries(id)` removes the series, recomputes every
  layer, and returns a replacement publication with a fresh, monotonically
  increasing `version`. A retracted series can never contribute again.
- **Hide / unhide**: hidden series neither stack nor gate the barrier;
  unhiding restores the series and its barrier duties.

`matrixPublish` (`src/matrixModel.ts`) is an independent full-matrix
reference model: it materializes the dense series × time matrix of all
(revision, value) entries and derives barriers by whole-column scans. The
fuzz test drives randomized event streams (out-of-order batches, hides,
unhides, retractions) through both implementations and asserts identical
publications.

## Requirements & tests

No runtime dependencies. Requires Node.js ≥ 22.6 (native TypeScript type
stripping; Node ≥ 23.6 needs no flags).

```sh
npm test
```
