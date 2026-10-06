import { buildBuckets } from './buckets.ts';
import { visibleColumnRange } from './downsample.ts';
import { computeSegments, isMissing } from './segments.ts';
import type { DataPoint, SampledPoint } from './types.ts';

export interface EnvelopeOptions {
  columnWidth: number;
  origin?: number;
  fromTime?: number;
  toTime?: number;
}

export interface EnvelopeViolation {
  segmentId: number;
  column: number | null;
  kind: 'min' | 'max' | 'first' | 'last';
  /** Expected envelope value (min/max) or original index (first/last). */
  expected: number;
  /** Actual sampled envelope value, or null when the point is missing. */
  actual: number | null;
}

/**
 * Per-column envelope check: recomputes the exact min/max envelope of the
 * raw data for every visible column and verifies the sampled output reaches
 * it. Also verifies the first/last visible point of each segment is kept.
 * This is what guarantees spikes survive downsampling.
 */
export function verifyEnvelope(
  points: readonly DataPoint[],
  sampled: readonly SampledPoint[],
  options: EnvelopeOptions,
): EnvelopeViolation[] {
  const origin = options.origin ?? 0;
  const [colMin, colMax] = visibleColumnRange(
    options.fromTime, options.toTime, options.columnWidth, origin,
  );
  const violations: EnvelopeViolation[] = [];

  const sampledBySegmentColumn = new Map<string, SampledPoint[]>();
  for (const sp of sampled) {
    if (sp.column < colMin || sp.column > colMax) continue; // edge neighbors
    const key = `${sp.segmentId}:${sp.column}`;
    const group = sampledBySegmentColumn.get(key);
    if (group) group.push(sp);
    else sampledBySegmentColumn.set(key, [sp]);
  }

  for (const segment of computeSegments(points)) {
    const buckets = buildBuckets(
      points, segment.startIndex, segment.endIndex,
      options.columnWidth, origin, colMin, colMax,
    );
    if (buckets.length === 0) continue;
    for (const bucket of buckets) {
      const group = sampledBySegmentColumn.get(`${segment.id}:${bucket.column}`) ?? [];
      const values = group.map((sp) => sp.value);
      const sampledMin = values.length > 0 ? Math.min(...values) : null;
      const sampledMax = values.length > 0 ? Math.max(...values) : null;
      if (sampledMin !== bucket.minValue) {
        violations.push({
          segmentId: segment.id, column: bucket.column, kind: 'min',
          expected: bucket.minValue, actual: sampledMin,
        });
      }
      if (sampledMax !== bucket.maxValue) {
        violations.push({
          segmentId: segment.id, column: bucket.column, kind: 'max',
          expected: bucket.maxValue, actual: sampledMax,
        });
      }
    }
    let firstVisible = Infinity;
    let lastVisible = -Infinity;
    for (const bucket of buckets) {
      if (bucket.firstIndex < firstVisible) firstVisible = bucket.firstIndex;
      if (bucket.lastIndex > lastVisible) lastVisible = bucket.lastIndex;
    }
    if (!sampled.some((sp) => sp.segmentId === segment.id && sp.index === firstVisible)) {
      violations.push({
        segmentId: segment.id, column: null, kind: 'first',
        expected: firstVisible, actual: null,
      });
    }
    if (!sampled.some((sp) => sp.segmentId === segment.id && sp.index === lastVisible)) {
      violations.push({
        segmentId: segment.id, column: null, kind: 'last',
        expected: lastVisible, actual: null,
      });
    }
  }
  return violations;
}

/** Group an ordered sampled output into consecutive runs of one segment id. */
export function splitIntoRuns(sampled: readonly SampledPoint[]): SampledPoint[][] {
  const runs: SampledPoint[][] = [];
  for (const sp of sampled) {
    const last = runs[runs.length - 1];
    if (last && last[0].segmentId === sp.segmentId) last.push(sp);
    else runs.push([sp]);
  }
  return runs;
}

/**
 * Gap-safety check: no polyline run may span a missing value, and every
 * point's segment tag must match the segment its original index belongs to.
 */
export function verifyNoGapCrossing(
  points: readonly DataPoint[],
  sampled: readonly SampledPoint[],
): string[] {
  const problems: string[] = [];
  const segments = computeSegments(points);
  const segmentOfIndex = (index: number): number | null => {
    for (const seg of segments) {
      if (index >= seg.startIndex && index < seg.endIndex) return seg.id;
    }
    return null;
  };
  for (const sp of sampled) {
    const actual = segmentOfIndex(sp.index);
    if (actual === null) {
      problems.push(`sampled index ${sp.index} falls on a missing value`);
    } else if (actual !== sp.segmentId) {
      problems.push(`sampled index ${sp.index} tagged segment ${sp.segmentId}, actual ${actual}`);
    }
  }
  for (const run of splitIntoRuns(sampled)) {
    let lo = Infinity;
    let hi = -Infinity;
    for (const sp of run) {
      if (sp.index < lo) lo = sp.index;
      if (sp.index > hi) hi = sp.index;
    }
    for (let i = lo; i <= hi; i++) {
      if (isMissing(points[i].value)) {
        problems.push(
          `run of segment ${run[0].segmentId} spans missing value at index ${i} (indices ${lo}..${hi})`,
        );
      }
    }
  }
  return problems;
}
