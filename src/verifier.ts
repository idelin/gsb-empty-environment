// Independent result verifier.
//
// verifyQueryResult does NOT trust the index. It:
//   1. Enumerates every segment of the referenced version directly
//      (bypassing the BVH) and recomputes hit intervals with its own
//      independent rational solver (recomputeHitInterval below).
//   2. Geometrically spot-checks each hit: the position at the interval
//      start (and at the end, when inclusive) must lie inside the box.
//   3. Recomputes the per-object merge and compares interval-by-interval.
//   4. Re-derives every reported source interval from the raw segment
//      data (provenance check) and verifies that the sources of a merged
//      interval actually cover it with no gap.
//   5. Checks merged intervals are sorted and pairwise disjoint.
//   6. Cross-checks the access statistics for internal consistency.
//
// It never settles for "the returned object exists".

import {
  makeFraction,
  fractionFromInt,
  fracCompare,
  fracAdd,
  fracToString,
} from './fraction.js';
import type { Fraction } from './fraction.js';
import type { Segment, Box3, TimeWindow } from './geometry.js';
import { positionAt } from './intersect.js';
import type { TimeInterval, HitSource, MergedInterval } from './intersect.js';
import { mergeIntervals } from './merge.js';
import type { SpatioTemporalIndex, QueryResult } from './index.js';

export interface VerificationReport {
  ok: boolean;
  errors: string[];
  checkedObjects: number;
  checkedSources: number;
}

// Independent re-implementation of the exact segment/box intersection.
// Written separately from intersect.ts: it solves for u = t - t0 per axis
// and combines the per-axis slabs with explicit min/max, then clips to the
// half-open segment/window domain.
function recomputeHitInterval(seg: Segment, box: Box3, win: TimeWindow): TimeInterval | null {
  const L = seg.t1 - seg.t0;
  const p0 = [seg.p0.x, seg.p0.y, seg.p0.z];
  const p1 = [seg.p1.x, seg.p1.y, seg.p1.z];
  const lo = [box.lo.x, box.lo.y, box.lo.z];
  const hi = [box.hi.x, box.hi.y, box.hi.z];
  let start = fractionFromInt(seg.t0 > win.t0 ? seg.t0 : win.t0);
  const limit = fractionFromInt(seg.t1 < win.t1 ? seg.t1 : win.t1);
  let closedEnd: Fraction | null = null;
  for (let i = 0; i < 3; i++) {
    const d = p1[i] - p0[i];
    if (d === 0n) {
      if (p0[i] < lo[i] || p0[i] > hi[i]) {
        return null;
      }
      continue;
    }
    const u1 = makeFraction((lo[i] - p0[i]) * L, d);
    const u2 = makeFraction((hi[i] - p0[i]) * L, d);
    const uEnter = fracCompare(u1, u2) <= 0 ? u1 : u2;
    const uExit = fracCompare(u1, u2) <= 0 ? u2 : u1;
    const tEnter = fracAdd(fractionFromInt(seg.t0), uEnter);
    const tExit = fracAdd(fractionFromInt(seg.t0), uExit);
    if (fracCompare(tEnter, start) > 0) {
      start = tEnter;
    }
    if (closedEnd === null || fracCompare(tExit, closedEnd) < 0) {
      closedEnd = tExit;
    }
  }
  if (closedEnd !== null && fracCompare(closedEnd, limit) < 0) {
    if (fracCompare(start, closedEnd) > 0) {
      return null;
    }
    return { start: start, end: closedEnd, endInclusive: true };
  }
  if (fracCompare(start, limit) >= 0) {
    return null;
  }
  return { start: start, end: limit, endInclusive: false };
}

function intervalEqual(a: TimeInterval, b: TimeInterval): boolean {
  return (
    fracCompare(a.start, b.start) === 0 &&
    fracCompare(a.end, b.end) === 0 &&
    a.endInclusive === b.endInclusive
  );
}

function intervalToString(iv: TimeInterval): string {
  return '[' + fracToString(iv.start) + ', ' + fracToString(iv.end) + (iv.endInclusive ? ']' : ')');
}

