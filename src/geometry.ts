/**
 * Analytic signed-distance functions (SDFs) for geometric primitives.
 * Sign convention: negative inside the surface, positive outside.
 */

export type Vec2 = [number, number];
export type Vec3 = [number, number, number];

export type SDF2 = (p: Vec2) => number;
export type SDF3 = (p: Vec3) => number;

/** 2D circle centered at `center` with radius `r`. */
export function circle(center: Vec2, r: number): SDF2 {
  return ([x, y]) => Math.hypot(x - center[0], y - center[1]) - r;
}

/** 2D axis-aligned box centered at `center` with half-extents `half`. */
export function box2(center: Vec2, half: Vec2): SDF2 {
  return ([x, y]) => {
    const dx = Math.abs(x - center[0]) - half[0];
    const dy = Math.abs(y - center[1]) - half[1];
    const ox = Math.max(dx, 0);
    const oy = Math.max(dy, 0);
    return Math.hypot(ox, oy) + Math.min(Math.max(dx, dy), 0);
  };
}

/** 3D sphere centered at `center` with radius `r`. */
export function sphere(center: Vec3, r: number): SDF3 {
  return ([x, y, z]) =>
    Math.hypot(x - center[0], y - center[1], z - center[2]) - r;
}

/** 3D axis-aligned box centered at `center` with half-extents `half`. */
export function box3(center: Vec3, half: Vec3): SDF3 {
  return ([x, y, z]) => {
    const dx = Math.abs(x - center[0]) - half[0];
    const dy = Math.abs(y - center[1]) - half[1];
    const dz = Math.abs(z - center[2]) - half[2];
    const ox = Math.max(dx, 0);
    const oy = Math.max(dy, 0);
    const oz = Math.max(dz, 0);
    return Math.hypot(ox, oy, oz) + Math.min(Math.max(dx, dy, dz), 0);
  };
}

/** 3D torus around the Y axis: major radius `R`, minor radius `r`. */
export function torus(center: Vec3, R: number, r: number): SDF3 {
  return ([x, y, z]) => {
    const px = x - center[0];
    const py = y - center[1];
    const pz = z - center[2];
    const q = Math.hypot(px, pz) - R;
    return Math.hypot(q, py) - r;
  };
}

/** Union of two SDFs (same dimension). */
export function union<T extends SDF2 | SDF3>(a: T, b: T): T {
  return ((p: Vec2 & Vec3) => Math.min(a(p), b(p))) as T;
}

/** Smooth union of two SDFs with blend radius `k`. */
export function smoothUnion<T extends SDF2 | SDF3>(a: T, b: T, k: number): T {
  return ((p: Vec2 & Vec3) => {
    const h = Math.min(Math.max(0.5 + (0.5 * (b(p) - a(p))) / k, 0), 1);
    return b(p) * (1 - h) + a(p) * h - k * h * (1 - h);
  }) as T;
}

/** Subtract SDF `b` from SDF `a`. */
export function subtract<T extends SDF2 | SDF3>(a: T, b: T): T {
  return ((p: Vec2 & Vec3) => Math.max(a(p), -b(p))) as T;
}
