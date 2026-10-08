/**
 * Flat per-pixel-column downsampling (the reference "full" implementation):
 * for every gap-delimited segment, each pixel column keeps its first, last,
 * minimum and maximum points. Output stays in original time order, with equal
 * timestamps ordered by original index. Missing gaps are never bridged:
 * every emitted point carries its segmentId.
 */
import {
  boundaryNeighbors,
  bucketIndexRanges,
  makePlan,
  viewportIndexRange,
} from './columns.ts';
import type { ColumnPlan } from './columns.ts';
import type { TimeSeries } from './series.ts';
import type { PointRole, SampledPoint, Segment } from './types.ts';

export interface BucketSummary {
  segmentId: number;
  column: number;
  /** Evidence: inclusive original-index range covered by this bucket. */
  lo: number;
  hi: number;
  firstIdx: number;
  lastIdx: number;
  minIdx: number;
  maxIdx: number;
}

/**
 * Assemble the final point list from bucket summaries plus boundary
 * neighbours. Points selected by several buckets/roles are emitted once with
 * all their roles; output is sorted by (time, index).
 */
export function emitPoints(
  series: TimeSeries,
  buckets: BucketSummary[],
  boundaries: Array<{ index: number; role: PointRole }>,
): SampledPoint[] {
  const byIndex = new Map<number, SampledPoint>();
  const add = (index: number, role: PointRole, lo: number, hi: number): void => {
    const seg = series.segmentAt(index);
    if (!seg) return; // never emit a missing point
    let pt = byIndex.get(index);
    if (!pt) {
      pt = {
        index,
        time: series.times[index],
        value: series.values[index] as number,
        segmentId: seg.id,
        roles: [],
        rangeLo: lo,
        rangeHi: hi,
      };
      byIndex.set(index, pt);
    }
    if (!pt.roles.includes(role)) pt.roles.push(role);
    // A point may summarize several buckets (e.g. min of two columns cannot
    // happen, but boundary + bucket can coincide at the viewport edge); keep
    // the union of the evidence ranges.
    pt.rangeLo = Math.min(pt.rangeLo, lo);
    pt.rangeHi = Math.max(pt.rangeHi, hi);
  };
  for (const b of buckets) {
    if (b.lo > b.hi) continue;
    add(b.firstIdx, 'first', b.lo, b.hi);
    add(b.lastIdx, 'last', b.lo, b.hi);
    add(b.minIdx, 'min', b.lo, b.hi);
    add(b.maxIdx, 'max', b.lo, b.hi);
  }
  for (const { index, role } of boundaries) {
    if (index >= 0) add(index, role, index, index);
  }
  const out = Array.from(byIndex.values());
  out.sort((a, b) => (a.time !== b.time ? a.time - b.time : a.index - b.index));
  return out;
}

/** Boundary neighbours of every segment intersecting the viewport. */
export function collectBoundaries(
  series: TimeSeries,
  plan: ColumnPlan,
): Array<{ index: number; role: PointRole }> {
  const out: Array<{ index: number; role: PointRole }> = [];
  for (const seg of series.segmentsIntersecting(plan.t0, plan.t1)) {
    const { left, right } = boundaryNeighbors(series.times, seg.start, seg.end, plan);
    if (left >= 0) out.push({ index: left, role: 'boundary-left' });
    if (right >= 0) out.push({ index: right, role: 'boundary-right' });
  }
  return out;
}

function scanBucket(series: TimeSeries, lo: number, hi: number): {
  firstIdx: number;
  lastIdx: number;
  minIdx: number;
  maxIdx: number;
} {
  let minIdx = lo;
  let maxIdx = lo;
  for (let i = lo + 1; i <= hi; i++) {
    const v = series.values[i] as number;
    const minV = series.values[minIdx] as number;
    const maxV = series.values[maxIdx] as number;
    if (v < minV) minIdx = i; // ties keep the smaller index
    if (v > maxV) maxIdx = i;
  }
  return { firstIdx: lo, lastIdx: hi, minIdx, maxIdx };
}

/**
 * Full downsampling by direct scan. This is the ground truth used to verify
 * the summary-tree viewport query point-for-point.
 */
export function downsampleFull(
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
      const s = scanBucket(series, range.lo, range.hi);
      buckets.push({ segmentId: seg.id, column: range.column, lo: range.lo, hi: range.hi, ...s });
    }
  }
  const boundaries = includeBoundary ? collectBoundaries(series, plan) : [];
  return emitPoints(series, buckets, boundaries);
}
