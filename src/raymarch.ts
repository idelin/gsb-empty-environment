/**
 * Sphere-tracing ray marcher.
 *
 * Each step advances the ray by |sdf(p)| — the exact radius of the largest
 * empty sphere around p — so the ray leaps through empty space at maximal
 * safe speed and converges geometrically onto the surface.
 */
import type { Vec2, Vec3 } from './vec.ts';
import { add2, scale2, normalize2, add3, scale3, normalize3 } from './vec.ts';
import type { Sdf2, Sdf3 } from './sdf.ts';

export interface RayMarchOptions {
  /** Hard cap on iterations (default 256). */
  maxSteps?: number;
  /** Give up beyond this travel distance (default 100). */
  maxDistance?: number;
  /** |sdf| below this counts as surface contact (default 1e-4). */
  epsilon?: number;
  /**
   * Safety factor in (0, 1] multiplied onto each step. Use < 1 (e.g. 0.9)
   * when marching a *sampled* grid whose interpolation error can make the
   * field slightly under-estimate the true clearance.
   */
  stepScale?: number;
}

export interface RayHit2 {
  hit: true;
  point: Vec2;
  distance: number;
  steps: number;
  normal: Vec2;
  sdf: number;
}
export interface RayMiss {
  hit: false;
  distance: number;
  steps: number;
}
export type RayResult2 = RayHit2 | RayMiss;

export interface RayHit3 {
  hit: true;
  point: Vec3;
  distance: number;
  steps: number;
  normal: Vec3;
  sdf: number;
}
export type RayResult3 = RayHit3 | RayMiss;

const gradient2 = (f: Sdf2, p: Vec2, h = 1e-4): Vec2 => normalize2([
  f([p[0] + h, p[1]]) - f([p[0] - h, p[1]]),
  f([p[0], p[1] + h]) - f([p[0], p[1] - h]),
]);

const gradient3 = (f: Sdf3, p: Vec3, h = 1e-4): Vec3 => normalize3([
  f([p[0] + h, p[1], p[2]]) - f([p[0] - h, p[1], p[2]]),
  f([p[0], p[1] + h, p[2]]) - f([p[0], p[1] - h, p[2]]),
  f([p[0], p[1], p[2] + h]) - f([p[0], p[1], p[2] - h]),
]);

export function rayMarch2(
  field: Sdf2,
  origin: Vec2,
  direction: Vec2,
  options: RayMarchOptions = {},
): RayResult2 {
  const { maxSteps = 256, maxDistance = 100, epsilon = 1e-4, stepScale = 1 } = options;
  const dir = normalize2(direction);
  let t = 0;
  for (let step = 0; step < maxSteps; step++) {
    const p = add2(origin, scale2(dir, t));
    const d = field(p);
    if (Math.abs(d) < epsilon) {
      return { hit: true, point: p, distance: t, steps: step + 1, normal: gradient2(field, p), sdf: d };
    }
    t += Math.abs(d) * stepScale;
    if (t > maxDistance) return { hit: false, distance: t, steps: step + 1 };
  }
  return { hit: false, distance: t, steps: maxSteps };
}

export function rayMarch3(
  field: Sdf3,
  origin: Vec3,
  direction: Vec3,
  options: RayMarchOptions = {},
): RayResult3 {
  const { maxSteps = 256, maxDistance = 100, epsilon = 1e-4, stepScale = 1 } = options;
  const dir = normalize3(direction);
  let t = 0;
  for (let step = 0; step < maxSteps; step++) {
    const p = add3(origin, scale3(dir, t));
    const d = field(p);
    if (Math.abs(d) < epsilon) {
      return { hit: true, point: p, distance: t, steps: step + 1, normal: gradient3(field, p), sdf: d };
    }
    t += Math.abs(d) * stepScale;
    if (t > maxDistance) return { hit: false, distance: t, steps: step + 1 };
  }
  return { hit: false, distance: t, steps: maxSteps };
}
