import { AABB } from "./aabb.ts";

export type Vec3 = [number, number, number];

export interface Triangle {
  v0: Vec3;
  v1: Vec3;
  v2: Vec3;
}

export function triangleAABB(tri: Triangle): AABB {
  return AABB.fromPoints([tri.v0, tri.v1, tri.v2]);
}

const EPS = 1e-9;

function sub(a: Vec3, b: Vec3): Vec3 {
  return [a[0] - b[0], a[1] - b[1], a[2] - b[2]];
}

function cross(a: Vec3, b: Vec3): Vec3 {
  return [
    a[1] * b[2] - a[2] * b[1],
    a[2] * b[0] - a[0] * b[2],
    a[0] * b[1] - a[1] * b[0],
  ];
}

function dot(a: Vec3, b: Vec3): number {
  return a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
}

/**
 * Möller–Trumbore ray/triangle intersection.
 * Returns the hit distance t >= 0, or null on a miss.
 */
export function rayTriangle(origin: Vec3, dir: Vec3, tri: Triangle): number | null {
  const e1 = sub(tri.v1, tri.v0);
  const e2 = sub(tri.v2, tri.v0);
  const p = cross(dir, e2);
  const det = dot(e1, p);
  if (det > -EPS && det < EPS) return null; // ray parallel to triangle
  const invDet = 1 / det;

  const s = sub(origin, tri.v0);
  const u = dot(s, p) * invDet;
  if (u < 0 || u > 1) return null;

  const q = cross(s, e1);
  const v = dot(dir, q) * invDet;
  if (v < 0 || u + v > 1) return null;

  const t = dot(e2, q) * invDet;
  return t >= 0 ? t : null;
}
