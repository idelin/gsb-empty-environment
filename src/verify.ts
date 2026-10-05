import { Rational, rmax, rmin } from "./rational.ts";
import { AXES } from "./types.ts";
import type {
  MergedInterval,
  QueryResult,
  Segment,
  SpatioTemporalQuery,
  TimeInterval,
} from "./types.ts";
import type { SpatioTemporalIndex } from "./index.ts";

/**
 * Independent recomputation of the hit interval, deliberately structured
 * differently from src/intersect.ts: it solves in the segment parameter
 * s in [0, 1) with p(s) = from + (to - from) * s and maps back to ticks,
 * so the rational arithmetic follows a different operation order.
 */
export function independentIntersect(
  seg: Segment,
  box: SpatioTemporalQuery["box"],
  time: SpatioTemporalQuery["time"],
): TimeInterval | null {
  const T = seg.t1 - seg.t0;
  let sLo = Rational.of(Math.max(0, time.t0 - seg.t0), T);
  let sHi = Rational.of(Math.min(T, time.t1 - seg.t0), T);

  for (const axis of AXES) {
    const p0 = seg.from[axis];
    const d = seg.to[axis] - p0;
    if (d === 0) {
      if (p0 < box.min[axis] || p0 > box.max[axis]) return null;
      continue;
    }
    let a = Rational.of(box.min[axis] - p0, d);
    let b = Rational.of(box.max[axis] - p0, d);
    if (d < 0) {
      const tmp = a;
      a = b;
      b = tmp;
    }
    sLo = rmax(sLo, a);
    sHi = rmin(sHi, b);
    if (sLo.gt(sHi)) return null;
  }
  if (sLo.gt(sHi)) return null;

  const toT = (s: Rational): Rational =>
    Rational.of(seg.t0).add(s.mul(Rational.of(T)));
  const lo = toT(sLo);
  const hi = toT(sHi);
  const segT1 = Rational.of(seg.t1);
  const qT1 = Rational.of(time.t1);

  if (lo.eq(hi)) {
    if (sLo.gte(Rational.of(1)) || lo.gte(qT1)) return null;
    return { lo, hi, hiOpen: false };
  }
  const hiOpen = hi.eq(segT1) || hi.eq(qT1);
  return { lo, hi, hiOpen };
}

export interface VerificationReport {
  ok: boolean;
  errors: string[];
}

function intervalEquals(a: TimeInterval, b: TimeInterval): boolean {
  return a.lo.eq(b.lo) && a.hi.eq(b.hi) && a.hiOpen === b.hiOpen;
}

function fmt(iv: TimeInterval): string {
  return `[${iv.lo}, ${iv.hi}${iv.hiOpen ? ")" : "]"}`;
}

/**
 * Verify a query result by recomputing everything from the referenced
 * source segments. Checks, per object:
 *  - the referenced trajectory version resolves in the index;
 *  - every source segmentId exists in that version's snapshot and its
 *    reported interval matches an independent recomputation;
 *  - merged intervals equal the union of their sources, are sorted and
 *    pairwise disjoint;
 *  - completeness: independently recomputing all hits of the snapshot
 *    yields exactly the reported source segments and merged intervals.
 */