function checkPointInBox(
  seg: Segment,
  t: Fraction,
  box: Box3,
  errors: string[],
  label: string
): void {
  const p = positionAt(seg, t);
  const xs = [p.x, p.y, p.z];
  const los = [box.lo.x, box.lo.y, box.lo.z];
  const his = [box.hi.x, box.hi.y, box.hi.z];
  const names = ['x', 'y', 'z'];
  for (let i = 0; i < 3; i++) {
    if (fracCompare(xs[i], fractionFromInt(los[i])) < 0 || fracCompare(xs[i], fractionFromInt(his[i])) > 0) {
      errors.push(
        'geometric check failed for segment ' + seg.segmentId + ' at ' + label +
        ' t=' + fracToString(t) + ': ' + names[i] + '=' + fracToString(xs[i]) +
        ' outside [' + los[i].toString() + ', ' + his[i].toString() + ']'
      );
    }
  }
}

function checkIntervalBounds(
  iv: TimeInterval,
  seg: Segment,
  win: TimeWindow,
  errors: string[],
  context: string
): void {
  if (fracCompare(iv.start, iv.end) > 0) {
    errors.push(context + ': interval start > end: ' + intervalToString(iv));
  }
  if (fracCompare(iv.start, iv.end) === 0 && !iv.endInclusive) {
    errors.push(context + ': degenerate empty interval ' + intervalToString(iv));
  }
  const lo = fractionFromInt(seg.t0 > win.t0 ? seg.t0 : win.t0);
  const hi = fractionFromInt(seg.t1 < win.t1 ? seg.t1 : win.t1);
  if (fracCompare(iv.start, lo) < 0) {
    errors.push(context + ': interval starts before segment/window domain: ' + intervalToString(iv));
  }
  if (fracCompare(iv.end, hi) > 0) {
    errors.push(context + ': interval ends after segment/window domain: ' + intervalToString(iv));
  }
  if (!iv.endInclusive && fracCompare(iv.end, hi) !== 0) {
    errors.push(context + ': half-open interval end must equal the domain limit: ' + intervalToString(iv));
  }
}

