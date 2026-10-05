import { Rational } from "./rational.ts";
import { AXES } from "./types.ts";
import type { QueryBox, Segment, TimeInterval, TimeRange } from "./types.ts";

/**
 * Exact intersection of a linear trajectory segment with a closed spatial
 * box over a half-open query time range, solved with rational arithmetic.
 *
 * Position at time t: p(t) = from + d * (t - t0) / T, d = to - from,
 * T = t1 - t0 > 0. For each axis the constraint min <= p(t) <= max yields
 * a closed rational t-interval; the hit set is the intersection of the
 * three axis intervals with [max(t0, qt0), min(t1, qt1)).
 *
 * Returns null when there is no hit. A single-point touch strictly inside
 * both half-open time ranges is a degenerate hit (lo == hi, hiOpen false).
 */
export function intersectSegmentBox(
  seg: Segment,
  box: QueryBox,
  time: TimeRange,
): TimeInterval | null {
  const T = BigInt(seg.t1 - seg.t0); // > 0, validated by the index
  const t0 = BigInt(seg.t0);

  let lo = Rational.of(Math.max(seg.t0, time.t0));
  let hi = Rational.of(Math.min(seg.t1, time.t1));

  for (const axis of AXES) {
    const p0 = seg.from[axis];
    const d = seg.to[axis] - p0;
    const mn = box.min[axis];
    const mx = box.max[axis];

    if (d === 0) {
      // Stationary on this axis: either always inside or never.
      if (p0 < mn || p0 > mx) return null;
      continue;
    }

    // (mn - p0) * T / d <= t - t0 <= (mx - p0) * T / d   (swap when d < 0)
    const dd = BigInt(d);
    let a = Rational.of(BigInt(mn - p0) * T, dd);
    let b = Rational.of(BigInt(mx - p0) * T, dd);
    if (d < 0) {
      const tmp = a;
      a = b;
      b = tmp;
    }
    const tA = a.add(Rational.of(t0));
    const tB = b.add(Rational.of(t0));
    if (tA.gt(lo)) lo = tA;
    if (tB.lt(hi)) hi = tB;
    if (lo.gt(hi)) return null;
  }

  if (lo.gt(hi)) return null;

  const segT1 = Rational.of(seg.t1);
  const qT1 = Rational.of(time.t1);

  if (lo.eq(hi)) {
    // Degenerate touch point: only a hit if it lies inside both
    // half-open time ranges (spatial box is closed, so the point counts).
    if (lo.gte(segT1) || lo.gte(qT1)) return null;
    return { lo, hi, hiOpen: false };
  }

  // The right endpoint is excluded exactly when it was clamped by a
  // half-open time boundary (segment end or query end).
  const hiOpen = hi.eq(segT1) || hi.eq(qT1);
  return { lo, hi, hiOpen };
}
