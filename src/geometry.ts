/**
 * 3D vector math, capsule geometry and ballistic contact-time solving.
 */

export interface Vec3 {
  x: number;
  y: number;
  z: number;
}

export function vec3(x = 0, y = 0, z = 0): Vec3 {
  return { x, y, z };
}

export function add(a: Vec3, b: Vec3): Vec3 {
  return { x: a.x + b.x, y: a.y + b.y, z: a.z + b.z };
}

export function sub(a: Vec3, b: Vec3): Vec3 {
  return { x: a.x - b.x, y: a.y - b.y, z: a.z - b.z };
}

export function scale(a: Vec3, s: number): Vec3 {
  return { x: a.x * s, y: a.y * s, z: a.z * s };
}

export function dot(a: Vec3, b: Vec3): number {
  return a.x * b.x + a.y * b.y + a.z * b.z;
}

export function length(a: Vec3): number {
  return Math.sqrt(dot(a, a));
}

export function distance(a: Vec3, b: Vec3): number {
  return length(sub(a, b));
}

export function clamp(v: number, lo: number, hi: number): number {
  return v < lo ? lo : v > hi ? hi : v;
}

const EPS = 1e-9;

/** Swept volume of a spherical-bodied entity over one time slot. */
export interface Capsule {
  start: Vec3;
  end: Vec3;
  radius: number;
}

export interface AABB {
  min: Vec3;
  max: Vec3;
}

export function capsuleAABB(c: Capsule): AABB {
  return {
    min: {
      x: Math.min(c.start.x, c.end.x) - c.radius,
      y: Math.min(c.start.y, c.end.y) - c.radius,
      z: Math.min(c.start.z, c.end.z) - c.radius,
    },
    max: {
      x: Math.max(c.start.x, c.end.x) + c.radius,
      y: Math.max(c.start.y, c.end.y) + c.radius,
      z: Math.max(c.start.z, c.end.z) + c.radius,
    },
  };
}

/**
 * Closest distance between segments P1Q1 and P2Q2.
 * (Christer Ericson, Real-Time Collision Detection, 5.1.9)
 */
export function segmentSegmentDistance(p1: Vec3, q1: Vec3, p2: Vec3, q2: Vec3): number {
  const d1 = sub(q1, p1);
  const d2 = sub(q2, p2);
  const r = sub(p1, p2);
  const a = dot(d1, d1);
  const e = dot(d2, d2);
  const f = dot(d2, r);

  let s: number;
  let t: number;

  if (a <= EPS && e <= EPS) {
    return length(r);
  }
  if (a <= EPS) {
    s = 0;
    t = clamp(f / e, 0, 1);
  } else {
    const c = dot(d1, r);
    if (e <= EPS) {
      t = 0;
      s = clamp(-c / a, 0, 1);
    } else {
      const b = dot(d1, d2);
      const denom = a * e - b * b;
      s = denom > EPS ? clamp((b * f - c * e) / denom, 0, 1) : 0;
      t = (b * s + f) / e;
      if (t < 0) {
        t = 0;
        s = clamp(-c / a, 0, 1);
      } else if (t > 1) {
        t = 1;
        s = clamp((b - c) / a, 0, 1);
      }
    }
  }

  const c1 = add(p1, scale(d1, s));
  const c2 = add(p2, scale(d2, t));
  return distance(c1, c2);
}

/** Minimum surface-to-surface gap between two capsules. */
export function capsuleSurfaceGap(a: Capsule, b: Capsule): number {
  return segmentSegmentDistance(a.start, a.end, b.start, b.end) - a.radius - b.radius;
}

/**
 * Earliest tau in [0, window] at which |relPos + relVel * tau| <= threshold,
 * assuming constant relative velocity. Returns null when no contact in window.
 */
export function firstContactTime(
  relPos: Vec3,
  relVel: Vec3,
  threshold: number,
  window: number,
): number | null {
  const c = dot(relPos, relPos) - threshold * threshold;
  if (c <= 0) return 0;
  const a = dot(relVel, relVel);
  if (a <= EPS) return null;
  const b = 2 * dot(relPos, relVel);
  const disc = b * b - 4 * a * c;
  if (disc < 0) return null;
  const root = (-b - Math.sqrt(disc)) / (2 * a);
  return root >= 0 && root <= window ? root : null;
}
