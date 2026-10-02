import { AABB } from './aabb.ts';
import type { Ray } from './ray.ts';
import type { Vec3 } from './vec3.ts';
import { sub, cross, dot } from './vec3.ts';

/** A triangle in 3D space (2D scenes: keep z = 0). */
export class Triangle {
  readonly v0: Vec3;
  readonly v1: Vec3;
  readonly v2: Vec3;
  readonly bounds: AABB;

  constructor(v0: Vec3, v1: Vec3, v2: Vec3) {
    this.v0 = v0;
    this.v1 = v1;
    this.v2 = v2;
    this.bounds = AABB.fromPoints([v0, v1, v2]);
  }

  /** Möller–Trumbore intersection. Returns t >= 0 or null. */
  intersectRay(ray: Ray): number | null {
    const EPS = 1e-9;
    const e1 = sub(this.v1, this.v0);
    const e2 = sub(this.v2, this.v0);
    const p = cross(ray.direction, e2);
    const det = dot(e1, p);
    if (Math.abs(det) < EPS) return null; // parallel
    const invDet = 1 / det;
    const s = sub(ray.origin, this.v0);
    const u = dot(s, p) * invDet;
    if (u < -EPS || u > 1 + EPS) return null;
    const q = cross(s, e1);
    const v = dot(ray.direction, q) * invDet;
    if (v < -EPS || u + v > 1 + EPS) return null;
    const t = dot(e2, q) * invDet;
    return t >= 0 ? t : null;
  }
}
