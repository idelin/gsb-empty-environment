/**
 * SDF grid rasterizer: samples an analytic SDF onto a regular lattice and
 * provides continuous (bi/tri-linear) interpolation plus gradient estimation,
 * so the sampled field itself can be ray-marched or contoured.
 */
import type { Vec2, Vec3 } from './vec.ts';
import type { Sdf2, Sdf3 } from './sdf.ts';

export interface Bounds2 { min: Vec2; max: Vec2 }
export interface Bounds3 { min: Vec3; max: Vec3 }

export class SdfGrid2D {
  readonly data: Float32Array;
  readonly cellX: number;
  readonly cellY: number;

  readonly bounds: Bounds2;
  readonly nx: number;
  readonly ny: number;

  constructor(bounds: Bounds2, nx: number, ny: number, data?: Float32Array) {
    this.bounds = bounds;
    this.nx = nx;
    this.ny = ny;
    this.data = data ?? new Float32Array(nx * ny);
    this.cellX = (bounds.max[0] - bounds.min[0]) / (nx - 1);
    this.cellY = (bounds.max[1] - bounds.min[1]) / (ny - 1);
  }

  /** Rasterize an analytic SDF into a new grid (nx × ny lattice points). */
  static fromSdf(sdf: Sdf2, bounds: Bounds2, nx: number, ny: number): SdfGrid2D {
    const grid = new SdfGrid2D(bounds, nx, ny);
    for (let j = 0; j < ny; j++) {
      for (let i = 0; i < nx; i++) {
        const x = bounds.min[0] + i * grid.cellX;
        const y = bounds.min[1] + j * grid.cellY;
        grid.data[j * nx + i] = sdf([x, y]);
      }
    }
    return grid;
  }

  at(i: number, j: number): number {
    return this.data[j * this.nx + i];
  }

  /** Bilinear-interpolated field value at an arbitrary point (clamped to bounds). */
  sample(p: Vec2): number {
    const gx = (p[0] - this.bounds.min[0]) / this.cellX;
    const gy = (p[1] - this.bounds.min[1]) / this.cellY;
    const i = Math.min(this.nx - 2, Math.max(0, Math.floor(gx)));
    const j = Math.min(this.ny - 2, Math.max(0, Math.floor(gy)));
    const fx = Math.min(1, Math.max(0, gx - i));
    const fy = Math.min(1, Math.max(0, gy - j));
    const v00 = this.at(i, j);
    const v10 = this.at(i + 1, j);
    const v01 = this.at(i, j + 1);
    const v11 = this.at(i + 1, j + 1);
    return (
      v00 * (1 - fx) * (1 - fy) +
      v10 * fx * (1 - fy) +
      v01 * (1 - fx) * fy +
      v11 * fx * fy
    );
  }

  /** Central-difference gradient of the interpolated field. */
  gradient(p: Vec2): Vec2 {
    const h = Math.min(this.cellX, this.cellY) * 0.5;
    const dx = this.sample([p[0] + h, p[1]]) - this.sample([p[0] - h, p[1]]);
    const dy = this.sample([p[0], p[1] + h]) - this.sample([p[0], p[1] - h]);
    return [dx / (2 * h), dy / (2 * h)];
  }
}

export class SdfGrid3D {
  readonly data: Float32Array;
  readonly cellX: number;
  readonly cellY: number;
  readonly cellZ: number;

  readonly bounds: Bounds3;
  readonly nx: number;
  readonly ny: number;
  readonly nz: number;

  constructor(bounds: Bounds3, nx: number, ny: number, nz: number, data?: Float32Array) {
    this.bounds = bounds;
    this.nx = nx;
    this.ny = ny;
    this.nz = nz;
    this.data = data ?? new Float32Array(nx * ny * nz);
    this.cellX = (bounds.max[0] - bounds.min[0]) / (nx - 1);
    this.cellY = (bounds.max[1] - bounds.min[1]) / (ny - 1);
    this.cellZ = (bounds.max[2] - bounds.min[2]) / (nz - 1);
  }

  /** Rasterize an analytic SDF into a new grid (nx × ny × nz lattice points). */
  static fromSdf(sdf: Sdf3, bounds: Bounds3, nx: number, ny: number, nz: number): SdfGrid3D {
    const grid = new SdfGrid3D(bounds, nx, ny, nz);
    for (let k = 0; k < nz; k++) {
      for (let j = 0; j < ny; j++) {
        for (let i = 0; i < nx; i++) {
          const x = bounds.min[0] + i * grid.cellX;
          const y = bounds.min[1] + j * grid.cellY;
          const z = bounds.min[2] + k * grid.cellZ;
          grid.data[(k * ny + j) * nx + i] = sdf([x, y, z]);
        }
      }
    }
    return grid;
  }

  at(i: number, j: number, k: number): number {
    return this.data[(k * this.ny + j) * this.nx + i];
  }

  /** Trilinear-interpolated field value at an arbitrary point (clamped to bounds). */
  sample(p: Vec3): number {
    const gx = (p[0] - this.bounds.min[0]) / this.cellX;
    const gy = (p[1] - this.bounds.min[1]) / this.cellY;
    const gz = (p[2] - this.bounds.min[2]) / this.cellZ;
    const i = Math.min(this.nx - 2, Math.max(0, Math.floor(gx)));
    const j = Math.min(this.ny - 2, Math.max(0, Math.floor(gy)));
    const k = Math.min(this.nz - 2, Math.max(0, Math.floor(gz)));
    const fx = Math.min(1, Math.max(0, gx - i));
    const fy = Math.min(1, Math.max(0, gy - j));
    const fz = Math.min(1, Math.max(0, gz - k));
    const c = (di: number, dj: number, dk: number) => this.at(i + di, j + dj, k + dk);
    const lerp = (a: number, b: number, t: number) => a + (b - a) * t;
    const x00 = lerp(c(0, 0, 0), c(1, 0, 0), fx);
    const x10 = lerp(c(0, 1, 0), c(1, 1, 0), fx);
    const x01 = lerp(c(0, 0, 1), c(1, 0, 1), fx);
    const x11 = lerp(c(0, 1, 1), c(1, 1, 1), fx);
    const y0 = lerp(x00, x10, fy);
    const y1 = lerp(x01, x11, fy);
    return lerp(y0, y1, fz);
  }

  /** Central-difference gradient of the interpolated field. */
  gradient(p: Vec3): Vec3 {
    const h = Math.min(this.cellX, this.cellY, this.cellZ) * 0.5;
    const dx = this.sample([p[0] + h, p[1], p[2]]) - this.sample([p[0] - h, p[1], p[2]]);
    const dy = this.sample([p[0], p[1] + h, p[2]]) - this.sample([p[0], p[1] - h, p[2]]);
    const dz = this.sample([p[0], p[1], p[2] + h]) - this.sample([p[0], p[1], p[2] - h]);
    return [dx / (2 * h), dy / (2 * h), dz / (2 * h)];
  }
}
