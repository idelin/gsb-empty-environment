/**
 * Per-column envelope verification: checks a downsampled output against the
 * raw series. Catches lost spikes, mis-ordered duplicate timestamps, dropped
 * single-point segments, broken evidence ranges and segment ids that would
 * let a renderer draw a line across a missing-value gap.
 */
import { bucketIndexRanges, makePlan, viewportIndexRange } from './columns.ts';
import type { TimeSeries } from './series.ts';
import type { SampledPoint } from './types.ts';

export interface EnvelopeViolation {
  kind:
    | 'order'
    | 'identity'
    | 'evidence'
    | 'spike'
    | 'endpoint'
    | 'single-point-segment'
    | 'duplicate-time'
    | 'gap-bridged';
  message: string;
  index?: number;
}

const isBoundary = (p: SampledPoint): boolean =>
  p.roles.includes('boundary-left') || p.roles.includes('boundary-right');

export function verifyEnvelope(
  series: TimeSeries,
  sampled: SampledPoint[],
  t0: number,
  t1: number,
  width: number,
): EnvelopeViolation[] {
  const violations: EnvelopeViolation[] = [];
  const plan = makePlan(t0, t1, width);
  const push = (kind: EnvelopeViolation['kind'], message: string, index?: number): void => {
    violations.push({ kind, message, index });
  };

  // 1. Output must be ordered by (time, original index).
  for (let i = 1; i < sampled.length; i++) {
    const a = sampled[i - 1];
    const b = sampled[i];
    if (a.time > b.time || (a.time === b.time && a.index > b.index)) {
      push('order', `output not sorted by (time, index) at output position ${i}`, b.index);
    }
  }

  // 2. Identity + evidence: every emitted point must match the raw series and
  //    sit inside its own evidence range, which must stay inside its segment.
  for (const p of sampled) {
    if (p.index < 0 || p.index >= series.length || !series.isValid(p.index)) {
      push('identity', `sampled index ${p.index} is not a valid raw point`, p.index);
      continue;
    }
    if (p.time !== series.times[p.index] || p.value !== series.values[p.index]) {
      push('identity', `sampled point ${p.index} does not match raw time/value`, p.index);
    }
    const seg = series.segmentAt(p.index);
    if (!seg || seg.id !== p.segmentId) {
      push('identity', `sampled point ${p.index} carries a wrong segmentId`, p.index);
    }
    if (!(p.rangeLo <= p.index && p.index <= p.rangeHi)) {
      push('evidence', `point ${p.index} lies outside its evidence range`, p.index);
    }
    if (seg && (p.rangeLo < seg.start || p.rangeHi > seg.end)) {
      push('evidence', `evidence range of point ${p.index} escapes its segment`, p.index);
    }
  }

  // 3. Per-column envelope: for every bucket, the raw minimum and maximum
  //    values (spikes) and the bucket's first/last indices must be present in
  //    the sampled output for that bucket.
  const interior = sampled.filter((p) => !isBoundary(p));
  const inBucket = (p: SampledPoint, segId: number, lo: number, hi: number): boolean =>
    p.segmentId === segId && p.index >= lo && p.index <= hi;
  for (const seg of series.segmentsIntersecting(t0, t1)) {
    const { lo, hi } = viewportIndexRange(series.times, seg.start, seg.end, plan);
    for (const range of bucketIndexRanges(series.times, lo, hi, plan)) {
      let minIdx = range.lo;
      let maxIdx = range.lo;
      for (let i = range.lo + 1; i <= range.hi; i++) {
        const v = series.values[i] as number;
        if (v < (series.values[minIdx] as number)) minIdx = i;
        if (v > (series.values[maxIdx] as number)) maxIdx = i;
      }
      const minV = series.values[minIdx] as number;
      const maxV = series.values[maxIdx] as number;
      const hasValue = (v: number): boolean =>
        interior.some((p) => inBucket(p, seg.id, range.lo, range.hi) && p.value === v);
      if (!hasValue(minV)) {
        push('spike', `column ${range.column} of segment ${seg.id} lost its minimum ${minV}`, minIdx);
      }
      if (!hasValue(maxV)) {
        push('spike', `column ${range.column} of segment ${seg.id} lost its maximum ${maxV}`, maxIdx);
      }
      if (!interior.some((p) => p.segmentId === seg.id && p.index === range.lo)) {
        push('endpoint', `column ${range.column} of segment ${seg.id} lost its first point`, range.lo);
      }
      if (!interior.some((p) => p.segmentId === seg.id && p.index === range.hi)) {
        push('endpoint', `column ${range.column} of segment ${seg.id} lost its last point`, range.hi);
      }
    }
    // 4. Single-point segments inside the viewport must survive.
    if (seg.start === seg.end && lo <= hi) {
      if (!sampled.some((p) => p.index === seg.start)) {
        push('single-point-segment', `single-point segment ${seg.id} was dropped`, seg.start);
      }
    }
  }

  // 5. Duplicate timestamps: equal times must appear in original index order.
  for (let i = 1; i < sampled.length; i++) {
    if (sampled[i - 1].time === sampled[i].time && sampled[i - 1].index >= sampled[i].index) {
      push('duplicate-time', `equal timestamps out of index order near index ${sampled[i].index}`, sampled[i].index);
    }
  }

  // 6. Gap integrity: two consecutive output points separated by missing raw
  //    values must carry different segment ids, so no line crosses the gap.
  for (let i = 1; i < sampled.length; i++) {
    const a = sampled[i - 1];
    const b = sampled[i];
    if (a.segmentId === b.segmentId) continue;
    let gapSeen = false;
    for (let k = a.index + 1; k < b.index; k++) {
      if (!series.isValid(k)) {
        gapSeen = true;
        break;
      }
    }
    if (!gapSeen) {
      // A segment id may only change across a missing-value gap; adjacent or
      // same-run valid indices always belong to one segment.
      push('gap-bridged', `segment id changes between ${a.index} and ${b.index} without a gap`, b.index);
    }
  }
  for (let i = 1; i < sampled.length; i++) {
    const a = sampled[i - 1];
    const b = sampled[i];
    if (a.segmentId !== b.segmentId) continue;
    for (let k = a.index + 1; k < b.index; k++) {
      if (!series.isValid(k)) {
        push('gap-bridged', `points ${a.index} and ${b.index} share a segment across a missing gap`, b.index);
        break;
      }
    }
  }

  return violations;
}