export function verifyQueryResult(
  index: SpatioTemporalIndex,
  query: SpatioTemporalQuery,
  result: QueryResult,
): VerificationReport {
  const errors: string[] = [];

  const expectedObjectIds = new Set<string>();
  const expectedByObject = new Map<string, MergedInterval[]>();
  for (const objectId of index.getObjectIds()) {
    const snap = index.getSnapshot(objectId, result.version);
    if (!snap) continue;
    const hits: { segmentId: string; interval: TimeInterval }[] = [];
    for (const seg of snap.segments) {
      const iv = independentIntersect(seg, query.box, query.time);
      if (iv) hits.push({ segmentId: seg.segmentId, interval: iv });
    }
    if (hits.length > 0) {
      expectedObjectIds.add(objectId);
      expectedByObject.set(objectId, independentMerge(hits));
    }
  }

  const reportedIds = new Set<string>();
  for (const obj of result.objects) {
    reportedIds.add(obj.objectId);
    const snap = index.getSnapshot(obj.objectId, result.version);
    if (!snap) {
      errors.push(`${obj.objectId}: no snapshot at version ${result.version}`);
      continue;
    }
    if (snap.version !== obj.version) {
      errors.push(
        `${obj.objectId}: result references version ${obj.version} but version ${result.version} resolves to ${snap.version}`,
      );
    }
    const byId = new Map(snap.segments.map((s) => [s.segmentId, s]));

    let prev: MergedInterval | null = null;
    for (const merged of obj.intervals) {
      if (merged.sources.length === 0) {
        errors.push(`${obj.objectId}: merged interval ${fmt(merged)} has no sources`);
        continue;
      }
      if (prev && merged.lo.lte(prev.hi)) {
        errors.push(
          `${obj.objectId}: merged intervals not disjoint/sorted: ${fmt(prev)} then ${fmt(merged)}`,
        );
      }
      prev = merged;

      // Recompute each source interval from its referenced segment.
      let unionLo: Rational | null = null;
      let unionHi: Rational | null = null;
      let unionHiOpen = true;
      let chainHi: Rational | null = null;
      for (const src of merged.sources) {
        const seg = byId.get(src.segmentId);
        if (!seg) {
          errors.push(
            `${obj.objectId}: source segment ${src.segmentId} not found in version ${obj.version}`,
          );
          continue;
        }
        const recomputed = independentIntersect(seg, query.box, query.time);
        if (!recomputed) {
          errors.push(
            `${obj.objectId}: segment ${src.segmentId} does not actually hit, but is listed with ${fmt(src.interval)}`,
          );
          continue;
        }
        if (!intervalEquals(recomputed, src.interval)) {
          errors.push(
            `${obj.objectId}: segment ${src.segmentId} interval ${fmt(src.interval)} != recomputed ${fmt(recomputed)}`,
          );
        }
        if (chainHi && src.interval.lo.gt(chainHi)) {
          errors.push(
            `${obj.objectId}: sources of ${fmt(merged)} are not connected (gap before ${src.segmentId})`,
          );
        }
        chainHi = chainHi && chainHi.gte(src.interval.hi) ? chainHi : src.interval.hi;
        unionLo = unionLo && unionLo.lte(src.interval.lo) ? unionLo : src.interval.lo;
        if (!unionHi || src.interval.hi.gt(unionHi)) {
          unionHi = src.interval.hi;
          unionHiOpen = src.interval.hiOpen;
        } else if (src.interval.hi.eq(unionHi)) {
          unionHiOpen = unionHiOpen && src.interval.hiOpen;
        }
      }
      if (unionLo && unionHi) {
        if (!merged.lo.eq(unionLo) || !merged.hi.eq(unionHi) || merged.hiOpen !== unionHiOpen) {
          errors.push(
            `${obj.objectId}: merged interval ${fmt(merged)} != union of sources [${unionLo}, ${unionHi}${unionHiOpen ? ")" : "]"}`,
          );
        }
      }
    }

    // Completeness against the independent recomputation.
    const expected = expectedByObject.get(obj.objectId);
    if (!expected) {
      errors.push(`${obj.objectId}: reported but independent pass found no hits`);
      continue;
    }
    if (expected.length !== obj.intervals.length) {
      errors.push(
        `${obj.objectId}: ${obj.intervals.length} merged intervals reported, ${expected.length} expected`,
      );
      continue;
    }
    for (let i = 0; i < expected.length; i++) {
      const got = obj.intervals[i];
      const exp = expected[i];
      if (!got.lo.eq(exp.lo) || !got.hi.eq(exp.hi) || got.hiOpen !== exp.hiOpen) {
        errors.push(
          `${obj.objectId}: merged interval #${i} ${fmt(got)} != expected ${fmt(exp)}`,
        );
      }
      const gotSrc = got.sources.map((s) => s.segmentId).sort();
      const expSrc = exp.sources.map((s) => s.segmentId).sort();
      if (gotSrc.join(",") !== expSrc.join(",")) {
        errors.push(
          `${obj.objectId}: merged interval #${i} sources [${gotSrc}] != expected [${expSrc}]`,
        );
      }
    }
  }

  for (const id of expectedObjectIds) {
    if (!reportedIds.has(id)) {
      errors.push(`${id}: hits expected but object missing from result`);
    }
  }

  return { ok: errors.length === 0, errors };
}

/** The verifier's own merge, independent of src/merge.ts. */
function independentMerge(
  hits: { segmentId: string; interval: TimeInterval }[],
): MergedInterval[] {
  const sorted = [...hits].sort(
    (a, b) =>
      a.interval.lo.cmp(b.interval.lo) || a.interval.hi.cmp(b.interval.hi),
  );
  const out: MergedInterval[] = [];
  for (const hit of sorted) {
    const last = out[out.length - 1];
    if (last && hit.interval.lo.cmp(last.hi) <= 0) {
      if (hit.interval.hi.gt(last.hi)) {
        last.hi = hit.interval.hi;
        last.hiOpen = hit.interval.hiOpen;
      } else if (hit.interval.hi.eq(last.hi)) {
        last.hiOpen = last.hiOpen && hit.interval.hiOpen;
      }
      last.sources.push({ segmentId: hit.segmentId, interval: hit.interval });
    } else {
      out.push({
        lo: hit.interval.lo,
        hi: hit.interval.hi,
        hiOpen: hit.interval.hiOpen,
        sources: [{ segmentId: hit.segmentId, interval: hit.interval }],
      });
    }
  }
  return out;
}
