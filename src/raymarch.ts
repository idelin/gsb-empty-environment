/**
 * Sphere tracing: march a ray through a signed-distance field. Every step
 * safely advances by the SDF value at the current point — the largest
 * guaranteed-empty distance — so the ray converges on the surface in very
 * few iterations.
 */

import type { Vec2, Vec3 } from "./geometry.ts";

export interface MarchOptions {
  /** Surface hit threshold (world units). Default 1e-4. */
  epsilon?: number;
  /** Maximum number of steps before giving up. Default 256. */
  maxSteps?: number;
  /** Maximum travel distance before reporting a miss. Default Infinity. */
  maxDistance?: number;
}

export interface MarchResult2 {
  hit: boolean;
  steps: number;
  distance: number;
  point: Vec2;
  /** SDF value at the final point (≈ 0 on hit). */
  finalSDF: number;
}

export interface MarchResult3 {
  hit: boolean;
  steps: number;
  distance: number;
  point: Vec3;
  finalSDF: number;
}

function norm2(v: Vec2): Vec2 {
  const len = Math.hypot(v[0], v[1]);
  if (len === 0) throw new Error("ray direction must be non-zero");
  return [v[0] / len, v[1] / len];
}

function norm3(v: Vec3): Vec3 {
  const len = Math.hypot(v[0], v[1], v[2]);
  if (len === 0) throw new Error("ray direction must be non-zero");
  return [v[0] / len, v[1] / len, v[2] / len];
}

/** March a 2D ray against a sampled 2D SDF (e.g. `grid.sample.bind(grid)`). */
export function rayMarch2D(
  sdf: (p: Vec2) => number,
  origin: Vec2,
  direction: Vec2,
  options: MarchOptions = {},
): MarchResult2 {
  const { epsilon = 1e-4, maxSteps = 256, maxDistance = Infinity } = options;
  const dir = norm2(direction);
  let t = 0;
  let p: Vec2 = [origin[0], origin[1]];
  let d = sdf(p);
  for (let step = 1; step <= maxSteps; step++) {
    if (d <= epsilon) {
      return { hit: true, steps: step - 1, distance: t, point: p, finalSDF: d };
    }
    t += d; // safe leap: the SDF bounds the distance to the nearest surface
    if (t > maxDistance) break;
    p = [origin[0] + dir[0] * t, origin[1] + dir[1] * t];
    d = sdf(p);
  }
  return { hit: false, steps: maxSteps, distance: t, point: p, finalSDF: d };
}

/** March a 3D ray against a sampled 3D SDF. */
export function rayMarch3D(
  sdf: (p: Vec3) => number,
  origin: Vec3,
  direction: Vec3,
  options: MarchOptions = {},
): MarchResult3 {
  const { epsilon = 1e-4, maxSteps = 256, maxDistance = Infinity } = options;
  const dir = norm3(direction);
  let t = 0;
  let p: Vec3 = [origin[0], origin[1], origin[2]];
  let d = sdf(p);
  for (let step = 1; step <= maxSteps; step++) {
    if (d <= epsilon) {
      return { hit: true, steps: step - 1, distance: t, point: p, finalSDF: d };
    }
    t += d;
    if (t > maxDistance) break;
    p = [origin[0] + dir[0] * t, origin[1] + dir[1] * t, origin[2] + dir[2] * t];
    d = sdf(p);
  }
  return { hit: false, steps: maxSteps, distance: t, point: p, finalSDF: d };
}
