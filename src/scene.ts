import { AABB } from "./aabb.ts";
import type { BVHItem } from "./bvh.ts";
import { triangleAABB, type Triangle, type Vec3 } from "./triangle.ts";

/** Deterministic PRNG (mulberry32) so demos and tests are reproducible. */
export function makeRng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Generate `count` triangles grouped into `clusters` spatial clusters,
 * mimicking a scene of distinct complex objects rather than uniform noise.
 */
export function generateTriangles(count: number, clusters: number, seed = 42): Triangle[] {
  const rng = makeRng(seed);
  const centers: Vec3[] = [];
  for (let i = 0; i < clusters; i++) {
    centers.push([rng() * 200 - 100, rng() * 200 - 100, rng() * 200 - 100]);
  }
  const tris: Triangle[] = [];
  for (let i = 0; i < count; i++) {
    const c = centers[Math.floor(rng() * clusters)];
    const spread = 6;
    const p = (): Vec3 => [
      c[0] + (rng() * 2 - 1) * spread,
      c[1] + (rng() * 2 - 1) * spread,
      c[2] + (rng() * 2 - 1) * spread,
    ];
    tris.push({ v0: p(), v1: p(), v2: p() });
  }
  return tris;
}

export function triangleItems(tris: Triangle[]): BVHItem<Triangle>[] {
  return tris.map((tri) => ({ aabb: triangleAABB(tri), data: tri }));
}

export interface Ray3 {
  origin: Vec3;
  dir: Vec3;
}

/**
 * Rays fired from a sphere around the scene towards random cluster points,
 * so a realistic mix of hits and misses occurs.
 */
export function generateRays(count: number, seed = 7): Ray3[] {
  const rng = makeRng(seed);
  const rays: Ray3[] = [];
  for (let i = 0; i < count; i++) {
    const theta = rng() * Math.PI * 2;
    const phi = Math.acos(rng() * 2 - 1);
    const r = 260;
    const origin: Vec3 = [
      r * Math.sin(phi) * Math.cos(theta),
      r * Math.sin(phi) * Math.sin(theta),
      r * Math.cos(phi),
    ];
    const target: Vec3 = [rng() * 160 - 80, rng() * 160 - 80, rng() * 160 - 80];
    const d: Vec3 = [target[0] - origin[0], target[1] - origin[1], target[2] - origin[2]];
    const len = Math.hypot(d[0], d[1], d[2]);
    rays.push({ origin, dir: [d[0] / len, d[1] / len, d[2] / len] });
  }
  return rays;
}

/** Brute-force nearest hit over all triangles — the ground truth. */
export function bruteForceRaycast(
  tris: Triangle[],
  origin: Vec3,
  dir: Vec3,
  rayTriangleFn: (o: Vec3, d: Vec3, t: Triangle) => number | null,
): { index: number; t: number } | null {
  let bestT = Infinity;
  let bestIndex = -1;
  for (let i = 0; i < tris.length; i++) {
    const t = rayTriangleFn(origin, dir, tris[i]);
    if (t !== null && t < bestT) {
      bestT = t;
      bestIndex = i;
    }
  }
  return bestIndex < 0 ? null : { index: bestIndex, t: bestT };
}
