import type { Vec3 } from './vec3.ts';
import { sub, dot, add, scale, distance } from './vec3.ts';

/**
 * A capsule is the Minkowski sum of a segment and a sphere of `radius`.
 * For a moving entity, the segment sweeps from its position at the start of
 * a time slot to its extrapolated position at the end of the slot, so the
 * capsule is the entity's outer swept volume for that slot.
 */
export interface Capsule {
  start: Vec3;
  end: Vec3;
  radius: number;
}

export const makeCapsule = (start: Vec3, end: Vec3, radius: number): Capsule => ({
  start,
  end,
  radius,
});

const clamp01 = (v: number): number => (v < 0 ? 0 : v > 1 ? 1 : v);

/**
 * Minimum distance between two segments (p1-q1) and (p2-q2).
 * Standard closest-point-between-segments formulation.
 */
export const segmentSegmentDistance = (
  p1: Vec3,
  q1: Vec3,
  p2: Vec3,
  q2: Vec3,
): number => {
  const d1 = sub(q1, p1);
  const d2 = sub(q2, p2);
  const r = sub(p1, p2);
  const a = dot(d1, d1);
  const e = dot(d2, d2);
  const f = dot(d2, r);

  let s: number;
  let t: number;

  if (a <= 1e-12 && e <= 1e-12) {
    return distance(p1, p2);
  }
  if (a <= 1e-12) {
    s = 0;
    t = clamp01(f / e);
  } else {
    const c = dot(d1, r);
    if (e <= 1e-12) {
      t = 0;
      s = clamp01(-c / a);
    } else {
      const b = dot(d1, d2);
      const denom = a * e - b * b;
      s = denom > 1e-12 ? clamp01((b * f - c * e) / denom) : 0;
      t = (b * s + f) / e;
      if (t < 0) {
        t = 0;
        s = clamp01(-c / a);
      } else if (t > 1) {
        t = 1;
        s = clamp01((b - c) / a);
      }
    }
  }

  const c1 = add(p1, scale(d1, s));
  const c2 = add(p2, scale(d2, t));
  return distance(c1, c2);
};

/** Surface-to-surface minimum distance between two capsules. */
export const capsuleDistance = (a: Capsule, b: Capsule): number =>
  Math.max(
    0,
    segmentSegmentDistance(a.start, a.end, b.start, b.end) - a.radius - b.radius,
  );

/**
 * For two entities moving with constant velocity, solve for the earliest
 * future time t >= `fromT` at which their center distance equals
 * `separation`. Returns undefined if they never come that close.
 *
 * |r0 + rv * t|^2 = separation^2  ->  quadratic in t.
 */
export const timeOfSeparationBreach = (
  posA: Vec3,
  velA: Vec3,
  posB: Vec3,
  velB: Vec3,
  separation: number,
  fromT = 0,
): number | undefined => {
  const r0 = sub(posA, posB);
  const rv = sub(velA, velB);
  const a = dot(rv, rv);
  const b = 2 * dot(r0, rv);
  const c = dot(r0, r0) - separation * separation;

  if (c <= 0) return fromT; // already inside the separation bubble
  if (a <= 1e-12) return undefined; // constant distance, currently outside

  const disc = b * b - 4 * a * c;
  if (disc < 0) return undefined;

  const t = (-b - Math.sqrt(disc)) / (2 * a); // earliest root = approach
  return t >= fromT ? t : undefined;
};
