/**
 * Pixel-column bucketing shared by the flat (full) downsampler and the
 * summary-tree viewport query. Both paths derive bucket index ranges from
 * the SAME boundary computation so their outputs are identical point-for-point;
 * this is what makes "verify the LOD query against a full downsampling" exact.
 */
import type { Viewport } from './types.ts';

export interface ColumnPlan extends Viewport {
  /** Column width in time units: (t1 - t0) / width. */
  dt: number;
}

export function makePlan(t0: number, t1: number, width: number): ColumnPlan {
  if (!Number.isFinite(t0) || !Number.isFinite(t1) || !(t1 > t0)) {
    throw new RangeError(`invalid viewport [${t0}, ${t1}]`);
  }
  if (!Number.isInteger(width) || width < 1) {
    throw new RangeError(`width must be a positive integer, got ${width}`);
  }
  return { t0, t1, width, dt: (t1 - t0) / width };
}

/** First index i in [lo, hi] with times[i] >= target; hi + 1 if none. */
export function lowerBound(times: ArrayLike<number>, target: number, lo: number, hi: number): number {
  let a = lo;
  let b = hi + 1;
  while (a < b) {
    const mid = (a + b) >>> 1;
    if (times[mid] < target) a = mid + 1;
    else b = mid;
  }
  return a;
}

/** First index i in [lo, hi] with times[i] > target; hi + 1 if none. */
export function upperBound(times: ArrayLike<number>, target: number, lo: number, hi: number): number {
  let a = lo;
  let b = hi + 1;
  while (a < b) {
    const mid = (a + b) >>> 1;
    if (times[mid] <= target) a = mid + 1;
    else b = mid;
  }
  return a;
}

export interface BucketRange {
  column: number;
  /** Inclusive original-index range of points falling into this column. */
  lo: number;
  hi: number;
}

/**
 * Index range [lo, hi] (inclusive) of a segment's points inside the viewport.
 * Returns hi < lo when the segment does not intersect the viewport.
 */
export function viewportIndexRange(
  times: ArrayLike<number>,
  segStart: number,
  segEnd: number,
  plan: ColumnPlan,
): { lo: number; hi: number } {
  const lo = Math.max(segStart, lowerBound(times, plan.t0, segStart, segEnd));
  const hi = Math.min(segEnd, upperBound(times, plan.t1, segStart, segEnd) - 1);
  return { lo, hi };
}

/**
 * Per-column inclusive index ranges for the segment points inside [lo, hi].
 * The last column's right edge is inclusive (a point exactly at t1 belongs to
 * the last column); all other column right edges are exclusive. Boundaries are
 * computed identically for every caller, so flat scans and tree queries agree.
 */
export function bucketIndexRanges(
  times: ArrayLike<number>,
  lo: number,
  hi: number,
  plan: ColumnPlan,
): BucketRange[] {
  const out: BucketRange[] = [];
  if (lo > hi) return out;
  const { t0, t1, width, dt } = plan;
  const c0 = Math.min(width - 1, Math.max(0, Math.floor((times[lo] - t0) / dt)));
  const c1 = Math.min(width - 1, Math.max(0, Math.floor((times[hi] - t0) / dt)));
  for (let c = c0; c <= c1; c++) {
    // The first/last spanned columns are clamped to [lo, hi] so that every
    // point belongs to exactly one bucket even when t0 + c * dt rounding
    // disagrees with the floor() used to find c0/c1. Interior boundaries are
    // single lowerBound cuts, so buckets never overlap and never leave gaps.
    const bLo = c === c0 ? lo : Math.max(lo, lowerBound(times, t0 + c * dt, lo, hi));
    const bHi = c === c1 ? hi : Math.min(hi, lowerBound(times, t0 + (c + 1) * dt, lo, hi) - 1);
    if (bLo <= bHi) out.push({ column: c, lo: bLo, hi: bHi });
  }
  return out;
}

/**
 * Nearest in-segment points just outside the viewport, kept so a renderer can
 * connect lines across the viewport edge. -1 means "no such point".
 */
export function boundaryNeighbors(
  times: ArrayLike<number>,
  segStart: number,
  segEnd: number,
  plan: ColumnPlan,
): { left: number; right: number } {
  const leftCand = lowerBound(times, plan.t0, segStart, segEnd) - 1;
  const rightCand = upperBound(times, plan.t1, segStart, segEnd);
  return {
    left: leftCand >= segStart ? leftCand : -1,
    right: rightCand <= segEnd ? rightCand : -1,
  };
}
