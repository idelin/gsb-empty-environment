import { bucketColumn, buildBuckets } from './buckets.ts';
import { computeSegments } from './segments.ts';
import type {
  ColumnBucket,
  DataPoint,
  SampledPoint,
  SampleRole,
  Segment,
} from './types.ts';

export interface DownsampleParams {
  /** Explicit column width in time units. Takes precedence over `columns`. */
  columnWidth?: number;
  /** Pixel column count; requires finite fromTime/toTime to derive the width. */
  columns?: number;
  /** Viewport start (inclusive). Omit for an unbounded (full-domain) query. */
  fromTime?: number;
  /** Viewport end (exclusive). Omit for an unbounded (full-domain) query. */
  toTime?: number;
  /**
   * Grid origin the columns align to. Defaults to `fromTime` when finite,
   * else 0. Pass a fixed origin to keep viewports aligned with full-domain
   * results (this is what the multi-zoom engine does).
   */
  origin?: number;
  /** Also emit the in-segment neighbors just outside the viewport edges. */
  includeEdgeNeighbors?: boolean;
}

export function resolveColumnWidth(params: DownsampleParams): number {
  if (params.columnWidth !== undefined) {
    if (!(params.columnWidth > 0)) throw new RangeError('columnWidth must be > 0');
    return params.columnWidth;
  }
  const { columns, fromTime, toTime } = params;
  if (
    columns !== undefined && columns > 0 &&
    fromTime !== undefined && toTime !== undefined &&
    Number.isFinite(fromTime) && Number.isFinite(toTime) && toTime > fromTime
  ) {
    return (toTime - fromTime) / columns;
  }
  throw new Error('Provide either columnWidth, or columns together with finite fromTime/toTime');
}

/** Inclusive [colMin, colMax] of pixel columns intersecting [fromTime, toTime). */
export function visibleColumnRange(
  fromTime: number | undefined,
  toTime: number | undefined,
  columnWidth: number,
  origin: number,
): [number, number] {
  const colMin = fromTime !== undefined && Number.isFinite(fromTime)
    ? Math.floor((fromTime - origin) / columnWidth)
    : -Infinity;
  const colMax = toTime !== undefined && Number.isFinite(toTime)
    ? Math.ceil((toTime - origin) / columnWidth) - 1
    : Infinity;
  return [colMin, colMax];
}

/**
 * Turn the visible buckets of one segment into sampled points:
 * per-column min/max, the segment's first/last visible point, and
 * optionally the in-segment neighbors just outside the visible range.
 * Points selected for several roles are merged; every point carries the
 * original-index range it stands for as evidence.
 */
export function assembleSegment(
  points: readonly DataPoint[],
  segment: Segment,
  visibleBuckets: readonly ColumnBucket[],
  includeEdgeNeighbors: boolean,
  columnOf: (time: number) => number,
): SampledPoint[] {
  if (visibleBuckets.length === 0) return [];
  const byIndex = new Map<number, SampledPoint>();
  const add = (index: number, role: SampleRole): void => {
    const existing = byIndex.get(index);
    if (existing) {
      if (!existing.roles.includes(role)) existing.roles.push(role);
      return;
    }
    byIndex.set(index, {
      index,
      time: points[index].time,
      value: points[index].value as number,
      segmentId: segment.id,
      column: columnOf(points[index].time),
      roles: [role],
      evidence: { fromIndex: index, toIndex: index },
    });
  };

  let firstVisible = Infinity;
  let lastVisible = -Infinity;
  for (const bucket of visibleBuckets) {
    add(bucket.minIndex, 'min');
    add(bucket.maxIndex, 'max');
    // A min/max point stands for its whole column bucket: widen the evidence.
    for (const idx of [bucket.minIndex, bucket.maxIndex]) {
      const sp = byIndex.get(idx)!;
      sp.evidence = {
        fromIndex: Math.min(sp.evidence.fromIndex, bucket.firstIndex),
        toIndex: Math.max(sp.evidence.toIndex, bucket.lastIndex),
      };
    }
    if (bucket.firstIndex < firstVisible) firstVisible = bucket.firstIndex;
    if (bucket.lastIndex > lastVisible) lastVisible = bucket.lastIndex;
  }
  add(firstVisible, 'first');
  add(lastVisible, 'last');
  if (includeEdgeNeighbors) {
    // Neighbors stay inside the same segment, so lines never cross a gap.
    if (firstVisible - 1 >= segment.startIndex) add(firstVisible - 1, 'edge-before');
    if (lastVisible + 1 < segment.endIndex) add(lastVisible + 1, 'edge-after');
  }
  return [...byIndex.values()];
}

/**
 * Downsample a time series with missing values.
 *
 * Keeps, per pixel column and segment, the min and max points, plus the
 * first/last visible point of every segment. Output is ordered by original
 * time; equal timestamps fall back to the original index. Every output point
 * is tagged with its segment id so missing-value gaps are never connected.
 */
export function downsample(
  points: readonly DataPoint[],
  params: DownsampleParams = {},
): SampledPoint[] {
  const columnWidth = resolveColumnWidth(params);
  const origin = params.origin ??
    (params.fromTime !== undefined && Number.isFinite(params.fromTime) ? params.fromTime : 0);
  const [colMin, colMax] = visibleColumnRange(params.fromTime, params.toTime, columnWidth, origin);
  const columnOf = (time: number): number => bucketColumn(time, columnWidth, origin);
  const includeEdgeNeighbors = params.includeEdgeNeighbors ?? false;

  const out: SampledPoint[] = [];
  for (const segment of computeSegments(points)) {
    const buckets = buildBuckets(
      points, segment.startIndex, segment.endIndex, columnWidth, origin, colMin, colMax,
    );
    out.push(...assembleSegment(points, segment, buckets, includeEdgeNeighbors, columnOf));
  }
  // Original time order; identical timestamps keep the original index order.
  out.sort((a, b) => a.time - b.time || a.index - b.index);
  return out;
}
