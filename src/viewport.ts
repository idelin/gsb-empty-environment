/**
 * Viewport query served from the shared summary tree instead of a raw scan.
 * Produces exactly the same points as downsampleFull, but each pixel column
 * costs O(log n) regardless of how many raw points fall into it, so every
 * zoom level reuses the one segmented summary.
 */
import { bucketIndexRanges, makePlan, viewportIndexRange } from './columns.ts';
import { collectBoundaries, emitPoints } from './downsample.ts';
import type { BucketSummary } from './downsample.ts';
import type { TimeSeries } from './series.ts';
import type { SampledPoint } from './types.ts';

export function queryViewport(
  series: TimeSeries,
  t0: number,
  t1: number,
  width: number,
  includeBoundary = true,
): SampledPoint[] {
  const plan = makePlan(t0, t1, width);
  const buckets: BucketSummary[] = [];
  for (const seg of series.segmentsIntersecting(t0, t1)) {
    const { lo, hi } = viewportIndexRange(series.times, seg.start, seg.end, plan);
    for (const range of bucketIndexRanges(series.times, lo, hi, plan)) {
      const s = series.queryRange(range.lo, range.hi);
      if (s.count === 0) continue;
      buckets.push({
        segmentId: seg.id,
        column: range.column,
        lo: range.lo,
        hi: range.hi,
        firstIdx: s.firstIdx,
        lastIdx: s.lastIdx,
        minIdx: s.minIdx,
        maxIdx: s.maxIdx,
      });
    }
  }
  const boundaries = includeBoundary ? collectBoundaries(series, plan) : [];
  return emitPoints(series, buckets, boundaries);
}
