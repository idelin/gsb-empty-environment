/**
 * 4D Axis-Aligned Bounding Box: 3D space (x, y, z) + 1D time (t).
 * All coordinates are plain numbers; time is expressed as epoch milliseconds
 * (or any monotonic numeric clock) so a trajectory segment swept over time
 * becomes a static 4D box.
 */

export interface Vec4 {
  x: number;
  y: number;
  z: number;
  t: number;
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
    minX: number,
    minY: number,
    minZ: number,
    minT: number,
    maxX: number,
    maxY: number,
    maxZ: number,
    maxT: number,
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
    return new AABB4(
      Infinity, Infinity, Infinity, Infinity,
      -Infinity, -Infinity, -Infinity, -Infinity,
    );
  }

  static fromPoints(a: Vec4, b: Vec4): AABB4 {
    return new AABB4(
      Math.min(a.x, b.x), Math.min(a.y, b.y), Math.min(a.z, b.z), Math.min(a.t, b.t),
      Math.max(a.x, b.x), Math.max(a.y, b.y), Math.max(a.z, b.z), Math.max(a.t, b.t),
    );
  }

  clone(): AABB4 {
    return new AABB4(
      this.minX, this.minY, this.minZ, this.minT,
      this.maxX, this.maxY, this.maxZ, this.maxT,
    );
  }

  expandToInclude(other: AABB4): void {
    this.minX = Math.min(this.minX, other.minX);
    this.minY = Math.min(this.minY, other.minY);
    this.minZ = Math.min(this.minZ, other.minZ);
    this.minT = Math.min(this.minT, other.minT);
    this.maxX = Math.max(this.maxX, other.maxX);
    this.maxY = Math.max(this.maxY, other.maxY);
    this.maxZ = Math.max(this.maxZ, other.maxZ);
    this.maxT = Math.max(this.maxT, other.maxT);
  }

  expanded(other: AABB4): AABB4 {
    const copy = this.clone();
    copy.expandToInclude(other);
    return copy;
  }

  intersects(other: AABB4): boolean {
    return (
      this.minX <= other.maxX && other.minX <= this.maxX &&
      this.minY <= other.maxY && other.minY <= this.maxY &&
      this.minZ <= other.maxZ && other.minZ <= this.maxZ &&
      this.minT <= other.maxT && other.minT <= this.maxT
    );
  }

  /** 4D hyper-volume, used as the cost metric for split/insertion heuristics. */
  hyperVolume(): number {
    return (
      (this.maxX - this.minX) *
      (this.maxY - this.minY) *
      (this.maxZ - this.minZ) *
      (this.maxT - this.minT)
    );
  }

  /** Enlargement of hyper-volume if `other` were merged in. */
  enlargement(other: AABB4): number {
    return this.expanded(other).hyperVolume() - this.hyperVolume();
  }

  /** Index (0=x, 1=y, 2=z, 3=t) of the axis with the largest extent. */
  widestAxis(): number {
    const ex = this.maxX - this.minX;
    const ey = this.maxY - this.minY;
    const ez = this.maxZ - this.minZ;
    const et = this.maxT - this.minT;
    let axis = 0;
    let widest = ex;
    if (ey > widest) { widest = ey; axis = 1; }
    if (ez > widest) { widest = ez; axis = 2; }
    if (et > widest) { axis = 3; }
    return axis;
  }

  centroid(axis: number): number {
    switch (axis) {
      case 0: return (this.minX + this.maxX) / 2;
      case 1: return (this.minY + this.maxY) / 2;
      case 2: return (this.minZ + this.maxZ) / 2;
      default: return (this.minT + this.maxT) / 2;
    }
  }
}

/**
 * Build a query box from a time range and a spatial cube.
 * Any bound may be +/-Infinity to leave an axis unconstrained
 * (e.g. "altitude above 100m" => minZ = 100, maxZ = Infinity).
 */
export function queryBox(
  minT: number,
  maxT: number,
  minX: number,
  maxX: number,
  minY: number,
  maxY: number,
  minZ: number,
  maxZ: number,
): AABB4 {
  return new AABB4(minX, minY, minZ, minT, maxX, maxY, maxZ, maxT);
}
