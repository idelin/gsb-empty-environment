/**
 * Dynamic SDF grids: rasterize an analytic signed-distance function into a
 * regular grid, then sample it anywhere with bilinear (2D) / trilinear (3D)
 * interpolation and estimate gradients by central differences.
 */

import type { SDF2, SDF3, Vec2, Vec3 } from "./geometry.ts";

export interface Bounds2 {
  min: Vec2;
  max: Vec2;
}

export interface Bounds3 {
  min: Vec3;
  max: Vec3;
}

export class SDFGrid2D {
  readonly width: number;
  readonly height: number;
  readonly bounds: Bounds2;
  readonly cellSize: Vec2;
  /** Row-major signed distances, negative inside / positive outside. */
  readonly data: Float32Array;

  constructor(width: number, height: number, bounds: Bounds2) {
    this.width = width;
    this.height = height;
    this.bounds = bounds;
    this.cellSize = [
      (bounds.max[0] - bounds.min[0]) / (width - 1),
      (bounds.max[1] - bounds.min[1]) / (height - 1),
    ];
    this.data = new Float32Array(width * height);
  }

  /** Rasterize an analytic SDF into the grid (call again to update dynamically). */
  rasterize(sdf: SDF2): this {
    const { min } = this.bounds;
    const [cx, cy] = this.cellSize;
    for (let j = 0; j < this.height; j++) {
      for (let i = 0; i < this.width; i++) {
        this.data[j * this.width + i] = sdf([min[0] + i * cx, min[1] + j * cy]);
      }
    }
    return this;
  }

  at(i: number, j: number): number {
    const ci = Math.min(Math.max(i, 0), this.width - 1);
    const cj = Math.min(Math.max(j, 0), this.height - 1);
    return this.data[cj * this.width + ci];
  }

  /** Bilinear interpolated signed distance at an arbitrary world point. */
  sample(p: Vec2): number {
    const { min } = this.bounds;
    const gx = (p[0] - min[0]) / this.cellSize[0];
    const gy = (p[1] - min[1]) / this.cellSize[1];
    const i = Math.floor(gx);
    const j = Math.floor(gy);
    const fx = gx - i;
    const fy = gy - j;
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

  /** Outward-pointing gradient of the sampled field (central differences). */
  gradient(p: Vec2): Vec2 {
    const hx = this.cellSize[0];
    const hy = this.cellSize[1];
    return [
      (this.sample([p[0] + hx, p[1]]) - this.sample([p[0] - hx, p[1]])) /
        (2 * hx),
      (this.sample([p[0], p[1] + hy]) - this.sample([p[0], p[1] - hy])) /
        (2 * hy),
    ];
  }

  /**
   * Isosurface extraction (marching squares): returns the zero-level contour
   * as a list of line segments in world coordinates.
   */
  extractIsosurface(iso = 0): Array<[Vec2, Vec2]> {
    const segments: Array<[Vec2, Vec2]> = [];
    const { min } = this.bounds;
    const [cx, cy] = this.cellSize;
    const point = (i: number, j: number): Vec2 => [min[0] + i * cx, min[1] + j * cy];
    const lerp = (a: Vec2, b: Vec2, va: number, vb: number): Vec2 => {
      const t = (iso - va) / (vb - va);
      return [a[0] + t * (b[0] - a[0]), a[1] + t * (b[1] - a[1])];
    };
    for (let j = 0; j < this.height - 1; j++) {
      for (let i = 0; i < this.width - 1; i++) {
        const corners: Vec2[] = [
          point(i, j),
          point(i + 1, j),
          point(i + 1, j + 1),
          point(i, j + 1),
        ];
        const values = [
          this.at(i, j),
          this.at(i + 1, j),
          this.at(i + 1, j + 1),
          this.at(i, j + 1),
        ];
        // Crossing points on each of the 4 edges (bottom, right, top, left).
        const crossings: Array<Vec2 | null> = [0, 1, 2, 3].map((e) => {
          const a = e;
          const b = (e + 1) % 4;
          const va = values[a];
          const vb = values[b];
          if ((va < iso && vb >= iso) || (vb < iso && va >= iso)) {
            return lerp(corners[a], corners[b], va, vb);
          }
          return null;
        });
        const hits = crossings.filter((c): c is Vec2 => c !== null);
        if (hits.length === 2) {
          segments.push([hits[0], hits[1]]);
        } else if (hits.length === 4) {
          // Ambiguous saddle cell: connect opposite edges pairwise.
          segments.push([hits[0], hits[1]]);
          segments.push([hits[2], hits[3]]);
        }
      }
    }
    return segments;
  }
}

export class SDFGrid3D {
  readonly width: number;
  readonly height: number;
  readonly depth: number;
  readonly bounds: Bounds3;
  readonly cellSize: Vec3;
  readonly data: Float32Array;

  constructor(width: number, height: number, depth: number, bounds: Bounds3) {
    this.width = width;
    this.height = height;
    this.depth = depth;
    this.bounds = bounds;
    this.cellSize = [
      (bounds.max[0] - bounds.min[0]) / (width - 1),
      (bounds.max[1] - bounds.min[1]) / (height - 1),
      (bounds.max[2] - bounds.min[2]) / (depth - 1),
    ];
    this.data = new Float32Array(width * height * depth);
  }