export function verifyQueryResult(index: SpatioTemporalIndex, result: QueryResult): VerificationReport {
  const errors: string[] = [];
  let segments: Segment[] = [];
  try {
    segments = index.getVersionSegments(result.version);
  } catch (e) {
    errors.push('cannot resolve result version ' + result.version + ': ' + String(e));
    return { ok: false, errors: errors, checkedObjects: 0, checkedSources: 0 };
  }
  const byId: Map<string, Segment> = new Map();
  for (const seg of segments) {
    byId.set(seg.segmentId, seg);
  }

  // 1 + 2: independent brute-force recompute with geometric spot checks.
  const expectedByObject: Map<string, HitSource[]> = new Map();
  for (const seg of segments) {
    const iv = recomputeHitInterval(seg, result.box, result.window);
    if (iv === null) {
      continue;
    }
    checkIntervalBounds(iv, seg, result.window, errors, 'recomputed hit of ' + seg.segmentId);
    checkPointInBox(seg, iv.start, result.box, errors, 'interval start');
    if (iv.endInclusive) {
      checkPointInBox(seg, iv.end, result.box, errors, 'interval end');
    }
    const source: HitSource = { segmentId: seg.segmentId, interval: iv };
    const list = expectedByObject.get(seg.objectId);
    if (list === undefined) {
      expectedByObject.set(seg.objectId, [source]);
    } else {
      list.push(source);
    }
  }

  // 3: compare per-object merged intervals.
  const actualByObject: Map<string, MergedInterval[]> = new Map();
  let checkedSources = 0;
  for (const oh of result.objects) {
    if (actualByObject.has(oh.objectId)) {
      errors.push('duplicate object in result: ' + oh.objectId);
      continue;
    }
    actualByObject.set(oh.objectId, oh.intervals);
    if (oh.version !== result.version) {
      errors.push('object ' + oh.objectId + ' carries version ' + oh.version + ' but result version is ' + result.version);
    }
    if (oh.intervals.length === 0) {
      errors.push('object ' + oh.objectId + ' reported with zero intervals');
    }
  }
  for (const entry of expectedByObject) {
    const objectId = entry[0];
    const expectedMerged = mergeIntervals(entry[1]);
    const actual = actualByObject.get(objectId);
    if (actual === undefined) {
      errors.push('missing object in result: ' + objectId + ' (expected ' + expectedMerged.length + ' interval(s))');
      continue;
    }
    if (actual.length !== expectedMerged.length) {
      errors.push(
        'object ' + objectId + ': expected ' + expectedMerged.length +
        ' merged interval(s), got ' + actual.length
      );
      continue;
    }
    for (let i = 0; i < actual.length; i++) {
      if (!intervalEqual(actual[i], expectedMerged[i])) {
        errors.push(
          'object ' + objectId + ' interval ' + i + ': expected ' +
          intervalToString(expectedMerged[i]) + ', got ' + intervalToString(actual[i])
        );
      }
    }
  }
  for (const entry of actualByObject) {
    if (!expectedByObject.has(entry[0])) {
      errors.push('phantom object in result: ' + entry[0] + ' (no recomputed hit)');
    }
  }

  // 4 + 5: provenance, coverage, disjointness.
  for (const oh of result.objects) {
    let prev: MergedInterval | null = null;
    for (const merged of oh.intervals) {
      if (prev !== null && fracCompare(merged.start, prev.end) <= 0) {
        errors.push('object ' + oh.objectId + ': merged intervals not disjoint/sorted');
      }
      prev = merged;
      if (merged.sources.length === 0) {
        errors.push('object ' + oh.objectId + ': merged interval without sources');
        continue;
      }
      const sources: HitSource[] = [];
      for (const src of merged.sources) {
        checkedSources++;
        const seg = byId.get(src.segmentId);
        if (seg === undefined) {
          errors.push('object ' + oh.objectId + ': source segment ' + src.segmentId + ' not in version ' + result.version);
          continue;
        }
        if (seg.objectId !== oh.objectId) {
          errors.push('object ' + oh.objectId + ': source segment ' + src.segmentId + ' belongs to ' + seg.objectId);
        }
        const recomputed = recomputeHitInterval(seg, result.box, result.window);
        if (recomputed === null) {
          errors.push('object ' + oh.objectId + ': source segment ' + src.segmentId + ' does not actually hit');
          continue;
        }
        if (!intervalEqual(recomputed, src.interval)) {
          errors.push(
            'object ' + oh.objectId + ': source ' + src.segmentId + ' interval ' +
            intervalToString(src.interval) + ' != recomputed ' + intervalToString(recomputed)
          );
        }
        sources.push({ segmentId: src.segmentId, interval: recomputed });
      }
      const remerged = mergeIntervals(sources);
      if (remerged.length !== 1 || !intervalEqual(remerged[0], merged)) {
        errors.push('object ' + oh.objectId + ': sources do not cover merged interval ' + intervalToString(merged));
      }
    }
  }

  // 6: statistics consistency.
  const st = result.stats;
  if (st.version !== result.version) {
    errors.push('stats.version mismatch');
  }
  if (st.totalSegments !== segments.length) {
    errors.push('stats.totalSegments ' + st.totalSegments + ' != version size ' + segments.length);
  }
  if (st.exactTests !== st.candidateSegments) {
    errors.push('stats.exactTests != stats.candidateSegments');
  }
  if (st.hitSegments !== checkedSources) {
    errors.push('stats.hitSegments ' + st.hitSegments + ' != total sources ' + checkedSources);
  }
  if (st.prunedSegments !== st.totalSegments - st.candidateSegments) {
    errors.push('stats.prunedSegments inconsistent');
  }
  if (st.nodesVisited < st.nodesPruned || st.nodesPruned < 0) {
    errors.push('stats node counters inconsistent');
  }
  if (st.totalSegments > 0 && st.nodesVisited === 0) {
    errors.push('stats.nodesVisited is 0 despite non-empty version');
  }

  return {
    ok: errors.length === 0,
    errors: errors,
    checkedObjects: result.objects.length,
    checkedSources: checkedSources,
  };
}
