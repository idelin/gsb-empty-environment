import type { Vec3 } from './vec3.ts';
import type { Ray } from './ray.ts';
import { minVec, maxVec } from './vec3.ts';

/** Axis-aligned bounding box. */
export class AABB {
  min: Vec3;
  max: Vec3;

  constructor(min: Vec3, max: Vec3) {
    this.min = min;
    this.max = max;
  }

  /** Empty (negative-volume) box, useful as a union identity. */
  static empty(): AABB {
    return new AABB(
      { x: Infinity, y: Infinity, z: Infinity },
      { x: -Infinity, y: -Infinity, z: -Infinity },
    );
  }

  static fromPoints(points: Vec3[]): AABB {
    const box = AABB.empty();
    for (const p of points) box.expandToInclude(p);
    return box;
  }

  clone(): AABB {
    return new AABB({ ...this.min }, { ...this.max });
  }

  static union(a: AABB, b: AABB): AABB {
    return new AABB(minVec(a.min, b.min), maxVec(a.max, b.max));
  }

  expandToInclude(p: Vec3): void {
    this.min = minVec(this.min, p);
    this.max = maxVec(this.max, p);
  }

  /** Surface area — the metric minimized by the Surface Area Heuristic. */
  surfaceArea(): number {
    const dx = this.max.x - this.min.x;
    const dy = this.max.y - this.min.y;
    const dz = this.max.z - this.min.z;
    return 2 * (dx * dy + dy * dz + dz * dx);
  }

  centroid(): Vec3 {
    return {
      x: (this.min.x + this.max.x) / 2,
      y: (this.min.y + this.max.y) / 2,
      z: (this.min.z + this.max.z) / 2,
    };
  }

  intersects(other: AABB): boolean {
    return (
      this.min.x <= other.max.x && this.max.x >= other.min.x &&
      this.min.y <= other.max.y && this.max.y >= other.min.y &&
      this.min.z <= other.max.z && this.max.z >= other.min.z
    );
  }

  /**
   * Slab-method ray intersection. Returns the entry distance t >= 0,
   * or null when the ray misses the box.
   */
  intersectRay(ray: Ray): number | null {
    const { origin, invDirection } = ray;

    let t1 = (this.min.x - origin.x) * invDirection.x;
    let t2 = (this.max.x - origin.x) * invDirection.x;
    let tmin = Math.min(t1, t2);
    let tmax = Math.max(t1, t2);

    t1 = (this.min.y - origin.y) * invDirection.y;
    t2 = (this.max.y - origin.y) * invDirection.y;
    tmin = Math.max(tmin, Math.min(t1, t2));
    tmax = Math.min(tmax, Math.max(t1, t2));

    t1 = (this.min.z - origin.z) * invDirection.z;
    t2 = (this.max.z - origin.z) * invDirection.z;
    tmin = Math.max(tmin, Math.min(t1, t2));
    tmax = Math.min(tmax, Math.max(t1, t2));

    if (tmax < 0 || tmin > tmax) return null;
    return tmin >= 0 ? tmin : 0; // origin inside the box -> t = 0
  }
}