  rasterize(sdf: SDF3): this {
    const { min } = this.bounds;
    const [cx, cy, cz] = this.cellSize;
    for (let k = 0; k < this.depth; k++) {
      for (let j = 0; j < this.height; j++) {
        for (let i = 0; i < this.width; i++) {
          this.data[(k * this.height + j) * this.width + i] = sdf([
            min[0] + i * cx,
            min[1] + j * cy,
            min[2] + k * cz,
          ]);
        }
      }
    }
    return this;
  }

  at(i: number, j: number, k: number): number {
    const ci = Math.min(Math.max(i, 0), this.width - 1);
    const cj = Math.min(Math.max(j, 0), this.height - 1);
    const ck = Math.min(Math.max(k, 0), this.depth - 1);
    return this.data[(ck * this.height + cj) * this.width + ci];
  }

  /** Trilinear interpolated signed distance at an arbitrary world point. */
  sample(p: Vec3): number {
    const { min } = this.bounds;
    const gx = (p[0] - min[0]) / this.cellSize[0];
    const gy = (p[1] - min[1]) / this.cellSize[1];
    const gz = (p[2] - min[2]) / this.cellSize[2];
    const i = Math.floor(gx);
    const j = Math.floor(gy);
    const k = Math.floor(gz);
    const fx = gx - i;
    const fy = gy - j;
    const fz = gz - k;
    let acc = 0;
    for (let dz = 0; dz <= 1; dz++) {
      for (let dy = 0; dy <= 1; dy++) {
        for (let dx = 0; dx <= 1; dx++) {
          const w =
            (dx ? fx : 1 - fx) * (dy ? fy : 1 - fy) * (dz ? fz : 1 - fz);
          acc += w * this.at(i + dx, j + dy, k + dz);
        }
      }
    }
    return acc;
  }

  /** Outward-pointing gradient of the sampled field (central differences). */
  gradient(p: Vec3): Vec3 {
    const [hx, hy, hz] = this.cellSize;
    return [
      (this.sample([p[0] + hx, p[1], p[2]]) -
        this.sample([p[0] - hx, p[1], p[2]])) /
        (2 * hx),
      (this.sample([p[0], p[1] + hy, p[2]]) -
        this.sample([p[0], p[1] - hy, p[2]])) /
        (2 * hy),
      (this.sample([p[0], p[1], p[2] + hz]) -
        this.sample([p[0], p[1], p[2] - hz])) /
        (2 * hz),
    ];
  }

  /**
   * Isosurface extraction (marching tetrahedra): returns the zero-level
   * surface as a triangle soup in world coordinates.
   */
  extractIsosurface(iso = 0): Array<[Vec3, Vec3, Vec3]> {
    const triangles: Array<[Vec3, Vec3, Vec3]> = [];
    const { min } = this.bounds;
    const [cx, cy, cz] = this.cellSize;
    const point = (i: number, j: number, k: number): Vec3 => [
      min[0] + i * cx,
      min[1] + j * cy,
      min[2] + k * cz,
    ];
    // Decompose each cube into 6 tetrahedra around the main diagonal.
    const cornerIndex = [
      [0, 0, 0],
      [1, 0, 0],
      [1, 1, 0],
      [0, 1, 0],
      [0, 0, 1],
      [1, 0, 1],
      [1, 1, 1],
      [0, 1, 1],
    ] as const;
    const tets = [
      [0, 5, 1, 6],
      [0, 1, 2, 6],
      [0, 2, 3, 6],
      [0, 3, 7, 6],
      [0, 7, 4, 6],
      [0, 4, 5, 6],
    ] as const;
    const lerp = (a: Vec3, b: Vec3, va: number, vb: number): Vec3 => {
      const t = (iso - va) / (vb - va);
      return [
        a[0] + t * (b[0] - a[0]),
        a[1] + t * (b[1] - a[1]),
        a[2] + t * (b[2] - a[2]),
      ];
    };
    for (let k = 0; k < this.depth - 1; k++) {
      for (let j = 0; j < this.height - 1; j++) {
        for (let i = 0; i < this.width - 1; i++) {
          const positions = cornerIndex.map(([dx, dy, dz]) =>
            point(i + dx, j + dy, k + dz),
          );
          const values = cornerIndex.map(([dx, dy, dz]) =>
            this.at(i + dx, j + dy, k + dz),
          );
          for (const tet of tets) {
            const p = tet.map((c) => positions[c]);
            const v = tet.map((c) => values[c]);
            const inside = v.map((val) => val < iso);
            const nIn = inside.filter(Boolean).length;
            if (nIn === 0 || nIn === 4) continue;
            const edges: Array<[number, number]> = [
              [0, 1],
              [0, 2],
              [0, 3],
              [1, 2],
              [1, 3],
              [2, 3],
            ];
            const crossings: Vec3[] = [];
            for (const [a, b] of edges) {
              if (inside[a] !== inside[b]) {
                crossings.push(lerp(p[a], p[b], v[a], v[b]));
              }
            }
            if (crossings.length === 3) {
              triangles.push([crossings[0], crossings[1], crossings[2]]);
            } else if (crossings.length === 4) {
              triangles.push([crossings[0], crossings[1], crossings[2]]);
              triangles.push([crossings[0], crossings[2], crossings[3]]);
            }
          }
        }
      }
    }
    return triangles;
  }
}
