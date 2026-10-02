/**
 * Axis-Aligned Bounding Box, dimension-generic (2D, 3D, ...).
 * Bounds are stored as plain number arrays so the same code path
 * serves 2D and 3D scenes.
 */
export class AABB {
  readonly min: number[];
  readonly max: number[];

  constructor(min: number[], max: number[]) {
    if (min.length !== max.length) {
      throw new Error("AABB min/max dimension mismatch");
    }
    this.min = min.slice();
    this.max = max.slice();
  }

  get dim(): number {
    return this.min.length;
  }

  static empty(dim: number): AABB {
    return new AABB(
      new Array(dim).fill(Infinity),
      new Array(dim).fill(-Infinity),
    );
  }

  static fromPoints(points: number[][]): AABB {
    const box = AABB.empty(points[0].length);
    for (const p of points) box.expandToIncludePoint(p);
    return box;
  }

  clone(): AABB {
    return new AABB(this.min, this.max);
  }

  expandToIncludePoint(p: number[]): void {
    for (let i = 0; i < this.min.length; i++) {
      if (p[i] < this.min[i]) this.min[i] = p[i];
      if (p[i] > this.max[i]) this.max[i] = p[i];
    }
  }

  expandToIncludeBox(other: AABB): void {
    for (let i = 0; i < this.min.length; i++) {
      if (other.min[i] < this.min[i]) this.min[i] = other.min[i];
      if (other.max[i] > this.max[i]) this.max[i] = other.max[i];
    }
  }

  static union(a: AABB, b: AABB): AABB {
    const out = a.clone();
    out.expandToIncludeBox(b);
    return out;
  }

  /** Returns a copy enlarged by `margin` on every side. */
  fattened(margin: number): AABB {
    const min = this.min.map((v) => v - margin);
    const max = this.max.map((v) => v + margin);
    return new AABB(min, max);
  }

  extent(axis: number): number {
    return this.max[axis] - this.min[axis];
  }

  centroid(): number[] {
    const c = new Array(this.dim);
    for (let i = 0; i < this.dim; i++) c[i] = (this.min[i] + this.max[i]) * 0.5;
    return c;
  }

  /**
   * Surface area of the box. For 3D this is the classic surface area
   * 2*(xy+yz+zx); for 2D it degenerates to the perimeter 2*(x+y).
   * General form: 2 * sum_i ( volume / extent_i ).
   */
  surfaceArea(): number {
    let sum = 0;
    for (let i = 0; i < this.dim; i++) {
      let prod = 1;
      for (let j = 0; j < this.dim; j++) {
        if (j !== i) prod *= Math.max(0, this.max[j] - this.min[j]);
      }
      sum += prod;
    }
    return 2 * sum;
  }

  contains(other: AABB): boolean {
    for (let i = 0; i < this.dim; i++) {
      if (other.min[i] < this.min[i] || other.max[i] > this.max[i]) return false;
    }
    return true;
  }

  overlaps(other: AABB): boolean {
    for (let i = 0; i < this.dim; i++) {
      if (this.min[i] > other.max[i] || other.min[i] > this.max[i]) return false;
    }
    return true;
  }

  /**
   * Slab-method ray test. `invDir` must be the component-wise inverse of the
   * ray direction (1/d, Infinity allowed). Returns the entry distance tNear
   * if the ray hits the box within [0, maxT], otherwise null.
   */
  rayHit(origin: number[], invDir: number[], maxT: number): number | null {
    let tNear = 0;
    let tFar = maxT;
    for (let i = 0; i < this.dim; i++) {
      let t1 = (this.min[i] - origin[i]) * invDir[i];
      let t2 = (this.max[i] - origin[i]) * invDir[i];
      if (t1 > t2) {
        const tmp = t1;
        t1 = t2;
        t2 = tmp;
      }
      if (t1 > tNear) tNear = t1;
      if (t2 < tFar) tFar = t2;
      if (tNear > tFar) return null;
    }
    return tNear;
  }
}
