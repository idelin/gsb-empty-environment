// Exact intersection of a linear trajectory segment with a closed 3D box
// over a half-open time window. All time math is exact rational arithmetic.
//
// Position at real time t in [t0, t1):  p(t) = p0 + (p1 - p0) * (t - t0) / L
// with L = t1 - t0 > 0. For each axis we solve  lo <= p(t) <= hi  exactly,
// giving a closed rational interval [enterAxis, exitAxis]. The hit set is
//
//   [maxAxis enterAxis, minAxis exitAxis]  intersected with  [t0, t1) and
//   with the query window [w0, w1).
//
// Because the box is closed, a trajectory that merely touches the box at a
// single instant (enter === exit) still counts as a hit: it yields a
// degenerate point interval [t*, t*] with endInclusive = true.

import type { Fraction } from './fraction.js';
import {
  makeFraction,
  fractionFromInt,
  fracCompare,
  fracMin,
  fracMax,
  fracSub,
  fracAdd,
  fracMul,
  fracDiv,
} from './fraction.js';
import type { Segment, Box3, TimeWindow } from './geometry.js';

export interface TimeInterval {
  readonly start: Fraction;
  readonly end: Fraction;
  readonly endInclusive: boolean;
}

export interface HitSource {
  readonly segmentId: string;
  readonly interval: TimeInterval;
}

export interface MergedInterval {
  start: Fraction;
  end: Fraction;
  endInclusive: boolean;
  sources: HitSource[];
}

export function intersectSegmentBox(seg: Segment, box: Box3, win: TimeWindow): TimeInterval | null {
  const L = seg.t1 - seg.t0;
  const p0 = [seg.p0.x, seg.p0.y, seg.p0.z];
  const p1 = [seg.p1.x, seg.p1.y, seg.p1.z];
  const lo = [box.lo.x, box.lo.y, box.lo.z];
  const hi = [box.hi.x, box.hi.y, box.hi.z];
  let enter: Fraction | null = null;
  let exit: Fraction | null = null;
  for (let i = 0; i < 3; i++) {
    const d = p1[i] - p0[i];
    if (d === 0n) {
      if (p0[i] < lo[i] || p0[i] > hi[i]) {
        return null;
      }
      continue;
    }
    const near = d > 0n ? lo[i] : hi[i];
    const far = d > 0n ? hi[i] : lo[i];
    const e = makeFraction(seg.t0 * d + (near - p0[i]) * L, d);
    const x = makeFraction(seg.t0 * d + (far - p0[i]) * L, d);
    if (enter === null || fracCompare(e, enter) > 0) {
      enter = e;
    }
    if (exit === null || fracCompare(x, exit) < 0) {
      exit = x;
    }
  }
  let start = fracMax(fractionFromInt(seg.t0), fractionFromInt(win.t0));
  if (enter !== null) {
    start = fracMax(start, enter);
  }
  const limit = fracMin(fractionFromInt(seg.t1), fractionFromInt(win.t1));
  if (exit !== null && fracCompare(exit, limit) < 0) {
    if (fracCompare(start, exit) > 0) {
      return null;
    }
    return { start: start, end: exit, endInclusive: true };
  }
  if (fracCompare(start, limit) >= 0) {
    return null;
  }
  return { start: start, end: limit, endInclusive: false };
}

export interface Vec3Fraction {
  readonly x: Fraction;
  readonly y: Fraction;
  readonly z: Fraction;
}

export function positionAt(seg: Segment, t: Fraction): Vec3Fraction {
  const L = seg.t1 - seg.t0;
  const u = fracSub(t, fractionFromInt(seg.t0));
  const ratio = fracDiv(u, fractionFromInt(L));
  const p0 = [seg.p0.x, seg.p0.y, seg.p0.z];
  const p1 = [seg.p1.x, seg.p1.y, seg.p1.z];
  const out: Fraction[] = [];
  for (let i = 0; i < 3; i++) {
    out.push(fracAdd(fractionFromInt(p0[i]), fracMul(fractionFromInt(p1[i] - p0[i]), ratio)));
  }
  return { x: out[0], y: out[1], z: out[2] };
}
