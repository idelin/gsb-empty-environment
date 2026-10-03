/**
 * Analytic signed-distance functions (SDFs) and CSG combinators.
 * Convention: negative inside the geometry, positive outside, zero on the surface.
 */
import type { Vec2, Vec3 } from './vec.ts';
import { sub2, length2, dot2, sub3, length3, dot3 } from './vec.ts';

export type Sdf2 = (p: Vec2) => number;
export type Sdf3 = (p: Vec3) => number;

/* ------------------------------ 2D primitives ----------------------------- */

export const circle = (center: Vec2, radius: number): Sdf2 =>
  (p) => length2(sub2(p, center)) - radius;

/** Axis-aligned box centred at `center` with half extents `half`. */
export const box2 = (center: Vec2, half: Vec2): Sdf2 =>
  (p) => {
    const dx = Math.abs(p[0] - center[0]) - half[0];
    const dy = Math.abs(p[1] - center[1]) - half[1];
    const ox = Math.max(dx, 0);
    const oy = Math.max(dy, 0);
    return Math.hypot(ox, oy) + Math.min(Math.max(dx, dy), 0);
  };

/** Line segment with zero thickness. */
export const segment2 = (a: Vec2, b: Vec2): Sdf2 =>
  (p) => {
    const pa = sub2(p, a);
    const ba = sub2(b, a);
    const h = Math.min(1, Math.max(0, dot2(pa, ba) / dot2(ba, ba)));
    return length2([pa[0] - ba[0] * h, pa[1] - ba[1] * h]);
  };

/** Capsule (stadium) around a segment. */
export const capsule2 = (a: Vec2, b: Vec2, radius: number): Sdf2 =>
  (p) => segment2(a, b)(p) - radius;

/* ------------------------------ 3D primitives ----------------------------- */

export const sphere = (center: Vec3, radius: number): Sdf3 =>
  (p) => length3(sub3(p, center)) - radius;

/** Axis-aligned box centred at `center` with half extents `half`. */
export const box3 = (center: Vec3, half: Vec3): Sdf3 =>
  (p) => {
    const dx = Math.abs(p[0] - center[0]) - half[0];
    const dy = Math.abs(p[1] - center[1]) - half[1];
    const dz = Math.abs(p[2] - center[2]) - half[2];
    const ox = Math.max(dx, 0);
    const oy = Math.max(dy, 0);
    const oz = Math.max(dz, 0);
    return Math.hypot(ox, oy, oz) + Math.min(Math.max(dx, dy, dz), 0);
  };

/** Plane with unit `normal`; `offset` is the signed distance of the plane from the origin. */
export const plane3 = (normal: Vec3, offset: number): Sdf3 =>
  (p) => dot3(p, normal) - offset;

/** Torus around the Y axis: `major` ring radius, `minor` tube radius. */
export const torus3 = (center: Vec3, major: number, minor: number): Sdf3 =>
  (p) => {
    const x = p[0] - center[0];
    const y = p[1] - center[1];
    const z = p[2] - center[2];
    const q = Math.hypot(x, z) - major;
    return Math.hypot(q, y) - minor;
  };

/* ------------------------------ CSG combinators --------------------------- */

export const union2 = (...fs: Sdf2[]): Sdf2 => (p) => Math.min(...fs.map((f) => f(p)));
export const union3 = (...fs: Sdf3[]): Sdf3 => (p) => Math.min(...fs.map((f) => f(p)));

export const intersect2 = (...fs: Sdf2[]): Sdf2 => (p) => Math.max(...fs.map((f) => f(p)));
export const intersect3 = (...fs: Sdf3[]): Sdf3 => (p) => Math.max(...fs.map((f) => f(p)));

export const subtract2 = (base: Sdf2, cutter: Sdf2): Sdf2 =>
  (p) => Math.max(base(p), -cutter(p));
export const subtract3 = (base: Sdf3, cutter: Sdf3): Sdf3 =>
  (p) => Math.max(base(p), -cutter(p));

/** Polynomial smooth-min blend; `k` is the blend radius. */
const smin = (a: number, b: number, k: number): number => {
  const h = Math.min(1, Math.max(0, (b - a + k) / (2 * k)));
  return b * (1 - h) + a * h - k * h * (1 - h);
};

export const smoothUnion2 = (k: number, a: Sdf2, b: Sdf2): Sdf2 =>
  (p) => smin(a(p), b(p), k);
export const smoothUnion3 = (k: number, a: Sdf3, b: Sdf3): Sdf3 =>
  (p) => smin(a(p), b(p), k);

/* ------------------------------- Transforms ------------------------------- */

export const translate2 = (offset: Vec2, f: Sdf2): Sdf2 =>
  (p) => f(sub2(p, offset));
export const translate3 = (offset: Vec3, f: Sdf3): Sdf3 =>
  (p) => f(sub3(p, offset));

/** Uniform scale (distance stays correct up to the scale factor). */
export const scaleSdf2 = (s: number, f: Sdf2): Sdf2 =>
  (p) => f([p[0] / s, p[1] / s]) * s;
export const scaleSdf3 = (s: number, f: Sdf3): Sdf3 =>
  (p) => f([p[0] / s, p[1] / s, p[2] / s]) * s;

/** Rotate the sampling point by `angle` radians around the origin. */
export const rotate2 = (angle: number, f: Sdf2): Sdf2 => {
  const c = Math.cos(-angle);
  const s = Math.sin(-angle);
  return (p) => f([c * p[0] - s * p[1], s * p[0] + c * p[1]]);
};
