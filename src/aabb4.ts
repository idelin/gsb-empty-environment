/**
 * 4D axis-aligned bounding box: 3D space (x, y, z) + 1D time (t).
 *
 * Convention: time is represented as epoch milliseconds (number), so a
 * "4D point" is [x, y, z, t] with all four components plain numbers.
 */

export interface Vec4 {
  readonly x: number;
  readonly y: number;
  readonly z: number;
  readonly t: number;
}

export class AABB4 {
  minX: number;
  minY: number;
  minZ: number;
  minT: number;
  maxX: number;
  maxY: number;
  maxZ: number;
  maxT: number;

  constructor(
    minX = Infinity,
    minY = Infinity,
    minZ = Infinity,
    minT = Infinity,
    maxX = -Infinity,
    maxY = -Infinity,
    maxZ = -Infinity,
    maxT = -Infinity,
  ) {
    this.minX = minX;
    this.minY = minY;
    this.minZ = minZ;
    this.minT = minT;
    this.maxX = maxX;
    this.maxY = maxY;
    this.maxZ = maxZ;
    this.maxT = maxT;
  }

  static empty(): AABB4 {
    return new AABB4();
  }

  static fromPoints(a: Vec4, b: Vec4): AABB4 {
    return new AABB4(
      Math.min(a.x, b.x), Math.min(a.y, b.y), Math.min(a.z, b.z), Math.min(a.t, b.t),
      Math.max(a.x, b.x), Math.max(a.y, b.y), Math.max(a.z, b.z), Math.max(a.t, b.t),
    );
  }

  static cube(
    centerX: number, centerY: number, centerZ: number,
    halfSize: number,
    t1: number, t2: number,
  ): AABB4 {
    return new AABB4(
      centerX - halfSize, centerY - halfSize, centerZ - halfSize, t1,
      centerX + halfSize, centerY + halfSize, centerZ + halfSize, t2,
    );
  }

  clone(): AABB4 {
    return new AABB4(
      this.minX, this.minY, this.minZ, this.minT,
      this.maxX, this.maxY, this.maxZ, this.maxT,
    );
  }

  isEmpty(): boolean {
    return this.minX > this.maxX || this.minY > this.maxY
      || this.minZ > this.maxZ || this.minT > this.maxT;
  }

  expandToInclude(p: Vec4): this {
    if (p.x < this.minX) this.minX = p.x;
    if (p.y < this.minY) this.minY = p.y;
    if (p.z < this.minZ) this.minZ = p.z;
    if (p.t < this.minT) this.minT = p.t;
    if (p.x > this.maxX) this.maxX = p.x;
    if (p.y > this.maxY) this.maxY = p.y;
    if (p.z > this.maxZ) this.maxZ = p.z;
    if (p.t > this.maxT) this.maxT = p.t;
    return this;
  }

  merge(other: AABB4): this {
    if (other.minX < this.minX) this.minX = other.minX;
    if (other.minY < this.minY) this.minY = other.minY;
    if (other.minZ < this.minZ) this.minZ = other.minZ;
    if (other.minT < this.minT) this.minT = other.minT;
    if (other.maxX > this.maxX) this.maxX = other.maxX;
    if (other.maxY > this.maxY) this.maxY = other.maxY;
    if (other.maxZ > this.maxZ) this.maxZ = other.maxZ;
    if (other.maxT > this.maxT) this.maxT = other.maxT;
    return this;
  }

  static merged(a: AABB4, b: AABB4): AABB4 {
    return a.clone().merge(b);
  }

  /** 4D intersection (overlap on every axis, touching counts as hit). */
  intersects(other: AABB4): boolean {
    return this.minX <= other.maxX && this.maxX >= other.minX
      && this.minY <= other.maxY && this.maxY >= other.minY
      && this.minZ <= other.maxZ && this.maxZ >= other.minZ
      && this.minT <= other.maxT && this.maxT >= other.minT;
  }

  containsPoint(p: Vec4): boolean {
    return p.x >= this.minX && p.x <= this.maxX
      && p.y >= this.minY && p.y <= this.maxY
      && p.z >= this.minZ && p.z <= this.maxZ
      && p.t >= this.minT && p.t <= this.maxT;
  }

  /**
   * 4D hypervolume, used as the cost metric for split / insertion heuristics.
   * Degenerate (zero-extent) axes contribute a small epsilon so that flat
   * boxes still have a comparable, non-zero cost.
   */
  volume(): number {
    const e = 1e-9;
    return (this.maxX - this.minX + e)
      * (this.maxY - this.minY + e)
      * (this.maxZ - this.minZ + e)
      * (this.maxT - this.minT + e);
  }

  /** Volume of the union box of `this` and `other` (used by SAH heuristics). */
  unionVolume(other: AABB4): number {
    const e = 1e-9;
    return (Math.max(this.maxX, other.maxX) - Math.min(this.minX, other.minX) + e)
      * (Math.max(this.maxY, other.maxY) - Math.min(this.minY, other.minY) + e)
      * (Math.max(this.maxZ, other.maxZ) - Math.min(this.minZ, other.minZ) + e)
      * (Math.max(this.maxT, other.maxT) - Math.min(this.minT, other.minT) + e);
  }

  toString(): string {
    return `AABB4(x:[${this.minX}, ${this.maxX}] y:[${this.minY}, ${this.maxY}] `
      + `z:[${this.minZ}, ${this.maxZ}] t:[${this.minT}, ${this.maxT}])`;
  }
}
