import { Triangle } from '../src/triangle.ts';
import { Ray } from '../src/ray.ts';
import type { Vec3 } from '../src/vec3.ts';

/** Deterministic PRNG (mulberry32) so tests and demo are reproducible. */
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

const CLUSTER_COUNT = 20;
const CLUSTER_RADIUS = 30;
const WORLD_EXTENT = 400;
const TRI_SIZE = 10;

function clusterCenters(seed: number): Vec3[] {
  const rng = makeRng(seed);
  const centers: Vec3[] = [];
  for (let i = 0; i < CLUSTER_COUNT; i++) {
    centers.push({
      x: (rng() - 0.5) * WORLD_EXTENT,
      y: (rng() - 0.5) * WORLD_EXTENT,
      z: (rng() - 0.5) * WORLD_EXTENT,
    });
  }
  return centers;
}

/**
 * A "complex" scene: triangles grouped into mesh-like clusters spread
 * through a large world box, so each ray only concerns a few clusters.
 */
export function generateScene(count: number, seed = 42): Triangle[] {
  const rng = makeRng(seed);
  const centers = clusterCenters(1000 + seed);
  const triangles: Triangle[] = [];
  for (let i = 0; i < count; i++) {
    const c = centers[i % CLUSTER_COUNT];
    const base: Vec3 = {
      x: c.x + (rng() - 0.5) * CLUSTER_RADIUS * 2,
      y: c.y + (rng() - 0.5) * CLUSTER_RADIUS * 2,
      z: c.z + (rng() - 0.5) * CLUSTER_RADIUS * 2,
    };
    const v = (): Vec3 => ({
      x: base.x + (rng() - 0.5) * TRI_SIZE,
      y: base.y + (rng() - 0.5) * TRI_SIZE,
      z: base.z + (rng() - 0.5) * TRI_SIZE,
    });
    triangles.push(new Triangle(v(), v(), v()));
  }
  return triangles;
}

/**
 * Rays shot from a surrounding sphere. When `scene` is given, ~70% of
 * rays are aimed at a random triangle centroid (with small jitter, so
 * some still miss); the rest go to random targets in the world box.
 */
export function generateRays(
  count: number,
  seed = 7,
  scene?: readonly Triangle[],
): Ray[] {
  const rng = makeRng(seed);
  const rays: Ray[] = [];
  for (let i = 0; i < count; i++) {
    const theta = rng() * Math.PI * 2;
    const phi = Math.acos(2 * rng() - 1);
    const origin: Vec3 = {
      x: 600 * Math.sin(phi) * Math.cos(theta),
      y: 600 * Math.sin(phi) * Math.sin(theta),
      z: 600 * Math.cos(phi),
    };
    let target: Vec3;
    if (scene && scene.length > 0 && rng() < 0.7) {
      const tri = scene[Math.floor(rng() * scene.length)];
      // True triangle centroid (guaranteed to lie on the triangle).
      const c = {
        x: (tri.v0.x + tri.v1.x + tri.v2.x) / 3,
        y: (tri.v0.y + tri.v1.y + tri.v2.y) / 3,
        z: (tri.v0.z + tri.v1.z + tri.v2.z) / 3,
      };
      target = {
        x: c.x + (rng() - 0.5) * TRI_SIZE * 0.15,
        y: c.y + (rng() - 0.5) * TRI_SIZE * 0.15,
        z: c.z + (rng() - 0.5) * TRI_SIZE * 0.15,
      };
    } else {
      target = {
        x: (rng() - 0.5) * WORLD_EXTENT,
        y: (rng() - 0.5) * WORLD_EXTENT,
        z: (rng() - 0.5) * WORLD_EXTENT,
      };
    }
    rays.push(
      new Ray(origin, {
        x: target.x - origin.x,
        y: target.y - origin.y,
        z: target.z - origin.z,
      }),
    );
  }
  return rays;
}

/** O(n) brute-force nearest hit — ground truth for validation. */
export function bruteForceNearest(
  triangles: readonly Triangle[],
  ray: Ray,
): { object: Triangle; t: number } | null {
  let bestT = Infinity;
  let best: Triangle | null = null;
  for (const tri of triangles) {
    const t = tri.intersectRay(ray);
    if (t !== null && t < bestT) {
      bestT = t;
      best = tri;
    }
  }
  return best === null ? null : { object: best, t: bestT };
}
